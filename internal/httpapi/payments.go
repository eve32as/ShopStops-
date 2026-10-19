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
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

type CheckoutPayment struct {
	CheckoutID      string
	Currency        string
	AmountCents     int64
	Status          string
	PaymentIntentID string
	IdempotencyKey  string
}

type PaymentIntent struct {
	ID           string
	ClientSecret string
	Status       string
	AmountCents  int64
	Currency     string
}

type StripePaymentEvent struct {
	ID                   string
	Type                 string
	CheckoutID           string
	PaymentIntentID      string
	AmountCents          int64
	Currency             string
	PaymentStatus        string
	ChargeID             string
	MerchantID           string
	CustomerID           string
	SubscriptionID       string
	SubscriptionStatus   string
	PeriodEnd            time.Time
	PaymentMethodID      string
	RefundID             string
	RefundStatus         string
	RefundIdempotencyKey string
	DisputeID            string
	DisputeStatus        string
	DisputeReason        string
	EvidenceDueBy        time.Time
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
	BeginConnectTransferAttempt(context.Context, string, string) error
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
		Metadata:      map[string]string{"shopstops_checkout_id": checkoutID},
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
	if isLifecycleStripeEvent(result.Type) {
		result, err = parseStripeLifecycleEvent(result, event.Data.Raw)
		if err != nil {
			return StripePaymentEvent{}, nil, err
		}
		summary, err := json.Marshal(map[string]any{
			"eventId": result.ID, "type": result.Type, "merchantId": result.MerchantID,
			"customerId": result.CustomerID, "subscriptionId": result.SubscriptionID,
			"subscriptionStatus": result.SubscriptionStatus,
			"periodEnd":          result.PeriodEnd, "paymentMethodId": result.PaymentMethodID,
			"checkoutId": result.CheckoutID, "paymentIntentId": result.PaymentIntentID,
			"refundId": result.RefundID, "refundStatus": result.RefundStatus,
			"refundIdempotencyKey": result.RefundIdempotencyKey,
			"disputeId":            result.DisputeID, "disputeStatus": result.DisputeStatus,
			"disputeReason": result.DisputeReason,
		})
		return result, summary, err
	}
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

func isLifecycleStripeEvent(eventType string) bool {
	switch eventType {
	case "customer.subscription.created", "customer.subscription.updated",
		"customer.subscription.deleted", "invoice.payment_succeeded",
		"invoice.payment_failed", "setup_intent.succeeded",
		"refund.created", "refund.updated", "charge.dispute.created",
		"charge.dispute.updated", "charge.dispute.closed":
		return true
	default:
		return false
	}
}

func parseStripeLifecycleEvent(
	event StripePaymentEvent, raw json.RawMessage,
) (StripePaymentEvent, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return StripePaymentEvent{}, err
	}
	readString := func(key string) string {
		var value string
		_ = json.Unmarshal(object[key], &value)
		return value
	}
	readID := func(key string) string {
		return stripeObjectID(object[key])
	}
	readAmount := func(key string) int64 {
		var value int64
		_ = json.Unmarshal(object[key], &value)
		return value
	}
	event.CustomerID = readID("customer")
	event.PaymentIntentID = readID("payment_intent")
	event.Currency = strings.ToLower(readString("currency"))
	metadata := map[string]string{}
	_ = json.Unmarshal(object["metadata"], &metadata)
	event.MerchantID = metadata["shopstops_merchant_id"]
	event.CheckoutID = metadata["shopstops_checkout_id"]
	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated",
		"customer.subscription.deleted":
		event.SubscriptionID = readString("id")
		event.SubscriptionStatus = readString("status")
		event.PeriodEnd = stripePeriodEnd(object)
	case "invoice.payment_succeeded", "invoice.payment_failed":
		event.SubscriptionID = stripeInvoiceSubscriptionID(object)
		event.PeriodEnd = stripeInvoicePeriodEnd(object)
		if event.MerchantID == "" {
			var parent struct {
				SubscriptionDetails struct {
					Metadata map[string]string `json:"metadata"`
				} `json:"subscription_details"`
			}
			_ = json.Unmarshal(object["parent"], &parent)
			event.MerchantID = parent.SubscriptionDetails.Metadata["shopstops_merchant_id"]
		}
		if event.Type == "invoice.payment_failed" {
			event.PaymentStatus = "failed"
		} else {
			event.PaymentStatus = "succeeded"
		}
	case "setup_intent.succeeded":
		event.PaymentMethodID = readID("payment_method")
	case "refund.created", "refund.updated":
		event.RefundID = readString("id")
		event.RefundStatus = readString("status")
		event.AmountCents = readAmount("amount")
		event.RefundIdempotencyKey = metadata["shopstops_refund_key"]
	case "charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed":
		event.DisputeID = readString("id")
		event.DisputeStatus = readString("status")
		event.DisputeReason = readString("reason")
		event.AmountCents = readAmount("amount")
		var dueBy int64
		var evidence map[string]json.RawMessage
		_ = json.Unmarshal(object["evidence_details"], &evidence)
		_ = json.Unmarshal(evidence["due_by"], &dueBy)
		if dueBy > 0 {
			event.EvidenceDueBy = time.Unix(dueBy, 0).UTC()
		}
	}
	return event, nil
}

func stripeObjectID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var object struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &object)
	return object.ID
}

func stripePeriodEnd(object map[string]json.RawMessage) time.Time {
	var items struct {
		Data []struct {
			CurrentPeriodEnd int64 `json:"current_period_end"`
		} `json:"data"`
	}
	_ = json.Unmarshal(object["items"], &items)
	if len(items.Data) == 0 || items.Data[0].CurrentPeriodEnd <= 0 {
		return time.Time{}
	}
	return time.Unix(items.Data[0].CurrentPeriodEnd, 0).UTC()
}

func stripeInvoiceSubscriptionID(object map[string]json.RawMessage) string {
	var parent struct {
		SubscriptionDetails struct {
			Subscription json.RawMessage `json:"subscription"`
		} `json:"subscription_details"`
	}
	_ = json.Unmarshal(object["parent"], &parent)
	return stripeObjectID(parent.SubscriptionDetails.Subscription)
}

func stripeInvoicePeriodEnd(object map[string]json.RawMessage) time.Time {
	var lines struct {
		Data []struct {
			Period struct {
				End int64 `json:"end"`
			} `json:"period"`
		} `json:"data"`
	}
	_ = json.Unmarshal(object["lines"], &lines)
	if len(lines.Data) == 0 || lines.Data[0].Period.End <= 0 {
		return time.Time{}
	}
	return time.Unix(lines.Data[0].Period.End, 0).UTC()
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
		Amount:            stripe.Int64(transfer.AmountCents),
		Currency:          stripe.String(transfer.Currency),
		Destination:       stripe.String(transfer.Destination),
		SourceTransaction: stripe.String(transfer.ChargeID),
		TransferGroup:     stripe.String("shopstops_" + transfer.CheckoutID),
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
		ClientSecret: intent.ClientSecret,
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
			ctx, checkout.IdempotencyKey, checkout.CheckoutID,
			checkout.AmountCents, checkout.Currency,
		)
		if err == nil {
			if intent.ID == "" || intent.AmountCents != checkout.AmountCents ||
				!strings.EqualFold(intent.Currency, checkout.Currency) ||
				intent.ClientSecret == "" {
				err = errors.New("Stripe returned an invalid payment intent")
			} else {
				err = repository.RecordPaymentIntent(ctx, checkout.CheckoutID,
					intent.ID, intent.AmountCents, intent.Status)
			}
		}
	}
	if err != nil {
		s.log.Error("Stripe payment intent request failed")
		writeError(w, http.StatusBadGateway, "payment could not be initialized")
		return
	}
	if intent.ID == "" || intent.AmountCents != checkout.AmountCents ||
		!strings.EqualFold(intent.Currency, checkout.Currency) {
		writeError(w, http.StatusBadGateway, "payment provider returned an invalid payment intent")
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
	if isLifecycleStripeEvent(event.Type) {
		s.handleStripeLifecycleWebhook(w, ctx, event, summary)
		return
	}
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
		if err := repository.BeginConnectTransferAttempt(
			ctx, transfer.CheckoutID, transfer.MerchantID,
		); err != nil {
			s.log.Error("Stripe Connect transfer requires reconciliation")
			writeError(w, http.StatusServiceUnavailable, "payment event will be retried")
			return
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

func (s *Server) handleStripeLifecycleWebhook(
	w http.ResponseWriter, ctx context.Context, event StripePaymentEvent,
	summary json.RawMessage,
) {
	repository, repoOK := s.repo.(LifecycleRepository)
	gateway, gatewayOK := s.payments.(StripeLifecycleGateway)
	if !repoOK || !gatewayOK {
		writeError(w, http.StatusServiceUnavailable, "Stripe lifecycle processing is unavailable")
		return
	}
	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated",
		"customer.subscription.deleted", "invoice.payment_succeeded",
		"invoice.payment_failed", "setup_intent.succeeded":
		if event.Type == "setup_intent.succeeded" {
			if event.CustomerID == "" || event.PaymentMethodID == "" {
				writeError(w, http.StatusBadRequest, "Stripe setup event is missing payment method details")
				return
			}
			if err := gateway.SetMerchantDefaultPaymentMethod(
				ctx, event.CustomerID, event.PaymentMethodID,
			); err != nil {
				s.log.Error("merchant default payment method could not be saved")
				writeError(w, http.StatusServiceUnavailable, "billing event will be retried")
				return
			}
		}
		if err := repository.ApplyMerchantBillingEvent(ctx, event, summary); err != nil {
			s.log.Error("merchant billing event could not be recorded")
			writeError(w, http.StatusServiceUnavailable, "billing event will be retried")
			return
		}
	case "refund.created", "refund.updated":
		if event.RefundID == "" {
			writeError(w, http.StatusBadRequest, "Stripe refund event is missing its ID")
			return
		}
		if err := repository.ApplyCheckoutRefundEvent(ctx, event, summary); err != nil {
			s.log.Error("checkout refund event could not be recorded")
			writeError(w, http.StatusServiceUnavailable, "refund event will be retried")
			return
		}
		if err := s.processPendingTransferReversals(
			ctx, repository, gateway, event.RefundID, "",
		); err != nil {
			s.log.Error("refund transfer reversal requires reconciliation")
			writeError(w, http.StatusServiceUnavailable, "refund event will be retried")
			return
		}
	case "charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed":
		if event.DisputeID == "" || event.PaymentIntentID == "" {
			writeError(w, http.StatusBadRequest, "Stripe dispute event is missing payment details")
			return
		}
		if err := repository.ApplyStripeDisputeEvent(ctx, event, summary); err != nil {
			s.log.Error("Stripe dispute event could not be recorded")
			writeError(w, http.StatusServiceUnavailable, "dispute event will be retried")
			return
		}
		if err := s.processPendingTransferReversals(
			ctx, repository, gateway, "", event.DisputeID,
		); err != nil {
			s.log.Error("dispute transfer reversal requires reconciliation")
			writeError(w, http.StatusServiceUnavailable, "dispute event will be retried")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "unsupported Stripe lifecycle event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

func (s *Server) processPendingTransferReversals(
	ctx context.Context, repository LifecycleRepository,
	gateway StripeLifecycleGateway, refundID, disputeID string,
) error {
	reversals, err := repository.PendingTransferReversals(ctx, refundID, disputeID)
	if err != nil {
		return err
	}
	for _, reversal := range reversals {
		if err := repository.BeginTransferReversalAttempt(ctx, reversal.ID); err != nil {
			return err
		}
		reversalID, err := gateway.CreateTransferReversal(ctx, reversal)
		if err != nil {
			return err
		}
		if err := repository.RecordTransferReversal(ctx, reversal.ID, reversalID); err != nil {
			return err
		}
	}
	return nil
}

func (p *PostgresRepository) CheckoutForPayment(
	ctx context.Context, checkoutID, customerRef string,
) (CheckoutPayment, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return CheckoutPayment{}, err
	}
	defer tx.Rollback()
	var payment CheckoutPayment
	var paymentIntentID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT c.id, c.currency, c.status, cp.stripe_payment_intent_id
		FROM unified_checkouts c
		LEFT JOIN checkout_payments cp ON cp.checkout_id = c.id
		WHERE c.id = $1 AND c.customer_ref = $2
		FOR UPDATE OF c`,
		checkoutID, customerRef,
	).Scan(
		&payment.CheckoutID, &payment.Currency, &payment.Status, &paymentIntentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CheckoutPayment{}, ErrNotFound
	}
	if err != nil {
		return CheckoutPayment{}, err
	}
	if paymentIntentID.Valid {
		payment.PaymentIntentID = paymentIntentID.String
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(sum(amount_cents), 0)
		FROM checkout_invoice_lines WHERE checkout_id = $1`, checkoutID,
	).Scan(&payment.AmountCents); err != nil {
		return CheckoutPayment{}, err
	}
	if payment.Status != "quoted" && !(payment.Status == "paid" && payment.PaymentIntentID != "") {
		return CheckoutPayment{}, ErrConflict
	}
	if payment.PaymentIntentID == "" && payment.AmountCents < 50 {
		return CheckoutPayment{}, ErrConflict
	}
	if payment.PaymentIntentID == "" {
		var payoutAccountsReady bool
		if err := tx.QueryRowContext(ctx, `
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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stripe_payment_intent_attempts (checkout_id, idempotency_key)
			VALUES ($1, 'shopstops-checkout-' || $1::text)
			ON CONFLICT (checkout_id) DO NOTHING`, checkoutID); err != nil {
			return CheckoutPayment{}, err
		}
		var attemptIsRecent bool
		if err := tx.QueryRowContext(ctx, `
			SELECT idempotency_key, created_at > now() - interval '23 hours'
			FROM stripe_payment_intent_attempts WHERE checkout_id = $1`,
			checkoutID,
		).Scan(&payment.IdempotencyKey, &attemptIsRecent); err != nil {
			return CheckoutPayment{}, err
		}
		if !attemptIsRecent {
			return CheckoutPayment{}, ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return CheckoutPayment{}, err
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
	var paymentRecordID string
	err = p.db.QueryRowContext(ctx, `
		SELECT record_checkout_payment($1, $2, $3, 0, $4)::text`,
		checkoutID, paymentIntentID, amount, status,
	).Scan(&paymentRecordID)
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

func (p *PostgresRepository) BeginConnectTransferAttempt(
	ctx context.Context, checkoutID, merchantID string,
) error {
	var attemptIsRecent bool
	err := p.db.QueryRowContext(ctx, `
		UPDATE connect_transfers
		SET transfer_attempted_at = COALESCE(transfer_attempted_at, now())
		WHERE checkout_id = $1 AND merchant_id = $2
		  AND status IN ('pending', 'failed')
		RETURNING transfer_attempted_at > now() - interval '23 hours'`,
		checkoutID, merchantID,
	).Scan(&attemptIsRecent)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !attemptIsRecent) {
		return ErrConflict
	}
	return err
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
