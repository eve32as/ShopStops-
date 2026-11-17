package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/eve32as/ShopStops-/internal/finance"
	"github.com/jackc/pgx/v5/pgconn"
)

type FinanceCostRequest struct {
	Period      string `json:"period"` // YYYY-MM, UTC accounting period; not wage/payment date
	Category    string `json:"category"`
	AmountCents int64  `json:"amountCents"`
}
type FinanceCost struct {
	ID string `json:"id"`
	FinanceCostRequest
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"createdAt"`
	Kind      string    `json:"kind"`
}
type FinanceCostReport struct {
	Kind       string        `json:"kind"`
	Period     string        `json:"period"`
	TotalCents string        `json:"totalCents"` // decimal integer string: ledger totals are unbounded by page size
	Costs      []FinanceCost `json:"costs"`
	Limit      int           `json:"limit"`
	Offset     int           `json:"offset"`
}

// Optional: existing repository implementations need not support the manual ledger.
type FinanceRepository interface {
	CreateFinanceCost(context.Context, string, string, FinanceCostRequest) (FinanceCost, error)
	FinanceCosts(context.Context, string, int, int) (FinanceCostReport, error)
}

func financeBody(w http.ResponseWriter, r *http.Request, target any, required ...string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	d := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := d.Decode(&raw); err != nil {
		writeError(w, 400, "invalid finance request body")
		return false
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "request body must contain one JSON object")
		return false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		writeError(w, 400, "finance request must be an object")
		return false
	}
	for _, key := range required {
		if _, ok := object[key]; !ok {
			writeError(w, 400, "required finance field is missing")
			return false
		}
	}
	for _, value := range object {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			writeError(w, 400, "finance fields must not be null")
			return false
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		writeError(w, 400, "invalid finance fields")
		return false
	}
	return true
}
func (s *Server) financeScenario(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	var request finance.Scenario
	if !financeBody(w, r, &request, "dropsPerBundle") {
		return
	}
	result, err := finance.Calculate(request)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, result)
}
func validFinancePeriod(period string) bool {
	t, err := time.Parse("2006-01", period)
	return err == nil && t.Year() >= 2000 && t.Year() <= 2100 && t.Format("2006-01") == period
}
func validFinanceCost(c FinanceCostRequest) bool {
	if !validFinancePeriod(c.Period) || c.AmountCents < 0 || c.AmountCents > finance.MaxMoney {
		return false
	}
	switch c.Category {
	case "driver", "staff", "hosting", "processing_excess", "refund_loss", "other":
		return true
	}
	return false
}
func (s *Server) financeRepository(w http.ResponseWriter) (FinanceRepository, bool) {
	p, ok := s.repo.(FinanceRepository)
	if !ok {
		writeError(w, 503, "manual finance ledger unavailable")
	}
	return p, ok
}
func (s *Server) financeError(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "42P01" || pg.Code == "42883") {
		writeError(w, 503, "manual finance ledger unavailable")
		return
	}
	s.databaseError(w, err)
}
func (s *Server) createFinanceCost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRole(w, r, "admin")
	if !ok {
		return
	}
	var request FinanceCostRequest
	if !financeBody(w, r, &request, "period", "category", "amountCents") {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !isUUID(key) || !validFinanceCost(request) {
		writeError(w, 400, "valid UUID Idempotency-Key, UTC period, category and 0..100000000 cents required")
		return
	}
	p, ok := s.financeRepository(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	cost, err := p.CreateFinanceCost(ctx, actor, key, request)
	switch {
	case errors.Is(err, ErrConflict):
		writeError(w, 409, "cost idempotency key conflicts with another actor or payload")
	case err != nil:
		s.financeError(w, err)
	default:
		writeJSON(w, 201, cost)
	}
}
func (s *Server) financeCosts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	q := r.URL.Query()
	period := q.Get("period")
	limit, offset := 50, 0
	var err error
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			limit = 0
		}
	}
	if raw := q.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil {
			offset = -1
		}
	}
	if !validFinancePeriod(period) || limit < 1 || limit > 100 || offset < 0 || offset > 100000 {
		writeError(w, 400, "UTC period YYYY-MM (2000..2100), limit 1..100 and offset 0..100000 required")
		return
	}
	p, ok := s.financeRepository(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	report, err := p.FinanceCosts(ctx, period, limit, offset)
	if err != nil {
		s.financeError(w, err)
		return
	}
	writeJSON(w, 200, report)
}

func (p *PostgresRepository) CreateFinanceCost(ctx context.Context, actor, key string, c FinanceCostRequest) (FinanceCost, error) {
	var cost FinanceCost
	err := p.db.QueryRowContext(ctx, `SELECT id, period, category, amount_cents, actor, created_at
		FROM record_finance_cost($1::uuid,$2,$3,$4,$5)`, key, actor, c.Period, c.Category, c.AmountCents).Scan(
		&cost.ID, &cost.Period, &cost.Category, &cost.AmountCents, &cost.Actor, &cost.CreatedAt)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return FinanceCost{}, ErrConflict
	}
	cost.Kind = "manual_cost"
	cost.CreatedAt = cost.CreatedAt.UTC()
	return cost, err
}
func (p *PostgresRepository) FinanceCosts(ctx context.Context, period string, limit, offset int) (FinanceCostReport, error) {
	// A single statement provides one consistent snapshot for total and page,
	// including an empty page beyond the final record. Numeric sum avoids bigint overflow.
	var raw []byte
	err := p.db.QueryRowContext(ctx, `SELECT jsonb_build_object(
		'kind','manual_cost_report','period',$1::text,
		'totalCents',(SELECT COALESCE(sum(amount_cents),0)::text FROM finance_costs WHERE period=$1),
		'limit',$2::int,'offset',$3::int,'costs',COALESCE((SELECT jsonb_agg(to_jsonb(page) ORDER BY page."createdAt",page.id) FROM (
		 SELECT id,period,category,amount_cents AS "amountCents",actor,
		 to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS "createdAt",'manual_cost' AS kind
		 FROM finance_costs WHERE period=$1 ORDER BY created_at,id LIMIT $2 OFFSET $3
		) page),'[]'::jsonb))`, period, limit, offset).Scan(&raw)
	if err != nil {
		return FinanceCostReport{}, err
	}
	var report FinanceCostReport
	err = json.Unmarshal(raw, &report)
	return report, err
}
