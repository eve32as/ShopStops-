package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type DriverProfile struct {
	DriverRef   string     `json:"driverRef"`
	DisplayName string     `json:"displayName"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	VerifiedAt  *time.Time `json:"verifiedAt,omitempty"`
}

func (s *Server) listDriverProfiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "active" && status != "suspended" {
		writeError(w, http.StatusBadRequest, "status must be pending, active, or suspended")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = value
	}
	repository, ok := s.repo.(DriverDirectoryRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver directory is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	profiles, err := repository.ListDriverProfiles(ctx, status, limit)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (s *Server) setDriverProfile(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRole(w, r, "admin")
	if !ok {
		return
	}
	driverRef := r.PathValue("driverRef")
	if !validDriverReference(driverRef) {
		writeError(w, http.StatusBadRequest, "invalid driver reference")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request struct {
		DisplayName string `json:"displayName"`
		Status      string `json:"status"`
	}
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
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	if len(request.DisplayName) == 0 || len(request.DisplayName) > 120 {
		writeError(w, http.StatusBadRequest, "display name must be between 1 and 120 characters")
		return
	}
	if request.Status != "pending" && request.Status != "active" && request.Status != "suspended" {
		writeError(w, http.StatusBadRequest, "status must be pending, active, or suspended")
		return
	}
	repository, ok := s.repo.(DriverDirectoryRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver directory is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	profile, err := repository.SetDriverProfile(
		ctx, driverRef, request.DisplayName, request.Status, actor,
	)
	switch {
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "driver profile conflicts with current state")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, profile)
	}
}

func (p *PostgresRepository) ListDriverProfiles(
	ctx context.Context, status string, limit int,
) ([]DriverProfile, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT driver_ref, display_name, status, created_at, updated_at, verified_at
		FROM driver_profiles
		WHERE $1 = '' OR status = $1
		ORDER BY CASE status WHEN 'pending' THEN 0 WHEN 'active' THEN 1 ELSE 2 END,
		         updated_at DESC, driver_ref
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := make([]DriverProfile, 0)
	for rows.Next() {
		var profile DriverProfile
		if err := rows.Scan(
			&profile.DriverRef, &profile.DisplayName, &profile.Status,
			&profile.CreatedAt, &profile.UpdatedAt, &profile.VerifiedAt,
		); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return profiles, nil
}

func (p *PostgresRepository) SetDriverProfile(
	ctx context.Context, driverRef, displayName, status, actor string,
) (DriverProfile, error) {
	var profile DriverProfile
	err := p.db.QueryRowContext(ctx, `
		SELECT driver_ref, display_name, status, created_at, updated_at, verified_at
		FROM set_driver_profile($1, $2, $3, $4)`,
		driverRef, displayName, status, actor,
	).Scan(
		&profile.DriverRef, &profile.DisplayName, &profile.Status,
		&profile.CreatedAt, &profile.UpdatedAt, &profile.VerifiedAt,
	)
	if err == nil {
		return profile, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
		return DriverProfile{}, ErrConflict
	}
	if errors.Is(err, sql.ErrNoRows) {
		return DriverProfile{}, ErrNotFound
	}
	return DriverProfile{}, err
}
