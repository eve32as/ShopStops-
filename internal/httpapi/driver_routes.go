package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type createAssignedRouteRequest struct {
	CheckoutID string `json:"checkoutId"`
	DriverRef  string `json:"driverRef"`
}

func (s *Server) listDriverRoutes(w http.ResponseWriter, r *http.Request) {
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	routes, err := s.repo.DriverRoutes(ctx, driverRef)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (s *Server) createAssignedRoute(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request createAssignedRouteRequest
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
	request.DriverRef = strings.TrimSpace(request.DriverRef)
	if !isUUID(request.CheckoutID) || request.DriverRef == "" || len(request.DriverRef) > 200 {
		writeError(w, http.StatusBadRequest, "valid checkoutId and driverRef are required")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	route, err := s.repo.CreateAssignedRoute(ctx, request.CheckoutID, request.DriverRef, idempotencyKey)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "paid checkout not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "checkout already has a different or inactive route")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusCreated, route)
	}
}

func (p *PostgresRepository) DriverRoutes(
	ctx context.Context, driverRef string,
) ([]routeResponse, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id
		FROM unified_delivery_routes
		WHERE driver_ref = $1 AND status IN ('planned', 'routed', 'in_progress')
		ORDER BY created_at DESC, id
		LIMIT 50`, driverRef)
	if err != nil {
		return nil, err
	}
	routeIDs := make([]string, 0)
	for rows.Next() {
		var routeID string
		if err := rows.Scan(&routeID); err != nil {
			rows.Close()
			return nil, err
		}
		routeIDs = append(routeIDs, routeID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	routes := make([]routeResponse, 0, len(routeIDs))
	for _, routeID := range routeIDs {
		route, err := p.DriverRoute(ctx, routeID, driverRef)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func (p *PostgresRepository) CreateAssignedRoute(
	ctx context.Context, checkoutID, driverRef, idempotencyKey string,
) (routeResponse, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return routeResponse{}, err
	}
	defer tx.Rollback()

	var routeID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM unified_delivery_routes WHERE idempotency_key = $1 FOR UPDATE`,
		idempotencyKey,
	).Scan(&routeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return routeResponse{}, err
	}
	if err == nil {
		var storedCheckoutID, assignedDriver string
		var status string
		if err := tx.QueryRowContext(ctx, `
			SELECT checkout_id::text, COALESCE(driver_ref, ''), status
			FROM unified_delivery_routes WHERE id = $1`,
			routeID,
		).Scan(&storedCheckoutID, &assignedDriver, &status); err != nil {
			return routeResponse{}, err
		}
		if storedCheckoutID != checkoutID || assignedDriver != driverRef || status == "cancelled" {
			return routeResponse{}, ErrConflict
		}
		if err := tx.Commit(); err != nil {
			return routeResponse{}, err
		}
		return p.DriverRoute(ctx, routeID, driverRef)
	}

	var checkoutStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM unified_checkouts WHERE id = $1 FOR UPDATE`,
		checkoutID,
	).Scan(&checkoutStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return routeResponse{}, ErrNotFound
		}
		return routeResponse{}, err
	}
	if checkoutStatus != "paid" {
		return routeResponse{}, ErrConflict
	}
	var checkoutHasRoute bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM unified_delivery_routes WHERE checkout_id = $1)`,
		checkoutID,
	).Scan(&checkoutHasRoute); err != nil {
		return routeResponse{}, err
	}
	if checkoutHasRoute {
		return routeResponse{}, ErrConflict
	}

	if err := tx.QueryRowContext(ctx,
		`SELECT create_unified_route($1, $2, 'osrm')::text`,
		checkoutID, idempotencyKey,
	).Scan(&routeID); err != nil {
		return routeResponse{}, err
	}

	var storedCheckoutID, assignedDriver, status string
	err = tx.QueryRowContext(ctx, `
		SELECT checkout_id::text, COALESCE(driver_ref, ''), status
		FROM unified_delivery_routes WHERE id = $1 FOR UPDATE`,
		routeID,
	).Scan(&storedCheckoutID, &assignedDriver, &status)
	if err != nil {
		return routeResponse{}, err
	}
	if storedCheckoutID != checkoutID ||
		(assignedDriver != "" && assignedDriver != driverRef) ||
		status == "cancelled" {
		return routeResponse{}, ErrConflict
	}
	if status != "completed" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE unified_delivery_routes
			SET driver_ref = $2, status = 'in_progress'
			WHERE id = $1 AND status IN ('planned', 'routed', 'in_progress')`,
			routeID, driverRef,
		); err != nil {
			return routeResponse{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return routeResponse{}, err
	}
	return p.DriverRoute(ctx, routeID, driverRef)
}
