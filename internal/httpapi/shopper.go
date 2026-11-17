package httpapi

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "regexp"
    "strconv"
    "time"

    "github.com/jackc/pgx/v5/pgconn"
)

// No shopper-facing identity or payroll approval surface is exposed.
type ShopperAssignment struct {
    OrderID               string `json:"orderId"`
    ShopperActor          string `json:"shopperActor"`
    CompletionAmountCents int64  `json:"completionAmountCents"`
    JurisdictionRef       string `json:"jurisdictionRef"`
}
type ShopperCompletion struct {
    ItemsConfirmed         bool   `json:"itemsConfirmed"`
    SubstitutionsConfirmed bool   `json:"substitutionsConfirmed"`
    ReceiptRef             string `json:"receiptRef"`
    HandoffRef             string `json:"handoffRef"`
}
type ShopperRepository interface {
    AssignShopperTask(context.Context, string, string, ShopperAssignment) (json.RawMessage, error)
    CompleteShopperTask(context.Context, string, string, string, ShopperCompletion) (json.RawMessage, error)
    ShopperTasks(context.Context, int, int) ([]json.RawMessage, error)
}

var shopperReference = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func (s *Server) shopperRepository(w http.ResponseWriter) (ShopperRepository, bool) {
    p, ok := s.repo.(ShopperRepository)
    if !ok {
        writeError(w, 503, "shopper completion records unavailable")
    }
    return p, ok
}
func (s *Server) shopperError(w http.ResponseWriter, err error) {
    var pg *pgconn.PgError
    switch {
    case errors.Is(err, ErrNotFound):
        writeError(w, 404, "shopper task or merchant order not found")
    case errors.Is(err, ErrConflict):
        writeError(w, 409, "shopper task, ownership or idempotency conflict")
    case errors.As(err, &pg) && (pg.Code == "42P01" || pg.Code == "42883"):
        writeError(w, 503, "shopper completion records unavailable")
    case errors.As(err, &pg) && pg.Code == "22023":
        writeError(w, 400, "invalid shopper task or evidence")
    default:
        s.databaseError(w, err)
    }
}
func (s *Server) assignShopperTask(w http.ResponseWriter, r *http.Request) {
    actor, ok := s.authorizedActorRole(w, r, "admin")
    if !ok {
        return
    }
    var request ShopperAssignment
    if !financeBody(w, r, &request, "orderId", "shopperActor", "completionAmountCents", "jurisdictionRef") {
        return
    }
    key := r.Header.Get("Idempotency-Key")
    if !isUUID(key) || !isUUID(request.OrderID) || !shopperReference.MatchString(request.ShopperActor) ||
        !shopperReference.MatchString(request.JurisdictionRef) || request.CompletionAmountCents < 1 || request.CompletionAmountCents > 100000000 {
        writeError(w, 400, "UUID key/order, opaque references and explicitly approved 1..100000000 cents required")
        return
    }
    p, ok := s.shopperRepository(w)
    if !ok {
        return
    }
    ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
    defer cancel()
    result, err := p.AssignShopperTask(ctx, actor, key, request)
    if err != nil {
        s.shopperError(w, err)
        return
    }
    writeJSON(w, 201, result)
}
func (s *Server) completeShopperTask(w http.ResponseWriter, r *http.Request) {
    actor, ok := s.authorizedActorRole(w, r, "admin")
    if !ok {
        return
    }
    var request ShopperCompletion
    if !financeBody(w, r, &request, "itemsConfirmed", "substitutionsConfirmed", "receiptRef", "handoffRef") {
        return
    }
    key, id := r.Header.Get("Idempotency-Key"), r.PathValue("taskId")
    if !isUUID(key) || !isUUID(id) || !request.ItemsConfirmed || !request.SubstitutionsConfirmed ||
        !shopperReference.MatchString(request.ReceiptRef) || !shopperReference.MatchString(request.HandoffRef) {
        writeError(w, 400, "UUID key/task and verified items, substitutions and opaque receipt/handoff references required")
        return
    }
    p, ok := s.shopperRepository(w)
    if !ok {
        return
    }
    ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
    defer cancel()
    result, err := p.CompleteShopperTask(ctx, actor, key, id, request)
    if err != nil {
        s.shopperError(w, err)
        return
    }
    writeJSON(w, 200, result)
}
func (s *Server) shopperTasks(w http.ResponseWriter, r *http.Request) {
    if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
        return
    }
    limit, offset := 50, 0
    q := r.URL.Query()
    if raw := q.Get("limit"); raw != "" {
        n, err := strconv.Atoi(raw)
        if err != nil {
            writeError(w, 400, "invalid limit")
            return
        }
        limit = n
    }
    if raw := q.Get("offset"); raw != "" {
        n, err := strconv.Atoi(raw)
        if err != nil {
            writeError(w, 400, "invalid offset")
            return
        }
        offset = n
    }
    if limit < 1 || limit > 100 || offset < 0 || offset > 100000 {
        writeError(w, 400, "limit 1..100 and offset 0..100000 required")
        return
    }
    p, ok := s.shopperRepository(w)
    if !ok {
        return
    }
    ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
    defer cancel()
    tasks, err := p.ShopperTasks(ctx, limit, offset)
    if err != nil {
        s.shopperError(w, err)
        return
    }
    writeJSON(w, 200, map[string]any{"tasks": tasks, "limit": limit, "offset": offset})
}
func shopperDBError(err error) error {
    var pg *pgconn.PgError
    if errors.As(err, &pg) {
        switch pg.Code {
        case "23505":
            return ErrConflict
        case "P0002":
            return ErrNotFound
        }
    }
    return err
}
func (p *PostgresRepository) AssignShopperTask(ctx context.Context, actor, key string, r ShopperAssignment) (json.RawMessage, error) {
    var raw json.RawMessage
    err := p.db.QueryRowContext(ctx, `SELECT assign_shopper_task($1::uuid,$2,$3::uuid,$4,$5,$6)`, key, actor, r.OrderID, r.ShopperActor, r.CompletionAmountCents, r.JurisdictionRef).Scan(&raw)
    return raw, shopperDBError(err)
}
func (p *PostgresRepository) CompleteShopperTask(ctx context.Context, actor, key, id string, r ShopperCompletion) (json.RawMessage, error) {
    var raw json.RawMessage
    err := p.db.QueryRowContext(ctx, `SELECT complete_shopper_task($1::uuid,$2,$3::uuid,$4,$5,$6,$7)`, key, actor, id, r.ItemsConfirmed, r.SubstitutionsConfirmed, r.ReceiptRef, r.HandoffRef).Scan(&raw)
    return raw, shopperDBError(err)
}
func (p *PostgresRepository) ShopperTasks(ctx context.Context, limit, offset int) ([]json.RawMessage, error) {
    rows, err := p.db.QueryContext(ctx, `SELECT record FROM shopper_task_records ORDER BY created_at,id LIMIT $1 OFFSET $2`, limit, offset)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    tasks := []json.RawMessage{}
    for rows.Next() {
        var raw json.RawMessage
        if err := rows.Scan(&raw); err != nil {
            return nil, err
        }
        tasks = append(tasks, raw)
    }
    return tasks, rows.Err()
}