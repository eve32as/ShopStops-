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

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

type CheckoutPayment struct {
	CheckoutID     string
	Currency       string
	AmountCents    int64
	Status         string
	PaymentIntentID string
}

type PaymentIntent struct {
	ID           string
	ClientSecret string
	Status       string
	AmountCents  int64
	Currency     string
}

type StripePaymentEvent struct {
	ID                 string
	Type               string
	CheckoutID         string
	PaymentIntentID    string
	AmountCents        int64
	Currency           string
	PaymentStatus      string
	ChargeID           string
}

type ConnectTransfer struct {
	CheckoutID  string
	MerchantID  string
	Destination string
	ChargeID    string
	AmountCents int64
	Currency    string
}

type PaymentRepository interface {
	CheckoutForPayment(context.Context, string, string) (CheckoutPayment, error)
	RecordPaymentIntent(context.Context, string, string, int64, string) error
	ApplyStripePaymentEvent(context.Context, StripePaymentEvent, int64, json.RawMessage) error
	PendingConnectTransfers(context.Context, string) ([]ConnectTransfer, error)
	RecordConnectTransfer(context.Context, string, string, string) error
}

type PaymentGateway interface {
	CreatePaymentIntent(context.Context, string, string, int64, string) (PaymentIntent, error)
	RetrievePaymentIntent(context.Context, string) (PaymentIntent, error)
	VerifyWebhook([]byte, string, string) (StripePaymentEvent, json.RawMessage, error)
	ProcessingFee(context.Context, string) (int64, error)
	CreateConnectTransfer(context.Context, ConnectTransfer, string) (string, error)
}

type StripeGateway struct {
	client *stripe.Client
}

func NewStripeGateway(secretKey string) *StripeGateway {
	return &StripeGateway{client: stripe.NewClient(secretKey)}
}

func (g *StripeGateway) CreatePaymentIntent(
	ctx context.Context, idempotencyKey, checkoutID string, amount int64, currency string,
) (PaymentIntent, error) {
	params := &stripe.PaymentIntentCreateParams{
		Amount:   stripe.Int64(amount),
		Currency: stripe.String(currency),
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
		Metadata: map[string]string{"shopstops_checkout_id": checkoutID},
		TransferGroup: stripe.String("shopstops_" + checkoutID),
	}
	params.SetIdempotencyKey(idempotencyKey)
	intent, err := g.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return PaymentIntent{}, err
	}
	return paymentIntentFromStripe(intent), nil
}

func (g *StripeGateway) RetrievePaymentIntent(
	ctx context.Context, paymentIntentID string,
) (PaymentIntent, error) {
	intent, err := g.client.V1PaymentIntents.Retrieve(
		ctx, paymentIntentID, &stripe.PaymentIntentRetrieveParams{},
	)
	if err != nil {
		return PaymentIntent{}, err
	}
	return paymentIntentFromStripe(intent), nil
}

func (g *StripeGateway) VerifyWebhook(
	payload []byte, signature, secret string,
) (StripePaymentEvent, json.RawMessage, error) {
	event, err := webhook.ConstructEvent(payload, signature, secret)
	if err != nil {
		return StripePaymentEvent{}, nil, err
	}
	result := StripePaymentEvent{ID: event.ID, Type: string(event.Type)}
	if result.Type != "payment_intent.succeeded" &&
		result.Type != "payment_intent.processing" &&
		result.Type != "payment_intent.payment_failed" {
		summary, _ := json.Marshal(map[string]string{"eventId": event.ID, "type": result.Type})
		return result, summary, nil
	}

	var intent stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &intent); err != nil {
		return StripePaymentEvent{}, nil, err
	}
	result.PaymentIntentID = intent.ID
	result.CheckoutID = intent.Metadata["shopstops_checkout_id"]
	result.AmountCents = intent.Amount
	result.Currency = string(intent.Currency)
	switch result.Type {
	case "payment_intent.succeeded":
		result.PaymentStatus = "succeeded"
		result.AmountCents = intent.AmountReceived
		if intent.LatestCharge != nil {
			result.ChargeID = intent.LatestCharge.ID
		}
	case "payment_intent.processing":
		result.PaymentStatus = "processing"
	case "payment_intent.payment_failed":
		result.PaymentStatus = "failed"
	}
	summary, err := json.Marshal(map[string]any{
		"eventId": result.ID, "type": result.Type,
		"paymentIntentId": result.PaymentIntentID, "checkoutId": result.CheckoutID,
		"amountCents": result.AmountCents, "currency": result.Currency,
		"status": result.PaymentStatus, "chargeId": result.ChargeID,
	})
	if err != nil {
		return StripePaymentEvent{}, nil, err
	}
	return result, summary, nil
}

func (g *StripeGateway) ProcessingFee(ctx context.Context, paymentIntentID string) (int64, error) {
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge.balance_transaction")
	intent, err := g.client.V1PaymentIntents.Retrieve(ctx, paymentIntentID, params)
	if err != nil {
		return 0, err
	}
	if intent.LatestCharge == nil || intent.LatestCharge.BalanceTransaction == nil {
		return 0, errors.New("Stripe charge balance transaction is not available")
	}
	return intent.LatestCharge.BalanceTransaction.Fee, nil
}

func (g *StripeGateway) CreateConnectTransfer(
	ctx context.Context, transfer ConnectTransfer, idempotencyKey string,
) (string, error) {
	params := &stripe.TransferCreateParams{
		Amount:           stripe.Int64(transfer.AmountCents),
		Currency:         stripe.String(transfer.Currency),
		Destination:      stripe.String(transfer.Destination),
		SourceTransaction: stripe.String(transfer.ChargeID),
		TransferGroup:    stripe.String("shopstops_" + transfer.CheckoutID),
		Metadata: map[string]string{
			"shopstops_checkout_id": transfer.CheckoutID,
			"shopstops_merchant_id": transfer.MerchantID,
		},
	}
	params.SetIdempotencyKey(idempotencyKey)
	result, err := g.client.V1Transfers.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return result.ID, nil
}

func paymentIntentFromStripe(intent *stripe.PaymentIntent) PaymentIntent {
	if intent == nil {
		return PaymentIntent{}
	}
	result := PaymentIntent{
		ID: intent.ID, Status: string(intent.Status),
		AmountCents: intent.Amount, Currency: string(intent.Currency),
	}
	if intent.ClientSecret != nil {
		result.ClientSecret = *intent.ClientSecret
	}
	return result
}

func (s *Server) createCheckoutPayment(w http.ResponseWriter, r *http.Request) {
	checkoutID := r.PathValue("checkoutId")
	customerRef := strings.TrimSpace(r.Header.Get("X-ShopStops-Actor"))
	if !isUUID(checkoutID) || customerRef == "" || len(customerRef) > 200 {
		writeError(w, http.StatusBadRequest, "valid checkout and authenticated customer are required")
		return
	}
	repository, ok := s.repo.(PaymentRepository)
	if !ok || s.payments == nil {
		writeError(w, http.StatusServiceUnavailable, "payment processing is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	checkout, err := repository.CheckoutForPayment(ctx, checkoutID, customerRef)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "checkout not found")
		return
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "checkout is not payable")
		return
	case err != nil:
		s.databaseError(w, err)
		return
	}
	if checkout.AmountCents < 50 {
		writeError(w, http.StatusConflict, "checkout total is below Stripe's minimum charge")
		return
	}

	var intent PaymentIntent
	if checkout.PaymentIntentID != "" {
		intent, err = s.payments.RetrievePaymentIntent(ctx, checkout.PaymentIntentID)
	} else {
		intent, err = s.payments.CreatePaymentIntent(
			ctx, "shopstops-checkout-"+checkout.ID, checkout.ID,
			checkout.AmountCents, checkout.Currency,
		)
		if err == nil {
			if intent.AmountCents != checkout.AmountCents ||
				!strings.EqualFold(intent.Currency, checkout.Currency) ||
				intent.ClientSecret == "" {
				writeError(w, http.StatusBadGateway, "payment provider returned an invalid payment intent")
				return
			}
			err = repository.RecordPaymentIntent(
				ctx, checkout.ID, intent.ID, intent.AmountCents, intent.Status,
			)
		}
	}
	if err != nil {
		s.log.Error("Stripe payment intent request failed")
		writeError(w, http.StatusBadGateway, "payment could not be initialized")
		return
	}
	if intent.ClientSecret == "" {
		writeError(w, http.StatusBadGateway, "payment provider returned no client secret")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"paymentIntentId": intent.ID, "clientSecret": intent.ClientSecret,
		"status": intent.Status, "amountCents": intent.AmountCents,
		"currency": intent.Currency,
	})
}

func (s *Server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if s.payments == nil || s.webhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "payment processing is unavailable")
		return
	}
	repository, ok := s.repo.(PaymentRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "payment processing is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	event, summary, err := s.payments.VerifyWebhook(
		payload, r.Header.Get("Stripe-Signature"), s.webhookSecret,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid Stripe webhook signature or event")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	var processingFee int64
	if event.Type == "payment_intent.succeeded" {
		processingFee, err = s.payments.ProcessingFee(ctx, event.PaymentIntentID)
		if err != nil {
			s.log.Error("Stripe processing fee could not be retrieved")
			writeError(w, http.StatusServiceUnavailable, "payment event will be retried")
			return
		}
	}
	if err := repository.ApplyStripePaymentEvent(ctx, event, processingFee, summary); err != nil {
		s.log.Error("Stripe payment event could not be recorded")
		writeError(w, http.StatusServiceUnavailable, "payment event will be retried")
		return
	}
	transfers, err := repository.PendingConnectTransfers(ctx, event.CheckoutID)
	if err != nil {
		s.log.Error("pending Connect transfers could not be loaded")
		writeError(w, http.StatusServiceUnavailable, "payment event will be retried")
		return
	}
	for _, transfer := range transfers {
		if transfer.AmountCents == 0 {
			if err := repository.RecordConnectTransfer(
				ctx, transfer.CheckoutID, transfer.MerchantID, "",
			); err != nil {
				s.log.Error("zero-value Connect transfer could not be finalized")
				writeError(w, http.StatusServiceUnavailable, "payment event will be retried")
				return
			}
			continue
		}
		transferID, err := s.payments.CreateConnectTransfer(
			ctx, transfer,
			"shopstops-transfer-"+transfer.CheckoutID+"-"+transfer.MerchantID,
		)
		if err != nil {
			s.log.Error("Stripe Connect transfer request failed")
			writeError(w, http.StatusServiceUnavailable, "payment event will be retried")
			return
		}
		if err := repository.RecordConnectTransfer(
			ctx, transfer.CheckoutID, transfer.MerchantID, transferID,
		); err != nil {
			s.log.Error("Stripe Connect transfer could not be recorded")
			writeError(w, http.StatusServiceUnavailable, "payment event will be retried")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

func (p *PostgresRepository) CheckoutForPayment(
	ctx context.Context, checkoutID, customerRef string,
) (CheckoutPayment, error) {
	var payment CheckoutPayment
	var paymentStatus sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT c.id, c.currency, COALESCE(sum(line.amount_cents), 0),
		       c.status, cp.stripe_payment_intent_id, cp.status
		FROM unified_checkouts c
		LEFT JOIN checkout_invoice_lines line ON line.checkout_id = c.id
		LEFT JOIN checkout_payments cp ON cp.checkout_id = c.id
		WHERE c.id = $1 AND c.customer_ref = $2
		GROUP BY c.id, c.currency, c.status, cp.stripe_payment_intent_id, cp.status`,
		checkoutID, customerRef,
	).Scan(
		&payment.CheckoutID, &payment.Currency, &payment.AmountCents,
		&payment.Status, &payment.PaymentIntentID, &paymentStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CheckoutPayment{}, ErrNotFound
	}
	if err != nil {
		return CheckoutPayment{}, err
	}
	if payment.Status != "quoted" && !(payment.Status == "paid" && payment.PaymentIntentID != "") {
		return CheckoutPayment{}, ErrConflict
	}
	if payment.PaymentIntentID == "" {
		var payoutAccountsReady bool
		if err := p.db.QueryRowContext(ctx, `
			SELECT NOT EXISTS (
				SELECT 1
				FROM checkout_invoice_lines line
				JOIN merchants merchant ON merchant.id = line.merchant_id
				WHERE line.checkout_id = $1
				  AND line.line_type = 'merchant_items'
				  AND line.amount_cents > 0
				  AND merchant.stripe_account_id IS NULL
			)`, checkoutID,
		).Scan(&payoutAccountsReady); err != nil {
			return CheckoutPayment{}, err
		}
		if !payoutAccountsReady {
			return CheckoutPayment{}, ErrConflict
		}
	}
	return payment, nil
}

func (p *PostgresRepository) RecordPaymentIntent(
	ctx context.Context, checkoutID, paymentIntentID string, amount int64, status string,
) error {
	if status != "requires_payment_method" && status != "processing" {
		return errors.New("Stripe returned an unsupported initial payment status")
	}
	var existing bool
	err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM checkout_payments
			WHERE checkout_id = $1 AND stripe_payment_intent_id = $2
		)`, checkoutID, paymentIntentID,
	).Scan(&existing)
	if err != nil {
		return err
	}
	if existing {
		return nil
	}
	_, err = p.db.ExecContext(ctx, `
		SELECT record_checkout_payment($1, $2, $3, 0, $4)`,
		checkoutID, paymentIntentID, amount, status,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "P0001" {
			return ErrConflict
		}
	}
	return err
}

func (p *PostgresRepository) ApplyStripePaymentEvent(
	ctx context.Context, event StripePaymentEvent, processingFee int64, summary json.RawMessage,
) error {
	var checkoutID sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT process_stripe_checkout_event(
			$1, $2, $3::jsonb, $4, $5, $6, $7, $8, $9, $10
		)`,
		event.ID, event.Type, string(summary), nullableString(event.CheckoutID),
		nullableString(event.PaymentIntentID), nullableInt(event.AmountCents),
		nullableString(event.Currency), nullableString(event.PaymentStatus), processingFee,
		nullableString(event.ChargeID),
	).Scan(&checkoutID)
	return err
}

func (p *PostgresRepository) PendingConnectTransfers(
	ctx context.Context, checkoutID string,
) ([]ConnectTransfer, error) {
	if checkoutID == "" {
		return []ConnectTransfer{}, nil
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT transfer.checkout_id, transfer.merchant_id,
		       merchant.stripe_account_id, transfer.source_charge_id,
		       transfer.transfer_amount_cents, checkout.currency
		FROM connect_transfers transfer
		JOIN merchants merchant ON merchant.id = transfer.merchant_id
		JOIN unified_checkouts checkout ON checkout.id = transfer.checkout_id
		WHERE transfer.checkout_id = $1 AND transfer.status IN ('pending', 'failed')
		ORDER BY transfer.merchant_id`, checkoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	transfers := make([]ConnectTransfer, 0)
	for rows.Next() {
		var transfer ConnectTransfer
		var destination sql.NullString
		if err := rows.Scan(
			&transfer.CheckoutID, &transfer.MerchantID, &destination,
			&transfer.ChargeID, &transfer.AmountCents, &transfer.Currency,
		); err != nil {
			return nil, err
		}
		if !destination.Valid && transfer.AmountCents > 0 {
			return nil, ErrIneligible
		}
		if destination.Valid {
			transfer.Destination = destination.String
		}
		transfers = append(transfers, transfer)
	}
	return transfers, rows.Err()
}

func (p *PostgresRepository) RecordConnectTransfer(
	ctx context.Context, checkoutID, merchantID, transferID string,
) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE connect_transfers
		SET stripe_transfer_id = NULLIF($3, ''), status = 'transferred'
		WHERE checkout_id = $1 AND merchant_id = $2
		  AND status IN ('pending', 'failed')
		  AND (stripe_transfer_id IS NULL OR stripe_transfer_id = NULLIF($3, ''))`,
		checkoutID, merchantID, transferID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var transferred bool
		err := p.db.QueryRowContext(ctx, `
			SELECT status = 'transferred' AND
			       stripe_transfer_id IS NOT DISTINCT FROM NULLIF($3, '')
			FROM connect_transfers WHERE checkout_id = $1 AND merchant_id = $2`,
			checkoutID, merchantID, transferID,
		).Scan(&transferred)
		if err != nil {
			return err
		}
		if !transferred {
			return ErrConflict
		}
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
