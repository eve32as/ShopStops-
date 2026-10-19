package httpapi

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrIneligible = errors.New("not eligible")
)

type Server struct {
	repo  Repository
	token string
	log   *slog.Logger
}

type Repository interface {
	PingContext(context.Context) error
	NearbyMerchants(context.Context, string, int) ([]NearbyMerchant, error)
	CreateBundle(context.Context, bundleRequest, string) (bundleResponse, error)
}

type NearbyMerchant struct {
	MerchantID     string  `json:"merchantId"`
	Name           string  `json:"name"`
	MerchantType   string  `json:"merchantType"`
	StoreLocation  string  `json:"storeLocationId"`
	DistanceMeters float64 `json:"distanceMeters"`
}

type bundleRequest struct {
	SeedOrderID         string  `json:"seedOrderId"`
	CapacityLiters      float64 `json:"capacityLiters"`
	MaxOrders           *int    `json:"maxOrders"`
	PickupRadiusMeters  *int    `json:"pickupRadiusMeters"`
	DropoffRadiusMeters *int    `json:"dropoffRadiusMeters"`
}

type bundleStop struct {
	ID              string  `json:"id"`
	OrderID         string  `json:"orderId"`
	Sequence        int     `json:"sequence"`
	Type            string  `json:"type"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Status          string  `json:"status"`
	StoreLocationID *string `json:"storeLocationId,omitempty"`
}

type bundleResponse struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Stops  []bundleStop `json:"stops"`
}

func New(repo Repository, token string, logger *slog.Logger) http.Handler {
	s := &Server{repo: repo, token: token, log: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.Handle("GET /stores/{storeLocationId}/nearby-merchants", s.authorized(http.HandlerFunc(s.nearbyMerchants)))
	mux.Handle("POST /dispatch/bundles", s.authorized(http.HandlerFunc(s.createBundle)))
	return mux
}

func (s *Server) authorized(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if value == r.Header.Get("Authorization") ||
			subtle.ConstantTimeCompare([]byte(value), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.repo.PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) nearbyMerchants(w http.ResponseWriter, r *http.Request) {
	storeID := r.PathValue("storeLocationId")
	if !isUUID(storeID) {
		writeError(w, http.StatusBadRequest, "invalid storeLocationId")
		return
	}

	radius := 500
	if raw := r.URL.Query().Get("radiusMeters"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 500 {
			writeError(w, http.StatusBadRequest, "radiusMeters must be between 0 and 500")
			return
		}
		radius = value
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	merchants, err := s.repo.NearbyMerchants(ctx, storeID, radius)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "store location not found")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, merchants)
}

func (s *Server) createBundle(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request bundleRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	if !isUUID(request.SeedOrderID) || request.CapacityLiters <= 0 ||
		request.CapacityLiters > 10000 || optionalInt(request.MaxOrders, 3) < 1 ||
		optionalInt(request.MaxOrders, 3) > 100 ||
		optionalInt(request.PickupRadiusMeters, 2000) > 100000 ||
		optionalInt(request.DropoffRadiusMeters, 2000) > 100000 ||
		optionalInt(request.PickupRadiusMeters, 2000) < 0 ||
		optionalInt(request.DropoffRadiusMeters, 2000) < 0 {
		writeError(w, http.StatusBadRequest, "invalid bundle parameters")
		return
	}
	request.MaxOrders = intPointer(optionalInt(request.MaxOrders, 3))
	request.PickupRadiusMeters = intPointer(optionalInt(request.PickupRadiusMeters, 2000))
	request.DropoffRadiusMeters = intPointer(optionalInt(request.DropoffRadiusMeters, 2000))
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) == 0 || len(idempotencyKey) > 200 || strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	response, err := s.repo.CreateBundle(ctx, request, idempotencyKey)
	if errors.Is(err, ErrIneligible) {
		writeError(w, http.StatusConflict, "seed order is not eligible for dispatch")
		return
	}
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (p *PostgresRepository) PingContext(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

func (p *PostgresRepository) NearbyMerchants(
	ctx context.Context, storeID string, radius int,
) ([]NearbyMerchant, error) {
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM store_locations WHERE id = $1 AND active)`,
		storeID,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT merchant_id, merchant_name, merchant_type, store_location_id, distance_meters
		FROM nearby_subscribed_merchants($1, $2)`, storeID, radius)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	merchants := make([]NearbyMerchant, 0)
	for rows.Next() {
		var merchant NearbyMerchant
		if err := rows.Scan(
			&merchant.MerchantID, &merchant.Name, &merchant.MerchantType,
			&merchant.StoreLocation, &merchant.DistanceMeters,
		); err != nil {
			return nil, err
		}
		merchants = append(merchants, merchant)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return merchants, nil
}

func (p *PostgresRepository) CreateBundle(
	ctx context.Context, request bundleRequest, idempotencyKey string,
) (bundleResponse, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return bundleResponse{}, err
	}
	defer tx.Rollback()
	var bundleID string
	err = tx.QueryRowContext(ctx, `
		SELECT create_proximity_bundle($1, $2, $3, $4, $5, $6)`,
		request.SeedOrderID, idempotencyKey, request.CapacityLiters,
		*request.MaxOrders, *request.PickupRadiusMeters, *request.DropoffRadiusMeters,
	).Scan(&bundleID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return bundleResponse{}, ErrIneligible
		}
		return bundleResponse{}, err
	}
	response := bundleResponse{ID: bundleID, Status: "active", Stops: make([]bundleStop, 0)}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, order_id, sequence, stop_type,
		       ST_Y(location::geometry), ST_X(location::geometry), status,
		       store_location_id
		FROM delivery_stops WHERE bundle_id = $1 ORDER BY sequence`, bundleID)
	if err != nil {
		return bundleResponse{}, err
	}
	for rows.Next() {
		var stop bundleStop
		var storeID sql.NullString
		if err := rows.Scan(
			&stop.ID, &stop.OrderID, &stop.Sequence, &stop.Type,
			&stop.Latitude, &stop.Longitude, &stop.Status, &storeID,
		); err != nil {
			rows.Close()
			return bundleResponse{}, err
		}
		if storeID.Valid {
			stop.StoreLocationID = &storeID.String
		}
		response.Stops = append(response.Stops, stop)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return bundleResponse{}, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return bundleResponse{}, err
	}
	return response, nil
}

func (s *Server) databaseError(w http.ResponseWriter, err error) {
	s.log.Error("database request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "request could not be completed")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func optionalInt(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func intPointer(value int) *int {
	return &value
}
