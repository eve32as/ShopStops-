package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/eve32as/ShopStops-/internal/checkr"
	"github.com/jackc/pgx/v5/pgconn"
)

type DriverScreening struct {
	ID             string     `json:"id"`
	PackageID      string     `json:"packageId"`
	Status         string     `json:"status"`
	ProviderStatus string     `json:"providerStatus,omitempty"`
	ProviderResult string     `json:"providerResult,omitempty"`
	ReviewDecision string     `json:"reviewDecision"`
	RequestedAt    time.Time  `json:"requestedAt"`
	ReviewedBy     string     `json:"reviewedBy,omitempty"`
	ReviewedAt     *time.Time `json:"reviewedAt,omitempty"`
}

type DriverScreeningProvider interface {
	PackageID() string
	CreateCandidate(context.Context, checkr.Candidate) (string, error)
	CreateInvitation(context.Context, string, checkr.WorkLocation) (checkr.Invitation, error)
	GetInvitation(context.Context, string) (checkr.Invitation, error)
	GetReport(context.Context, string) (checkr.Report, error)
}

type DriverScreeningRepository interface {
	StartDriverScreening(
		context.Context, string, string, string, string,
	) (DriverScreening, bool, error)
	SetDriverScreeningCandidate(context.Context, string, string) error
	ClaimDriverScreeningInvitation(context.Context, string) (string, error)
	SetDriverScreeningInvitation(context.Context, string, string) error
	SetDriverScreeningFailure(context.Context, string, string) error
	LatestDriverScreening(context.Context, string) (DriverScreening, string, error)
	UpdateDriverScreeningProviderStatus(
		context.Context, string, string, string, string, string,
	) (DriverScreening, error)
	ReviewDriverScreening(
		context.Context, string, string, string, string,
	) (DriverScreening, error)
}

type screeningStartRequest struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
	Location  struct {
		City    string `json:"city"`
		Region  string `json:"region"`
		Country string `json:"country"`
	} `json:"workLocation"`
}

func (s *Server) startDriverScreening(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRole(w, r, "admin")
	if !ok {
		return
	}
	if s.screening == nil {
		writeError(w, http.StatusServiceUnavailable, "Checkr screening is not configured")
		return
	}
	driverRef := r.PathValue("driverRef")
	if !validDriverReference(driverRef) {
		writeError(w, http.StatusBadRequest, "invalid driver reference")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(idempotencyKey) {
		writeError(w, http.StatusBadRequest, "a valid Idempotency-Key header is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request screeningStartRequest
	if err := decodeScreeningJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid screening request")
		return
	}
	request.FirstName = strings.TrimSpace(request.FirstName)
	request.LastName = strings.TrimSpace(request.LastName)
	request.Email = strings.TrimSpace(request.Email)
	request.Location.City = strings.TrimSpace(request.Location.City)
	request.Location.Region = strings.ToUpper(strings.TrimSpace(request.Location.Region))
	request.Location.Country = strings.ToUpper(strings.TrimSpace(request.Location.Country))
	if err := validateScreeningRequest(request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	repository, ok := s.repo.(DriverScreeningRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver screening is unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	screening, created, err := repository.StartDriverScreening(
		ctx, driverRef, s.screening.PackageID(), idempotencyKey, actor,
	)
	if err != nil {
		s.writeDriverScreeningError(w, err)
		return
	}
	if created {
		candidateID, providerErr := s.screening.CreateCandidate(ctx, checkr.Candidate{
			FirstName: request.FirstName, LastName: request.LastName, Email: request.Email,
		})
		if providerErr != nil {
			status := "reconciliation_required"
			var apiErr *checkr.APIError
			if errors.As(providerErr, &apiErr) &&
				apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 &&
				apiErr.StatusCode != http.StatusConflict {
				status = "failed"
			}
			_ = repository.SetDriverScreeningFailure(ctx, screening.ID, status)
			if status == "failed" {
				writeError(w, http.StatusBadGateway, "Checkr rejected the candidate request")
			} else {
				writeError(w, http.StatusBadGateway, "Checkr candidate request could not be confirmed; reconcile it in Checkr before retrying")
			}
			return
		}
		if err := repository.SetDriverScreeningCandidate(ctx, screening.ID, candidateID); err != nil {
			writeError(w, http.StatusServiceUnavailable, "screening was created by Checkr but could not be recorded; reconcile it before retrying")
			return
		}
		screening.Status = "candidate_created"
	} else if screening.Status != "candidate_created" {
		writeJSON(w, http.StatusOK, screening)
		return
	}

	candidateID, err := repository.ClaimDriverScreeningInvitation(ctx, screening.ID)
	if err != nil {
		s.writeDriverScreeningError(w, err)
		return
	}
	invitation, providerErr := s.screening.CreateInvitation(ctx, candidateID, checkr.WorkLocation{
		City: request.Location.City, Region: request.Location.Region,
		Country: request.Location.Country,
	})
	if providerErr != nil {
		var apiErr *checkr.APIError
		if errors.As(providerErr, &apiErr) && apiErr.StatusCode >= 400 &&
			apiErr.StatusCode < 500 && apiErr.StatusCode != http.StatusConflict {
			_ = repository.SetDriverScreeningFailure(ctx, screening.ID, "candidate_created")
		} else {
			_ = repository.SetDriverScreeningFailure(ctx, screening.ID, "reconciliation_required")
		}
		writeError(w, http.StatusBadGateway, "Checkr invitation could not be confirmed; reconcile it before retrying")
		return
	}
	if err := repository.SetDriverScreeningInvitation(ctx, screening.ID, invitation.ID); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Checkr invitation was created but could not be recorded; reconcile it before retrying")
		return
	}
	screening.Status = "invited"
	w.Header().Set("Location", "/admin/drivers/"+driverRef+"/screenings")
	writeJSON(w, http.StatusCreated, screening)
}

func (s *Server) refreshDriverScreening(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	if s.screening == nil {
		writeError(w, http.StatusServiceUnavailable, "Checkr screening is not configured")
		return
	}
	driverRef := r.PathValue("driverRef")
	if !validDriverReference(driverRef) {
		writeError(w, http.StatusBadRequest, "invalid driver reference")
		return
	}
	repository, ok := s.repo.(DriverScreeningRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver screening is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	screening, invitationID, err := repository.LatestDriverScreening(ctx, driverRef)
	if err != nil {
		s.writeDriverScreeningError(w, err)
		return
	}
	if invitationID == "" {
		writeError(w, http.StatusConflict, "screening has no confirmed Checkr invitation to refresh")
		return
	}
	invitation, err := s.screening.GetInvitation(ctx, invitationID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not refresh Checkr invitation status")
		return
	}
	status, providerStatus, providerResult := "invited", safeProviderCode(invitation.Status), ""
	reportID := invitation.ReportID
	if reportID != "" {
		report, reportErr := s.screening.GetReport(ctx, reportID)
		if reportErr != nil {
			writeError(w, http.StatusBadGateway, "could not refresh Checkr report status")
			return
		}
		providerStatus = safeProviderCode(report.Status)
		providerResult = safeProviderCode(report.Result)
		if providerStatus == "complete" || providerStatus == "completed" {
			status = "complete"
		} else {
			status = "in_progress"
		}
	}
	updated, err := repository.UpdateDriverScreeningProviderStatus(
		ctx, screening.ID, reportID, status, providerStatus, providerResult,
	)
	if err != nil {
		s.writeDriverScreeningError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) reviewDriverScreening(w http.ResponseWriter, r *http.Request) {
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
		ScreeningID string `json:"screeningId"`
		Decision    string `json:"decision"`
	}
	if err := decodeScreeningJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid screening review")
		return
	}
	if !isUUID(request.ScreeningID) ||
		(request.Decision != "approved" && request.Decision != "denied") {
		writeError(w, http.StatusBadRequest, "screeningId must be a UUID and decision must be approved or denied")
		return
	}
	repository, ok := s.repo.(DriverScreeningRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "driver screening is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	screening, err := repository.ReviewDriverScreening(
		ctx, driverRef, request.ScreeningID, request.Decision, actor,
	)
	if err != nil {
		s.writeDriverScreeningError(w, err)
		return
	}
	if screening.Status != "complete" || screening.ReviewDecision != request.Decision {
		writeError(w, http.StatusConflict, "only the latest completed screening can be reviewed")
		return
	}
	writeJSON(w, http.StatusOK, screening)
}

func (s *Server) writeDriverScreeningError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "driver or screening not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "screening conflicts with current state")
	default:
		s.databaseError(w, err)
	}
}

func decodeScreeningJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func validateScreeningRequest(request screeningStartRequest) error {
	if request.FirstName == "" || len(request.FirstName) > 100 ||
		request.LastName == "" || len(request.LastName) > 100 {
		return errors.New("first and last name must each be between 1 and 100 characters")
	}
	address, err := mail.ParseAddress(request.Email)
	if err != nil || address.Address != request.Email || len(request.Email) > 254 {
		return errors.New("a valid candidate email address is required")
	}
	if request.Location.City == "" || len(request.Location.City) > 100 ||
		len(request.Location.Region) < 2 || len(request.Location.Region) > 3 ||
		len(request.Location.Country) != 2 {
		return errors.New("workLocation requires a city, region code, and two-letter country code")
	}
	for _, value := range []string{
		request.FirstName, request.LastName, request.Email,
		request.Location.City, request.Location.Region, request.Location.Country,
	} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("screening fields contain invalid characters")
		}
	}
	return nil
}

func safeProviderCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 64 {
		return ""
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') &&
			!(character >= '0' && character <= '9') &&
			character != '_' && character != '-' {
			return ""
		}
	}
	return value
}

func (p *PostgresRepository) StartDriverScreening(
	ctx context.Context, driverRef, packageID, idempotencyKey, actor string,
) (DriverScreening, bool, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return DriverScreening{}, false, err
	}
	defer tx.Rollback()

	var profileID, profileStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, status
		FROM driver_profiles
		WHERE driver_ref = $1
		FOR UPDATE`, driverRef).Scan(&profileID, &profileStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return DriverScreening{}, false, ErrNotFound
	}
	if err != nil {
		return DriverScreening{}, false, err
	}
	existing, err := scanDriverScreening(tx.QueryRowContext(ctx, `
		SELECT id::text, package_id, status, provider_status, provider_result,
		       review_decision, requested_by, reviewed_by, created_at, reviewed_at
		FROM driver_screenings
		WHERE driver_ref = $1 AND idempotency_key = $2`, driverRef, idempotencyKey))
	if err == nil {
		if err := tx.Commit(); err != nil {
			return DriverScreening{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DriverScreening{}, false, err
	}
	screening, err := scanDriverScreening(tx.QueryRowContext(ctx, `
		INSERT INTO driver_screenings (driver_ref, idempotency_key, package_id, requested_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, package_id, status, provider_status, provider_result,
		          review_decision, requested_by, reviewed_by, created_at, reviewed_at`,
		driverRef, idempotencyKey, packageID, actor))
	if err != nil {
		return DriverScreening{}, false, err
	}
	if profileStatus == "active" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE driver_profiles
			SET status = 'pending', verified_by = NULL, verified_at = NULL,
			    updated_by = $2, updated_at = now()
			WHERE driver_ref = $1`, driverRef, actor); err != nil {
			return DriverScreening{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
			VALUES ('driver_profile', $1::uuid, 'driver.screening.required',
			        jsonb_build_object('driverRef', $2, 'actor', $3))`,
			profileID, driverRef, actor); err != nil {
			return DriverScreening{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return DriverScreening{}, false, err
	}
	return screening, true, nil
}

func scanDriverScreening(row interface{ Scan(...any) error }) (DriverScreening, error) {
	var screening DriverScreening
	var providerStatus, providerResult, requestedBy, reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	err := row.Scan(
		&screening.ID, &screening.PackageID, &screening.Status, &providerStatus,
		&providerResult, &screening.ReviewDecision, &requestedBy, &reviewedBy,
		&screening.RequestedAt, &reviewedAt,
	)
	if err != nil {
		return DriverScreening{}, err
	}
	if providerStatus.Valid {
		screening.ProviderStatus = providerStatus.String
	}
	if providerResult.Valid {
		screening.ProviderResult = providerResult.String
	}
	if reviewedBy.Valid {
		screening.ReviewedBy = reviewedBy.String
	}
	if reviewedAt.Valid {
		screening.ReviewedAt = &reviewedAt.Time
	}
	return screening, nil
}

func (p *PostgresRepository) SetDriverScreeningCandidate(
	ctx context.Context, screeningID, candidateID string,
) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE driver_screenings
		SET candidate_id = $2, status = 'candidate_created', updated_at = now()
		WHERE id = $1::uuid AND status = 'requested'`, screeningID, candidateID)
	return requireOneScreeningRow(result, err)
}

func (p *PostgresRepository) ClaimDriverScreeningInvitation(
	ctx context.Context, screeningID string,
) (string, error) {
	var candidateID string
	err := p.db.QueryRowContext(ctx, `
		UPDATE driver_screenings
		SET status = 'invitation_submitting', updated_at = now()
		WHERE id = $1::uuid AND status = 'candidate_created'
		RETURNING candidate_id`, screeningID).Scan(&candidateID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrConflict
	}
	return candidateID, err
}

func (p *PostgresRepository) SetDriverScreeningInvitation(
	ctx context.Context, screeningID, invitationID string,
) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE driver_screenings
		SET invitation_id = $2, status = 'invited', updated_at = now()
		WHERE id = $1::uuid AND status = 'invitation_submitting'`, screeningID, invitationID)
	return requireOneScreeningRow(result, err)
}

func (p *PostgresRepository) SetDriverScreeningFailure(
	ctx context.Context, screeningID, status string,
) error {
	if status != "candidate_created" && status != "failed" &&
		status != "reconciliation_required" {
		return ErrConflict
	}
	result, err := p.db.ExecContext(ctx, `
		UPDATE driver_screenings
		SET status = $2, updated_at = now()
		WHERE id = $1::uuid
		  AND status IN ('requested', 'candidate_created', 'invitation_submitting')`,
		screeningID, status)
	return requireOneScreeningRow(result, err)
}

func (p *PostgresRepository) LatestDriverScreening(
	ctx context.Context, driverRef string,
) (DriverScreening, string, error) {
	var invitationID sql.NullString
	screening, err := scanDriverScreening(p.db.QueryRowContext(ctx, `
		SELECT id::text, package_id, status, provider_status, provider_result,
		       review_decision, requested_by, reviewed_by, created_at, reviewed_at
		FROM driver_screenings
		WHERE driver_ref = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, driverRef))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DriverScreening{}, "", ErrNotFound
		}
		return DriverScreening{}, "", err
	}
	err = p.db.QueryRowContext(ctx, `
		SELECT invitation_id
		FROM driver_screenings
		WHERE id = $1::uuid`, screening.ID).Scan(&invitationID)
	if err != nil {
		return DriverScreening{}, "", err
	}
	return screening, invitationID.String, nil
}

func (p *PostgresRepository) UpdateDriverScreeningProviderStatus(
	ctx context.Context, screeningID, reportID, status, providerStatus, providerResult string,
) (DriverScreening, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return DriverScreening{}, err
	}
	defer tx.Rollback()
	current, err := scanDriverScreening(tx.QueryRowContext(ctx, `
		SELECT id::text, package_id, status, provider_status, provider_result,
		       review_decision, requested_by, reviewed_by, created_at, reviewed_at
		FROM driver_screenings WHERE id = $1::uuid FOR UPDATE`, screeningID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DriverScreening{}, ErrNotFound
		}
		return DriverScreening{}, err
	}
	var driverRef, profileID string
	if err := tx.QueryRowContext(ctx, `
		SELECT profile.driver_ref, profile.id::text
		FROM driver_profiles profile
		JOIN driver_screenings screening ON screening.driver_ref = profile.driver_ref
		WHERE screening.id = $1::uuid
		FOR UPDATE OF profile`, screeningID).Scan(&driverRef, &profileID); err != nil {
		return DriverScreening{}, err
	}
	changed := current.Status != status ||
		current.ProviderStatus != providerStatus || current.ProviderResult != providerResult
	if changed {
		_, err = tx.ExecContext(ctx, `
			UPDATE driver_screenings
			SET report_id = NULLIF($2, '')::text,
			    status = $3, provider_status = NULLIF($4, ''),
			    provider_result = NULLIF($5, ''),
			    review_decision = 'pending', reviewed_by = NULL, reviewed_at = NULL,
			    updated_at = now()
			WHERE id = $1::uuid`, screeningID, reportID, status, providerStatus, providerResult)
		if err != nil {
			return DriverScreening{}, err
		}
		var profileStatus string
		if err := tx.QueryRowContext(ctx, `
			SELECT status FROM driver_profiles WHERE driver_ref = $1`, driverRef,
		).Scan(&profileStatus); err != nil {
			return DriverScreening{}, err
		}
		if profileStatus == "active" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE driver_profiles
				SET status = 'pending', verified_by = NULL, verified_at = NULL,
				    updated_by = 'system:checkr', updated_at = now()
				WHERE driver_ref = $1`, driverRef); err != nil {
				return DriverScreening{}, err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
				VALUES ('driver_profile', $1::uuid, 'driver.screening.changed',
				        jsonb_build_object('driverRef', $2, 'actor', 'system:checkr'))`,
				profileID, driverRef); err != nil {
				return DriverScreening{}, err
			}
		}
	}
	updated, err := scanDriverScreening(tx.QueryRowContext(ctx, `
		SELECT id::text, package_id, status, provider_status, provider_result,
		       review_decision, requested_by, reviewed_by, created_at, reviewed_at
		FROM driver_screenings WHERE id = $1::uuid`, screeningID))
	if err != nil {
		return DriverScreening{}, err
	}
	if err := tx.Commit(); err != nil {
		return DriverScreening{}, err
	}
	return updated, nil
}

func (p *PostgresRepository) ReviewDriverScreening(
	ctx context.Context, driverRef, screeningID, decision, actor string,
) (DriverScreening, error) {
	var screening DriverScreening
	var reviewedBy sql.NullString
	var reviewedAt sql.NullTime
	err := p.db.QueryRowContext(ctx, `
		SELECT screening_id::text, status, provider_result, review_decision,
		       reviewed_by, reviewed_at
		FROM record_driver_screening_review($1::uuid, $2, $3, $4)`,
		screeningID, driverRef, decision, actor).Scan(
		&screening.ID, &screening.Status, &screening.ProviderResult,
		&screening.ReviewDecision, &reviewedBy, &reviewedAt,
	)
	if err == nil {
		if reviewedBy.Valid {
			screening.ReviewedBy = reviewedBy.String
		}
		if reviewedAt.Valid {
			screening.ReviewedAt = &reviewedAt.Time
		}
		return screening, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "P0001":
			return DriverScreening{}, ErrConflict
		case "P0002":
			return DriverScreening{}, ErrNotFound
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return DriverScreening{}, ErrNotFound
	}
	return DriverScreening{}, err
}

func requireOneScreeningRow(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrConflict
	}
	return nil
}
