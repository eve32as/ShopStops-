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

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/eve32as/ShopStops-/internal/routing"
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
		writeError(w, http.StatusConflict, "checkout is not eligible or already has a conflicting route assignment")
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
		WHERE driver_ref = $1 AND status = 'in_progress'
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

	var storedCheckoutID string
	err = tx.QueryRowContext(ctx, `
		SELECT id::text FROM unified_checkouts WHERE id = $1 FOR UPDATE`,
		checkoutID,
	).Scan(&storedCheckoutID)
	if errors.Is(err, sql.ErrNoRows) {
		return routeResponse{}, ErrNotFound
	}
	if err != nil {
		return routeResponse{}, err
	}

	var routeID, storedIdempotencyKey, assignedDriver, status string
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, idempotency_key, COALESCE(driver_ref, ''), status
		FROM unified_delivery_routes WHERE checkout_id = $1 FOR UPDATE`,
		checkoutID,
	).Scan(&routeID, &storedIdempotencyKey, &assignedDriver, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx,
			`SELECT create_unified_route($1, $2, 'osrm')::text`,
			checkoutID, idempotencyKey,
		).Scan(&routeID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) &&
				(pgErr.Code == "P0001" || pgErr.Code == "23505") {
				return routeResponse{}, ErrConflict
			}
			return routeResponse{}, err
		}
	} else if err != nil {
		return routeResponse{}, err
	} else if storedIdempotencyKey != idempotencyKey {
		if !strings.HasPrefix(storedIdempotencyKey, "stripe-checkout-session-") ||
			assignedDriver != "" {
			return routeResponse{}, ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE unified_delivery_routes SET idempotency_key = $2
			WHERE id = $1`, routeID, idempotencyKey); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return routeResponse{}, ErrConflict
			}
			return routeResponse{}, err
		}
	}

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
		status == "cancelled" ||
		(status == "completed" && assignedDriver != driverRef) {
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
	if p.router != nil {
		var legCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM unified_route_legs WHERE route_id = $1`,
			routeID,
		).Scan(&legCount); err != nil {
			return routeResponse{}, err
		}
		if legCount == 0 {
			stops, err := tx.QueryContext(ctx, `
				SELECT ST_Y(location::geometry), ST_X(location::geometry)
				FROM unified_route_stops
				WHERE route_id = $1
				ORDER BY sequence`, routeID)
			if err != nil {
				return routeResponse{}, err
			}
			coordinates := make([]routing.Coordinate, 0, 3)
			for stops.Next() {
				var coordinate routing.Coordinate
				if err := stops.Scan(&coordinate.Latitude, &coordinate.Longitude); err != nil {
					stops.Close()
					return routeResponse{}, err
				}
				coordinates = append(coordinates, coordinate)
			}
			if err := stops.Err(); err != nil {
				stops.Close()
				return routeResponse{}, err
			}
			if err := stops.Close(); err != nil {
				return routeResponse{}, err
			}
			legs, err := p.router.Route(ctx, coordinates)
			if err != nil {
				return routeResponse{}, err
			}
			if len(legs) != len(coordinates)-1 {
				return routeResponse{}, errors.New("routing provider returned an unexpected leg count")
			}
			for index, leg := range legs {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO unified_route_legs (
						route_id, from_sequence, to_sequence,
						distance_meters, duration_seconds, provider
					) VALUES ($1, $2, $3, $4, $5, 'osrm')
					ON CONFLICT (route_id, from_sequence) DO NOTHING`,
					routeID, index+1, index+2, leg.DistanceMeters, leg.DurationSeconds,
				); err != nil {
					return routeResponse{}, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return routeResponse{}, err
	}
	return p.DriverRoute(ctx, routeID, driverRef)
}
