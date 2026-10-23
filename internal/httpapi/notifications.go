package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type NotificationRepository interface {
	ListDeliveryMessageLogs(context.Context, string, int) ([]DeliveryMessageLog, error)
	RetryDeliveryMessage(context.Context, string) error
	UpsertDriverPayoutProfile(context.Context, string, string, string) error
}

type DeliveryMessageLog struct {
	ID                string                   `json:"id"`
	RecipientRef      string                   `json:"recipientRef"`
	NotificationType  string                   `json:"notificationType"`
	Channel           string                   `json:"channel"`
	Status            string                   `json:"status"`
	Attempts          int                      `json:"attempts"`
	LastError         string                   `json:"lastError,omitempty"`
	ProviderMessageID string                   `json:"providerMessageId,omitempty"`
	CreatedAt         time.Time                `json:"createdAt"`
	SentAt            *time.Time               `json:"sentAt,omitempty"`
	AttemptHistory    []DeliveryMessageAttempt `json:"attemptHistory"`
}

type DeliveryMessageAttempt struct {
	Status            string     `json:"status"`
	ErrorCode         string     `json:"errorCode,omitempty"`
	ProviderMessageID string     `json:"providerMessageId,omitempty"`
	StartedAt         time.Time  `json:"startedAt"`
	CompletedAt       *time.Time `json:"completedAt,omitempty"`
}

func (s *Server) listDeliveryMessageLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "queued", "sending", "sent", "failed":
	default:
		writeError(w, http.StatusBadRequest, "status must be queued, sending, sent, or failed")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = value
	}
	repository, ok := s.repo.(NotificationRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "notification management is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	logs, err := repository.ListDeliveryMessageLogs(ctx, status, limit)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) retryDeliveryMessage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizedActorRole(w, r, "admin"); !ok {
		return
	}
	messageID := r.PathValue("notificationId")
	if !isUUID(messageID) {
		writeError(w, http.StatusBadRequest, "invalid notification ID")
		return
	}
	repository, ok := s.repo.(NotificationRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "notification management is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	err := repository.RetryDeliveryMessage(ctx, messageID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "notification not found")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "only failed notifications can be retried")
	case err != nil:
		s.databaseError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{
			"id": messageID, "status": "queued",
		})
	}
}

func (s *Server) upsertDriverPayoutProfile(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorizedActorRole(w, r, "admin")
	if !ok {
		return
	}
	driverRef := strings.TrimSpace(r.PathValue("driverRef"))
	if !validDriverReference(driverRef) {
		writeError(w, http.StatusBadRequest, "invalid driver reference")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var request struct {
		Email string `json:"email"`
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
	email := strings.TrimSpace(request.Email)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 320 ||
		strings.ContainsAny(email, "\r\n") {
		writeError(w, http.StatusBadRequest, "enter one valid email address")
		return
	}
	repository, ok := s.repo.(NotificationRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "notification management is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := repository.UpsertDriverPayoutProfile(ctx, driverRef, email, actor); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email address is already assigned to another driver")
			return
		}
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"driverRef": driverRef, "status": "enabled",
	})
}

func validDriverReference(reference string) bool {
	if len(reference) == 0 || len(reference) > 200 {
		return false
	}
	for _, char := range reference {
		if char < 0x21 || char > 0x7e || char == '/' || char == '\\' {
			return false
		}
	}
	return true
}

func (p *PostgresRepository) ListDeliveryMessageLogs(
	ctx context.Context, status string, limit int,
) ([]DeliveryMessageLog, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT message.id::text, message.recipient_ref, message.notification_type,
		       message.channel, message.status, message.attempts,
		       COALESCE(message.last_error, ''), COALESCE(message.provider_message_id, ''),
		       message.created_at, message.sent_at,
		       COALESCE(jsonb_agg(jsonb_build_object(
		           'status', attempt.status,
		           'errorCode', attempt.error_code,
		           'providerMessageId', attempt.provider_message_id,
		           'startedAt', attempt.started_at,
		           'completedAt', attempt.completed_at
		       ) ORDER BY attempt.started_at, attempt.id)
		           FILTER (WHERE attempt.id IS NOT NULL), '[]'::jsonb)::text
		FROM delivery_message_logs message
		LEFT JOIN delivery_message_attempts attempt ON attempt.message_id = message.id
		WHERE ($1 = '' OR message.status = $1)
		GROUP BY message.id
		ORDER BY message.created_at DESC, message.id DESC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := make([]DeliveryMessageLog, 0)
	for rows.Next() {
		var item DeliveryMessageLog
		var sentAt sql.NullTime
		var history []byte
		if err := rows.Scan(
			&item.ID, &item.RecipientRef, &item.NotificationType, &item.Channel,
			&item.Status, &item.Attempts, &item.LastError, &item.ProviderMessageID,
			&item.CreatedAt, &sentAt, &history,
		); err != nil {
			return nil, err
		}
		if sentAt.Valid {
			item.SentAt = &sentAt.Time
		}
		if err := json.Unmarshal(history, &item.AttemptHistory); err != nil {
			return nil, err
		}
		if item.AttemptHistory == nil {
			item.AttemptHistory = []DeliveryMessageAttempt{}
		}
		logs = append(logs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

func (p *PostgresRepository) RetryDeliveryMessage(
	ctx context.Context, messageID string,
) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE delivery_message_logs
		SET status = 'queued', attempts = 0, available_at = now(),
		    locked_until = NULL, last_error = NULL, provider_message_id = NULL,
		    updated_at = now()
		WHERE id = $1 AND status = 'failed'`, messageID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM delivery_message_logs WHERE id = $1)`,
		messageID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (p *PostgresRepository) UpsertDriverPayoutProfile(
	ctx context.Context, driverRef, email, actor string,
) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO driver_payout_profiles (driver_ref, email_address, updated_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (driver_ref) DO UPDATE
		SET email_address = EXCLUDED.email_address, enabled = true,
		    updated_by = EXCLUDED.updated_by, updated_at = now()`,
		driverRef, email, actor,
	)
	return err
}
