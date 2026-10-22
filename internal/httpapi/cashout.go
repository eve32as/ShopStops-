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
)

type DriverEarning struct {
	RouteID               string    `json:"routeId"`
	AmountCents           int64     `json:"amountCents"`
	BasePayoutCents       int       `json:"basePayoutCents"`
	AlongRouteBonusCents  int       `json:"alongRouteBonusCents"`
	AlongRoutePickupCount int       `json:"alongRoutePickupCount"`
	PickupCount           int       `json:"pickupCount"`
	Currency              string    `json:"currency"`
	Status                string    `json:"status"`
	CreatedAt             time.Time `json:"createdAt"`
}

type DriverCashout struct {
	ID          string    `json:"id"`
	AmountCents int64     `json:"amountCents"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	payoutID    string
	attempt     int
}

type DriverCashoutSummary struct {
	ConnectAccount *DriverConnectStatus `json:"connectAccount,omitempty"`
	AvailableCents int64                `json:"availableCents"`
	PendingCents   int64                `json:"pendingCents"`
	Earnings       []DriverEarning      `json:"earnings"`
	Cashouts       []DriverCashout      `json:"cashouts"`
}

type driverCashoutRecord struct {
	ID              string
	DriverRef       string
	StripeAccountID string
	AmountCents     int64
	TransferID      string
	PayoutID        string
	Attempt         int
	Status          string
}

type DriverCashoutRepository interface {
	DriverConnectAccount(context.Context, string) (DriverConnectStatus, bool, error)
	SaveDriverConnectAccount(context.Context, string, string) error
	UpdateDriverConnectStatus(context.Context, DriverConnectStatus) error
	DriverCashoutSummary(context.Context, string) (DriverCashoutSummary, error)
	ReserveDriverCashout(context.Context, string, string, string) (driverCashoutRecord, error)
	RecordDriverCashoutTransfer(context.Context, string, string) error
	RecordDriverCashoutPayout(context.Context, string, int, string, string) error
	ApplyDriverPayoutEvent(context.Context, string, string, string) error
}

type DriverCashoutRetryRepository interface {
	PrepareDriverCashoutRetry(context.Context, string, string, string) (driverCashoutRecord, error)
}

type connectOnboardingRequest struct {
	Country string `json:"country"`
}

func (s *Server) startDriverConnectOnboarding(w http.ResponseWriter, r *http.Request) {
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	repository, repoOK := s.repo.(DriverCashoutRepository)
	gateway, gatewayOK := s.payments.(DriverPayoutGateway)
	if !repoOK || !gatewayOK {
		writeError(w, http.StatusServiceUnavailable, "driver payouts are unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024)
	var request connectOnboardingRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "country is required")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return
	}
	request.Country = strings.ToUpper(strings.TrimSpace(request.Country))
	if len(request.Country) != 2 || request.Country[0] < 'A' || request.Country[0] > 'Z' ||
		request.Country[1] < 'A' || request.Country[1] > 'Z' {
		writeError(w, http.StatusBadRequest, "country must be a two-letter ISO code")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	account, exists, err := repository.DriverConnectAccount(ctx, driverRef)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if !exists {
		accountID, err := gateway.CreateDriverConnectAccount(
			ctx, request.Country, driverAccountIdempotencyKey(driverRef),
		)
		if err != nil {
			s.log.Error("Stripe driver account creation failed")
			writeError(w, http.StatusBadGateway, "Connect onboarding could not be started")
			return
		}
		if err := repository.SaveDriverConnectAccount(ctx, driverRef, accountID); err != nil {
			s.databaseError(w, err)
			return
		}
		account.AccountID = accountID
	}
	link, err := gateway.CreateDriverConnectLink(
		ctx, account.AccountID,
	)
	if err != nil {
		s.log.Error("Stripe driver onboarding link creation failed")
		writeError(w, http.StatusBadGateway, "Connect onboarding could not be started")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"url": link, "status": "onboarding_required",
	})
}

func (s *Server) getDriverCashoutSummary(w http.ResponseWriter, r *http.Request) {
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	repository, repoOK := s.repo.(DriverCashoutRepository)
	if !repoOK {
		writeError(w, http.StatusServiceUnavailable, "driver earnings are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	summary, err := repository.DriverCashoutSummary(ctx, driverRef)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if summary.ConnectAccount != nil {
		if gateway, ok := s.payments.(DriverPayoutGateway); ok {
			status, err := gateway.RetrieveDriverConnectStatus(
				ctx, summary.ConnectAccount.AccountID,
			)
			if err != nil {
				s.log.Error("Stripe driver account status could not be refreshed")
				writeError(w, http.StatusServiceUnavailable, "Connect status could not be refreshed")
				return
			}
			if err := repository.UpdateDriverConnectStatus(ctx, status); err != nil {
				s.databaseError(w, err)
				return
			}
			summary.ConnectAccount = &status
			for index := range summary.Cashouts {
				cashout := &summary.Cashouts[index]
				if cashout.Status != "pending" || cashout.payoutID == "" {
					continue
				}
				payoutStatus, err := gateway.RetrieveDriverPayout(
					ctx, status.AccountID, cashout.payoutID,
				)
				if err != nil {
					s.log.Error("Stripe driver payout status could not be refreshed")
					writeError(w, http.StatusServiceUnavailable, "cash-out status could not be refreshed")
					return
				}
				cashout.Status = driverCashoutStatus(payoutStatus)
				if err := repository.RecordDriverCashoutPayout(
					ctx, cashout.ID, cashout.attempt, cashout.payoutID, cashout.Status,
				); err != nil {
					s.databaseError(w, err)
					return
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) createDriverCashout(w http.ResponseWriter, r *http.Request) {
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid Idempotency-Key header is required")
		return
	}
	repository, repoOK := s.repo.(DriverCashoutRepository)
	gateway, gatewayOK := s.payments.(DriverPayoutGateway)
	if !repoOK || !gatewayOK {
		writeError(w, http.StatusServiceUnavailable, "driver payouts are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	account, exists, err := repository.DriverConnectAccount(ctx, driverRef)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusConflict, "complete Stripe Connect onboarding first")
		return
	}
	status, err := gateway.RetrieveDriverConnectStatus(ctx, account.AccountID)
	if err != nil {
		s.log.Error("Stripe driver account status could not be checked")
		writeError(w, http.StatusServiceUnavailable, "Connect status could not be checked")
		return
	}
	if err := repository.UpdateDriverConnectStatus(ctx, status); err != nil {
		s.databaseError(w, err)
		return
	}
	if !status.PayoutsEnabled {
		writeError(w, http.StatusConflict, "Stripe has not enabled payouts for this account")
		return
	}
	request, err := repository.ReserveDriverCashout(
		ctx, driverRef, idempotencyKey, account.AccountID,
	)
	switch {
	case errors.Is(err, ErrIneligible):
		writeError(w, http.StatusConflict, "no settled earnings are available to cash out")
		return
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "cash-out key conflicts with an earlier request")
		return
	case err != nil:
		s.databaseError(w, err)
		return
	}
	if request.Status == "paid" {
		writeJSON(w, http.StatusOK, DriverCashout{
			ID: request.ID, AmountCents: request.AmountCents,
			Status: request.Status,
		})
		return
	}
	if request.TransferID == "" {
		transferID, err := gateway.CreateDriverTransfer(
			ctx, account.AccountID, request.AmountCents, request.ID,
			"shopstops-driver-transfer-"+request.ID,
		)
		if err != nil {
			s.log.Error("Stripe driver transfer failed")
			writeError(w, http.StatusServiceUnavailable, "cash-out transfer will be retried")
			return
		}
		request.TransferID = transferID
		if err := repository.RecordDriverCashoutTransfer(ctx, request.ID, transferID); err != nil {
			s.databaseError(w, err)
			return
		}
	}
	if request.PayoutID == "" {
		payoutID, payoutStatus, err := gateway.CreateDriverPayout(
			ctx, account.AccountID, request.AmountCents, request.ID,
			"shopstops-driver-payout-"+request.ID+"-"+strconv.Itoa(request.Attempt),
		)
		if err != nil {
			s.log.Error("Stripe driver payout failed")
			writeError(w, http.StatusServiceUnavailable, "cash-out payout will be retried")
			return
		}
		request.PayoutID = payoutID
		request.Status = driverCashoutStatus(payoutStatus)
		if err := repository.RecordDriverCashoutPayout(
			ctx, request.ID, request.Attempt, payoutID, request.Status,
		); err != nil {
			s.databaseError(w, err)
			return
		}
	} else if request.Status == "pending" {
		payoutStatus, err := gateway.RetrieveDriverPayout(
			ctx, account.AccountID, request.PayoutID,
		)
		if err != nil {
			s.log.Error("Stripe driver payout status could not be refreshed")
			writeError(w, http.StatusServiceUnavailable, "cash-out status could not be refreshed")
			return
		}
		request.Status = driverCashoutStatus(payoutStatus)
		if err := repository.RecordDriverCashoutPayout(
			ctx, request.ID, request.Attempt, request.PayoutID, request.Status,
		); err != nil {
			s.databaseError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, DriverCashout{
		ID: request.ID, AmountCents: request.AmountCents, Status: request.Status,
	})
}

func (s *Server) retryDriverCashout(w http.ResponseWriter, r *http.Request) {
	driverRef, ok := s.authorizedActorRole(w, r, "driver")
	if !ok {
		return
	}
	requestID := r.PathValue("cashoutId")
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if !isUUID(requestID) || idempotencyKey == "" || len(idempotencyKey) > 200 ||
		strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeError(w, http.StatusBadRequest, "valid cash-out ID and Idempotency-Key are required")
		return
	}
	repository, repoOK := s.repo.(DriverCashoutRepository)
	retryRepository, retryOK := s.repo.(DriverCashoutRetryRepository)
	gateway, gatewayOK := s.payments.(DriverPayoutGateway)
	if !repoOK || !retryOK || !gatewayOK {
		writeError(w, http.StatusServiceUnavailable, "driver payouts are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	request, err := retryRepository.PrepareDriverCashoutRetry(
		ctx, driverRef, requestID, idempotencyKey,
	)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "cash-out request not found")
		return
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "cash-out cannot be retried in its current state")
		return
	case err != nil:
		s.databaseError(w, err)
		return
	}
	if request.PayoutID == "" {
		payoutID, status, err := gateway.CreateDriverPayout(
			ctx, request.StripeAccountID, request.AmountCents, request.ID,
			"shopstops-driver-payout-"+request.ID+"-"+strconv.Itoa(request.Attempt),
		)
		if err != nil {
			s.log.Error("Stripe driver payout retry failed")
			writeError(w, http.StatusServiceUnavailable, "cash-out retry will be available")
			return
		}
		request.PayoutID = payoutID
		request.Status = driverCashoutStatus(status)
	} else {
		status, err := gateway.RetrieveDriverPayout(ctx, request.StripeAccountID, request.PayoutID)
		if err != nil {
			s.log.Error("Stripe driver payout retry status could not be checked")
			writeError(w, http.StatusServiceUnavailable, "cash-out status could not be checked")
			return
		}
		request.Status = driverCashoutStatus(status)
	}
	if err := repository.RecordDriverCashoutPayout(
		ctx, request.ID, request.Attempt, request.PayoutID, request.Status,
	); err != nil {
		s.databaseError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, DriverCashout{
		ID: request.ID, AmountCents: request.AmountCents, Status: request.Status,
	})
}

func driverCashoutStatus(stripeStatus string) string {
	switch stripeStatus {
	case "paid":
		return "paid"
	case "failed", "canceled":
		return "failed"
	default:
		return "pending"
	}
}

func (p *PostgresRepository) DriverConnectAccount(
	ctx context.Context, driverRef string,
) (DriverConnectStatus, bool, error) {
	var status DriverConnectStatus
	err := p.db.QueryRowContext(ctx, `
		SELECT stripe_account_id, details_submitted, payouts_enabled
		FROM driver_connect_accounts WHERE driver_ref = $1`, driverRef,
	).Scan(&status.AccountID, &status.DetailsSubmitted, &status.PayoutsEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return DriverConnectStatus{}, false, nil
	}
	return status, err == nil, err
}

func (p *PostgresRepository) SaveDriverConnectAccount(
	ctx context.Context, driverRef, accountID string,
) error {
	result, err := p.db.ExecContext(ctx, `
		INSERT INTO driver_connect_accounts (driver_ref, stripe_account_id)
		VALUES ($1, $2)
		ON CONFLICT (driver_ref) DO UPDATE
		SET updated_at = now()
		WHERE driver_connect_accounts.stripe_account_id = EXCLUDED.stripe_account_id`,
		driverRef, accountID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (p *PostgresRepository) UpdateDriverConnectStatus(
	ctx context.Context, status DriverConnectStatus,
) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE driver_connect_accounts
		SET details_submitted = $2, payouts_enabled = $3, updated_at = now()
		WHERE stripe_account_id = $1`,
		status.AccountID, status.DetailsSubmitted, status.PayoutsEnabled)
	return err
}

func (p *PostgresRepository) DriverCashoutSummary(
	ctx context.Context, driverRef string,
) (DriverCashoutSummary, error) {
	summary := DriverCashoutSummary{
		Earnings: make([]DriverEarning, 0), Cashouts: make([]DriverCashout, 0),
	}
	account, exists, err := p.DriverConnectAccount(ctx, driverRef)
	if err != nil {
		return summary, err
	}
	if exists {
		summary.ConnectAccount = &account
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT route_id::text, amount_cents, base_payout_cents,
		       along_route_bonus_cents, along_route_pickup_count,
		       currency, pickup_count,
		       status, created_at
		FROM driver_route_earnings
		WHERE driver_ref = $1
		ORDER BY created_at DESC
		LIMIT 100`, driverRef)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var earning DriverEarning
		if err := rows.Scan(
			&earning.RouteID, &earning.AmountCents, &earning.BasePayoutCents,
			&earning.AlongRouteBonusCents, &earning.AlongRoutePickupCount,
			&earning.Currency, &earning.PickupCount,
			&earning.Status, &earning.CreatedAt,
		); err != nil {
			rows.Close()
			return summary, err
		}
		if earning.Status == "available" {
			summary.AvailableCents += earning.AmountCents
		}
		if earning.Status == "reserved" {
			summary.PendingCents += earning.AmountCents
		}
		summary.Earnings = append(summary.Earnings, earning)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return summary, err
	}
	if err := rows.Close(); err != nil {
		return summary, err
	}
	rows, err = p.db.QueryContext(ctx, `
		SELECT id::text, amount_cents, status, created_at,
		       COALESCE(stripe_payout_id, ''), payout_attempt
		FROM driver_cashout_requests
		WHERE driver_ref = $1
		ORDER BY created_at DESC
		LIMIT 50`, driverRef)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var cashout DriverCashout
		if err := rows.Scan(
			&cashout.ID, &cashout.AmountCents, &cashout.Status, &cashout.CreatedAt,
			&cashout.payoutID, &cashout.attempt,
		); err != nil {
			return summary, err
		}
		summary.Cashouts = append(summary.Cashouts, cashout)
	}
	return summary, rows.Err()
}

func (p *PostgresRepository) ReserveDriverCashout(
	ctx context.Context, driverRef, key, accountID string,
) (driverCashoutRecord, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return driverCashoutRecord{}, err
	}
	defer tx.Rollback()
	var accountEnabled bool
	if err := tx.QueryRowContext(ctx, `
		SELECT payouts_enabled FROM driver_connect_accounts
		WHERE driver_ref = $1 AND stripe_account_id = $2
		FOR UPDATE`, driverRef, accountID).Scan(&accountEnabled); errors.Is(err, sql.ErrNoRows) {
		return driverCashoutRecord{}, ErrIneligible
	} else if err != nil {
		return driverCashoutRecord{}, err
	}
	var request driverCashoutRecord
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, driver_ref, stripe_account_id, amount_cents,
		       COALESCE(stripe_transfer_id, ''), COALESCE(stripe_payout_id, ''),
		       payout_attempt, status
		FROM driver_cashout_requests
		WHERE driver_ref = $1 AND idempotency_key = $2
		FOR UPDATE`, driverRef, key,
	).Scan(&request.ID, &request.DriverRef, &request.StripeAccountID,
		&request.AmountCents, &request.TransferID, &request.PayoutID,
		&request.Attempt, &request.Status)
	if err == nil {
		if request.StripeAccountID != accountID {
			return driverCashoutRecord{}, ErrConflict
		}
		return request, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return driverCashoutRecord{}, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, driver_ref, stripe_account_id, amount_cents,
		       COALESCE(stripe_transfer_id, ''), COALESCE(stripe_payout_id, ''),
		       payout_attempt, status
		FROM driver_cashout_requests
		WHERE driver_ref = $1 AND status = 'pending'
		ORDER BY created_at DESC
		LIMIT 1 FOR UPDATE`, driverRef,
	).Scan(&request.ID, &request.DriverRef, &request.StripeAccountID,
		&request.AmountCents, &request.TransferID, &request.PayoutID,
		&request.Attempt, &request.Status)
	if err == nil {
		if request.StripeAccountID != accountID {
			return driverCashoutRecord{}, ErrConflict
		}
		return request, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return driverCashoutRecord{}, err
	}
	if !accountEnabled {
		return driverCashoutRecord{}, ErrIneligible
	}
	var routes []string
	rows, err := tx.QueryContext(ctx, `
		SELECT route_id::text, amount_cents
		FROM driver_route_earnings
		WHERE driver_ref = $1 AND status = 'available'
		ORDER BY created_at, route_id
		FOR UPDATE`, driverRef)
	if err != nil {
		return driverCashoutRecord{}, err
	}
	for rows.Next() {
		var routeID string
		var amount int64
		if err := rows.Scan(&routeID, &amount); err != nil {
			rows.Close()
			return driverCashoutRecord{}, err
		}
		request.AmountCents += amount
		routes = append(routes, routeID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return driverCashoutRecord{}, err
	}
	if err := rows.Close(); err != nil {
		return driverCashoutRecord{}, err
	}
	if request.AmountCents <= 0 {
		return driverCashoutRecord{}, ErrIneligible
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO driver_cashout_requests (
		    driver_ref, idempotency_key, stripe_account_id, amount_cents
		) VALUES ($1, $2, $3, $4)
		RETURNING id::text, payout_attempt, status`,
		driverRef, key, accountID, request.AmountCents,
	).Scan(&request.ID, &request.Attempt, &request.Status)
	if err != nil {
		return driverCashoutRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO driver_cashout_payout_attempts (
		    cashout_request_id, attempt_number, retry_key
		) VALUES ($1, $2, 'initial')`, request.ID, request.Attempt); err != nil {
		return driverCashoutRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE driver_route_earnings
		SET status = 'reserved', cashout_request_id = $3
		WHERE driver_ref = $1 AND route_id = ANY($2::uuid[])`,
		driverRef, postgresUUIDArray(routes), request.ID); err != nil {
		return driverCashoutRecord{}, err
	}
	request.DriverRef, request.StripeAccountID = driverRef, accountID
	return request, tx.Commit()
}

func (p *PostgresRepository) RecordDriverCashoutTransfer(
	ctx context.Context, requestID, transferID string,
) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE driver_cashout_requests SET stripe_transfer_id = $2, updated_at = now()
		WHERE id = $1 AND (stripe_transfer_id IS NULL OR stripe_transfer_id = $2)`,
		requestID, transferID)
	return err
}

func (p *PostgresRepository) RecordDriverCashoutPayout(
	ctx context.Context, requestID string, attempt int, payoutID, status string,
) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE driver_cashout_payout_attempts
		SET stripe_payout_id = $3, status = $4, updated_at = now()
		WHERE cashout_request_id = $1 AND attempt_number = $2
		  AND (stripe_payout_id IS NULL OR stripe_payout_id = $3)`,
		requestID, attempt, payoutID, status)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return ErrConflict
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE driver_cashout_requests
		SET stripe_payout_id = $3, status = $4, updated_at = now()
		WHERE id = $1 AND payout_attempt = $2
		  AND (stripe_payout_id IS NULL OR stripe_payout_id = $3)`,
		requestID, attempt, payoutID, status)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return ErrConflict
	}
	if status == "paid" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE driver_route_earnings
			SET status = 'paid', paid_at = now()
			WHERE cashout_request_id = $1 AND status = 'reserved'`, requestID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *PostgresRepository) PrepareDriverCashoutRetry(
	ctx context.Context, driverRef, requestID, retryKey string,
) (driverCashoutRecord, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return driverCashoutRecord{}, err
	}
	defer tx.Rollback()
	var request driverCashoutRecord
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, driver_ref, stripe_account_id, amount_cents,
		       COALESCE(stripe_transfer_id, ''), COALESCE(stripe_payout_id, ''),
		       payout_attempt, status
		FROM driver_cashout_requests
		WHERE id = $1 AND driver_ref = $2
		FOR UPDATE`, requestID, driverRef,
	).Scan(&request.ID, &request.DriverRef, &request.StripeAccountID,
		&request.AmountCents, &request.TransferID, &request.PayoutID,
		&request.Attempt, &request.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return driverCashoutRecord{}, ErrNotFound
	}
	if err != nil {
		return driverCashoutRecord{}, err
	}
	var attempt int
	var payoutID, status string
	err = tx.QueryRowContext(ctx, `
		SELECT attempt_number, COALESCE(stripe_payout_id, ''), status
		FROM driver_cashout_payout_attempts
		WHERE cashout_request_id = $1 AND retry_key = $2
		FOR UPDATE`, requestID, "retry:"+retryKey,
	).Scan(&attempt, &payoutID, &status)
	if err == nil {
		request.Attempt, request.PayoutID, request.Status = attempt, payoutID, status
		return request, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return driverCashoutRecord{}, err
	}
	if request.Status != "failed" || request.TransferID == "" {
		return driverCashoutRecord{}, ErrConflict
	}
	request.Attempt++
	request.PayoutID = ""
	request.Status = "pending"
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO driver_cashout_payout_attempts (
		    cashout_request_id, attempt_number, retry_key
		) VALUES ($1, $2, $3)`,
		requestID, request.Attempt, "retry:"+retryKey); err != nil {
		return driverCashoutRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE driver_cashout_requests
		SET payout_attempt = $2, stripe_payout_id = NULL,
		    status = 'pending', updated_at = now()
		WHERE id = $1`, requestID, request.Attempt); err != nil {
		return driverCashoutRecord{}, err
	}
	return request, tx.Commit()
}

func (p *PostgresRepository) ApplyDriverPayoutEvent(
	ctx context.Context, accountID, payoutID, status string,
) error {
	status = driverCashoutStatus(status)
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE driver_cashout_payout_attempts attempt
		SET status = $3, updated_at = now()
		FROM driver_cashout_requests request
		WHERE attempt.cashout_request_id = request.id
		  AND request.stripe_account_id = $1
		  AND attempt.stripe_payout_id = $2`,
		accountID, payoutID, status)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE driver_cashout_requests request
		SET status = $3, updated_at = now()
		FROM driver_cashout_payout_attempts attempt
		WHERE attempt.cashout_request_id = request.id
		  AND request.payout_attempt = attempt.attempt_number
		  AND request.stripe_account_id = $1
		  AND attempt.stripe_payout_id = $2`,
		accountID, payoutID, status); err != nil {
		return err
	}
	if status == "paid" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE driver_route_earnings
			SET status = 'paid', paid_at = now()
			WHERE cashout_request_id IN (
			    SELECT id FROM driver_cashout_requests
			    WHERE stripe_account_id = $1 AND stripe_payout_id = $2
			) AND status = 'reserved'`, accountID, payoutID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func postgresUUIDArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, `"`+value+`"`)
	}
	return "{" + strings.Join(quoted, ",") + "}"
}
