package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type RouteOptimization struct {
	ID                      string     `json:"id"`
	RouteID                 string     `json:"routeId"`
	Status                  string     `json:"status"`
	RequestedBy             string     `json:"requestedBy"`
	RequestedAt             time.Time  `json:"requestedAt"`
	ErrorCode               string     `json:"errorCode,omitempty"`
	BaselineDistanceMeters  int        `json:"baselineDistanceMeters"`
	ProposedDistanceMeters  *int       `json:"proposedDistanceMeters,omitempty"`
	BaselineDurationSeconds int        `json:"baselineDurationSeconds"`
	ProposedDurationSeconds *int       `json:"proposedDurationSeconds,omitempty"`
	EstimatedSavingsCents   *int       `json:"estimatedSavingsCents,omitempty"`
	ProposedStopIDs         []string   `json:"proposedStopIds,omitempty"`
	ApprovedBy              string     `json:"approvedBy,omitempty"`
	ApprovedAt              *time.Time `json:"approvedAt,omitempty"`
	AppliedBy               string     `json:"appliedBy,omitempty"`
	AppliedAt               *time.Time `json:"appliedAt,omitempty"`
}

type RouteOptimizationMetrics struct {
	CompletedOptimizations   int64 `json:"completedOptimizations"`
	ProjectedDistanceMeters  int64 `json:"projectedDistanceMeters"`
	ActualDistanceMeters     int64 `json:"actualDistanceMeters"`
	ProjectedDurationSeconds int64 `json:"projectedDurationSeconds"`
	ActualDurationSeconds    int64 `json:"actualDurationSeconds"`
	EstimatedSavingsCents    int64 `json:"estimatedSavingsCents"`
}

func (s *Server) requestRouteOptimization(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRoles(w, r, "dispatch", "admin")
	if !ok {
		return
	}
	routeID := r.PathValue("routeId")
	if !isUUID(routeID) {
		writeError(w, http.StatusBadRequest, "valid route ID is required")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(idempotencyKey) {
		writeError(w, http.StatusBadRequest, "a valid Idempotency-Key header is required")
		return
	}
	repository, ok := s.repo.(RouteOptimizationRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "route optimization is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	job, err := repository.RequestRouteOptimization(ctx, routeID, idempotencyKey, actor)
	if err != nil {
		s.writeRouteOptimizationError(w, err)
		return
	}
	w.Header().Set("Location", "/dispatch/routes/"+routeID+
		"/optimizations/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) getRouteOptimization(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRoles(w, r, "dispatch", "admin"); !ok {
		return
	}
	routeID, jobID := r.PathValue("routeId"), r.PathValue("optimizationId")
	if !isUUID(routeID) || !isUUID(jobID) {
		writeError(w, http.StatusBadRequest, "valid route and optimization IDs are required")
		return
	}
	repository, ok := s.repo.(RouteOptimizationRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "route optimization is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	job, err := repository.GetRouteOptimization(ctx, routeID, jobID)
	if err != nil {
		s.writeRouteOptimizationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) approveRouteOptimization(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRoles(w, r, "dispatch", "admin")
	if !ok {
		return
	}
	routeID, jobID := r.PathValue("routeId"), r.PathValue("optimizationId")
	if !isUUID(routeID) || !isUUID(jobID) {
		writeError(w, http.StatusBadRequest, "valid route and optimization IDs are required")
		return
	}
	repository, ok := s.repo.(RouteOptimizationRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "route optimization is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	job, err := repository.ApproveRouteOptimization(ctx, routeID, jobID, actor)
	if err != nil {
		s.writeRouteOptimizationError(w, err)
		return
	}
	if job.Status == "stale" {
		writeError(w, http.StatusConflict, "route changed or was assigned; optimization preview is stale")
		return
	}
	if job.Status != "applied" {
		writeError(w, http.StatusConflict, "only an improved preview can be approved")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) getRouteOptimizationMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRoles(w, r, "dispatch", "admin"); !ok {
		return
	}
	repository, ok := s.repo.(RouteOptimizationRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "route optimization is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	metrics, err := repository.RouteOptimizationMetrics(ctx)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) writeRouteOptimizationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "route optimization not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "route is not eligible or optimization conflicts with current state")
	default:
		s.databaseError(w, err)
	}
}

func (p *PostgresRepository) RequestRouteOptimization(
	ctx context.Context, routeID, key, actor string,
) (RouteOptimization, error) {
	var jobID string
	if err := p.db.QueryRowContext(ctx,
		`SELECT request_route_optimization($1, $2, $3)`,
		routeID, key, actor,
	).Scan(&jobID); err != nil {
		return RouteOptimization{}, routeOptimizationDatabaseError(err)
	}
	return p.GetRouteOptimization(ctx, routeID, jobID)
}

func (p *PostgresRepository) GetRouteOptimization(
	ctx context.Context, routeID, jobID string,
) (RouteOptimization, error) {
	var job RouteOptimization
	var errorCode, proposedIDs sql.NullString
	var proposedDistance, proposedDuration, savings sql.NullInt64
	var approvedBy, appliedBy sql.NullString
	var approvedAt, appliedAt sql.NullTime
	err := p.db.QueryRowContext(ctx, `
		SELECT id::text, route_id::text, status, requested_by, created_at,
		       error_code, baseline_distance_meters, proposed_distance_meters,
		       baseline_duration_seconds, proposed_duration_seconds,
		       estimated_savings_cents,
		       array_to_json(proposed_stop_ids)::text,
		       approved_by, approved_at, applied_by, applied_at
		FROM route_optimization_jobs
		WHERE route_id = $1 AND id = $2`,
		routeID, jobID,
	).Scan(
		&job.ID, &job.RouteID, &job.Status, &job.RequestedBy, &job.RequestedAt,
		&errorCode, &job.BaselineDistanceMeters, &proposedDistance,
		&job.BaselineDurationSeconds, &proposedDuration, &savings,
		&proposedIDs, &approvedBy, &approvedAt, &appliedBy, &appliedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteOptimization{}, ErrNotFound
	}
	if err != nil {
		return RouteOptimization{}, err
	}
	if errorCode.Valid {
		job.ErrorCode = errorCode.String
	}
	if proposedDistance.Valid {
		value := int(proposedDistance.Int64)
		job.ProposedDistanceMeters = &value
	}
	if proposedDuration.Valid {
		value := int(proposedDuration.Int64)
		job.ProposedDurationSeconds = &value
	}
	if savings.Valid {
		value := int(savings.Int64)
		job.EstimatedSavingsCents = &value
	}
	if proposedIDs.Valid && proposedIDs.String != "" {
		if err := json.Unmarshal([]byte(proposedIDs.String), &job.ProposedStopIDs); err != nil {
			return RouteOptimization{}, err
		}
	}
	if approvedBy.Valid {
		job.ApprovedBy = approvedBy.String
	}
	if approvedAt.Valid {
		job.ApprovedAt = &approvedAt.Time
	}
	if appliedBy.Valid {
		job.AppliedBy = appliedBy.String
	}
	if appliedAt.Valid {
		job.AppliedAt = &appliedAt.Time
	}
	return job, nil
}

func (p *PostgresRepository) ApproveRouteOptimization(
	ctx context.Context, routeID, jobID, actor string,
) (RouteOptimization, error) {
	var status string
	err := p.db.QueryRowContext(ctx,
		`SELECT apply_route_optimization($1, $2, $3)`,
		routeID, jobID, actor,
	).Scan(&status)
	if err != nil {
		return RouteOptimization{}, routeOptimizationDatabaseError(err)
	}
	return p.GetRouteOptimization(ctx, routeID, jobID)
}

func (p *PostgresRepository) RouteOptimizationMetrics(
	ctx context.Context,
) (RouteOptimizationMetrics, error) {
	var metrics RouteOptimizationMetrics
	err := p.db.QueryRowContext(ctx, `
		SELECT count(*),
		       COALESCE(sum(projected_distance_meters), 0),
		       COALESCE(sum(actual_distance_meters), 0),
		       COALESCE(sum(projected_duration_seconds), 0),
		       COALESCE(sum(actual_duration_seconds), 0),
		       COALESCE(sum(estimated_savings_cents), 0)
		FROM route_optimization_actual_metrics`,
	).Scan(
		&metrics.CompletedOptimizations,
		&metrics.ProjectedDistanceMeters, &metrics.ActualDistanceMeters,
		&metrics.ProjectedDurationSeconds, &metrics.ActualDurationSeconds,
		&metrics.EstimatedSavingsCents,
	)
	return metrics, err
}

func routeOptimizationDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "P0002":
			return ErrNotFound
		case "P0001", "23505":
			return ErrConflict
		}
	}
	return err
}
