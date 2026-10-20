package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

type fakePaymentRepository struct {
	fakeRepository
	checkout           CheckoutPayment
	checkoutErr        error
	recordedIntentID   string
	recordedAmount     int64
	recordedStatus     string
	event              StripePaymentEvent
	eventFee           int64
	eventSummary       json.RawMessage
	eventErr           error
	transfers          []ConnectTransfer
	recordedTransfers  []string
	transferAttemptErr error
	billingProfile     MerchantBillingProfile
	billingProfileErr  error
	refund             RefundRecord
	refundErr          error
	refundRecord       RefundRecord
	adminCheckouts     []AdminCheckoutRecord
	adminCheckoutErr   error
	adminSearch        struct {
		checkoutID   string
		status       string
		disputeState string
		limit        int
	}
	billingEvent    StripePaymentEvent
	billingPayload  json.RawMessage
	billingEventErr error
	refundEvent     StripePaymentEvent
	refundEventErr  error
	disputeEvent    StripePaymentEvent
	disputeEventErr error
	reversals       []TransferReversal
	reversalIDs     []string
	reversalErr     error
}

func (f *fakePaymentRepository) SearchAdminCheckouts(
	_ context.Context, checkoutID, status, disputeStatus string, limit int,
) ([]AdminCheckoutRecord, error) {
	f.adminSearch.checkoutID, f.adminSearch.status = checkoutID, status
	f.adminSearch.disputeState, f.adminSearch.limit = disputeStatus, limit
	return f.adminCheckouts, f.adminCheckoutErr
}

func (f *fakePaymentRepository) CheckoutForPayment(
	_ context.Context, checkoutID, customerRef string,
) (CheckoutPayment, error) {
	if f.checkoutErr != nil {
		return CheckoutPayment{}, f.checkoutErr
	}
	if checkoutID != f.checkout.CheckoutID || customerRef != "customer-1" {
		return CheckoutPayment{}, ErrNotFound
	}
	return f.checkout, nil
}

func (f *fakePaymentRepository) RecordPaymentIntent(
	_ context.Context, checkoutID, paymentIntentID string, amount int64, status string,
) error {
	f.recordedIntentID, f.recordedAmount, f.recordedStatus =
		paymentIntentID, amount, status
	return nil
}

func (f *fakePaymentRepository) ApplyStripePaymentEvent(
	_ context.Context, event StripePaymentEvent, fee int64, summary json.RawMessage,
) error {
	f.event, f.eventFee, f.eventSummary = event, fee, summary
	return f.eventErr
}

func (f *fakePaymentRepository) PendingConnectTransfers(
	context.Context, string,
) ([]ConnectTransfer, error) {
	return f.transfers, nil
}

func (f *fakePaymentRepository) RecordConnectTransfer(
	_ context.Context, checkoutID, merchantID, transferID string,
) error {
	f.recordedTransfers = append(f.recordedTransfers, transferID)
	return nil
}

func (f *fakePaymentRepository) BeginConnectTransferAttempt(
	context.Context, string, string,
) error {
	return f.transferAttemptErr
}

func (f *fakePaymentRepository) MerchantBillingProfile(
	context.Context, string,
) (MerchantBillingProfile, error) {
	return f.billingProfile, f.billingProfileErr
}

func (f *fakePaymentRepository) ReserveMerchantBillingAttempt(
	context.Context, string, string,
) (MerchantBillingProfile, error) {
	return f.billingProfile, f.billingProfileErr
}

func (f *fakePaymentRepository) RecordMerchantStripeCustomer(
	context.Context, string, string,
) error {
	return nil
}

func (f *fakePaymentRepository) RecordMerchantStripeSubscription(
	context.Context, string, string, string,
) error {
	return nil
}

func (f *fakePaymentRepository) ApplyMerchantBillingEvent(
	_ context.Context, event StripePaymentEvent, summary json.RawMessage,
) error {
	f.billingEvent, f.billingPayload = event, summary
	return f.billingEventErr
}

func (f *fakePaymentRepository) ReserveCheckoutRefund(
	_ context.Context, checkoutID, actor, key, reason string,
) (RefundRecord, error) {
	if f.refundErr != nil {
		return RefundRecord{}, f.refundErr
	}
	f.refund = RefundRecord{
		ID: "refund-record", CheckoutID: checkoutID, PaymentIntentID: "pi_test",
		IdempotencyKey: key, AmountCents: 3120, Reason: reason,
	}
	return f.refund, nil
}

func (f *fakePaymentRepository) RecordStripeRefund(
	_ context.Context, refundID, stripeRefundID, status string,
) error {
	f.refundRecord = RefundRecord{ID: refundID, StripeRefundID: stripeRefundID, Status: status}
	return nil
}

func (f *fakePaymentRepository) ApplyCheckoutRefundEvent(
	_ context.Context, event StripePaymentEvent, _ json.RawMessage,
) error {
	f.refundEvent = event
	return f.refundEventErr
}

func (f *fakePaymentRepository) ApplyStripeDisputeEvent(
	_ context.Context, event StripePaymentEvent, _ json.RawMessage,
) error {
	f.disputeEvent = event
	return f.disputeEventErr
}

func (f *fakePaymentRepository) PendingTransferReversals(
	context.Context, string, string,
) ([]TransferReversal, error) {
	return f.reversals, nil
}

func (f *fakePaymentRepository) BeginTransferReversalAttempt(context.Context, string) error {
	return f.reversalErr
}

func (f *fakePaymentRepository) RecordTransferReversal(
	_ context.Context, reversalID, stripeReversalID string,
) error {
	f.reversalIDs = append(f.reversalIDs, stripeReversalID)
	return nil
}

type fakePaymentGateway struct {
	createdIntent      PaymentIntent
	retrievedIntent    PaymentIntent
	event              StripePaymentEvent
	summary            json.RawMessage
	verifyErr          error
	processingFeeCents int64
	transferID         string
	transferErr        error
	createKey          string
	createCheckoutID   string
	createAmount       int64
	transferKey        string
	customerID         string
	setupIntentID      string
	setupClientSecret  string
	subscriptionID     string
	subscriptionStatus string
	refundID           string
	refundStatus       string
	reversalID         string
	reversalEvent      TransferReversal
	refundEvent        RefundRecord
	setupMerchantID    string
	subscriptionPrice  string
	lifecycleErr       error
}

func (f *fakePaymentGateway) CreatePaymentIntent(
	_ context.Context, key, checkoutID string, amount int64, _ string,
) (PaymentIntent, error) {
	f.createKey, f.createCheckoutID, f.createAmount = key, checkoutID, amount
	return f.createdIntent, nil
}

func (f *fakePaymentGateway) RetrievePaymentIntent(
	context.Context, string,
) (PaymentIntent, error) {
	return f.retrievedIntent, nil
}

func (f *fakePaymentGateway) VerifyWebhook(
	[]byte, string, string,
) (StripePaymentEvent, json.RawMessage, error) {
	return f.event, f.summary, f.verifyErr
}

func (f *fakePaymentGateway) ProcessingFee(context.Context, string) (int64, error) {
	return f.processingFeeCents, nil
}

func (f *fakePaymentGateway) CreateConnectTransfer(
	_ context.Context, _ ConnectTransfer, key string,
) (string, error) {
	f.transferKey = key
	return f.transferID, f.transferErr
}

func (f *fakePaymentGateway) CreateMerchantCustomer(
	_ context.Context, merchantID, _ string, _ string,
) (string, error) {
	f.setupMerchantID = merchantID
	return f.customerID, f.lifecycleErr
}

func (f *fakePaymentGateway) CreateMerchantSetupIntent(
	_ context.Context, merchantID, customerID, _ string,
) (string, string, error) {
	f.setupMerchantID = merchantID
	if customerID == "" {
		return "", "", errors.New("customer required")
	}
	return f.setupIntentID, f.setupClientSecret, f.lifecycleErr
}

func (f *fakePaymentGateway) SetMerchantDefaultPaymentMethod(
	context.Context, string, string,
) error {
	return f.lifecycleErr
}

func (f *fakePaymentGateway) CreateMerchantSubscription(
	_ context.Context, merchantID, customerID, paymentMethodID, priceID string,
) (string, string, error) {
	f.setupMerchantID, f.subscriptionPrice = merchantID, priceID
	if customerID == "" || paymentMethodID == "" {
		return "", "", errors.New("customer and payment method required")
	}
	return f.subscriptionID, f.subscriptionStatus, f.lifecycleErr
}

func (f *fakePaymentGateway) CreateCheckoutRefund(
	_ context.Context, refund RefundRecord,
) (string, string, error) {
	f.refundEvent = refund
	return f.refundID, f.refundStatus, f.lifecycleErr
}

func (f *fakePaymentGateway) CreateTransferReversal(
	_ context.Context, reversal TransferReversal,
) (string, error) {
	f.reversalEvent = reversal
	return f.reversalID, f.lifecycleErr
}

func paymentTestHandler(repo *fakePaymentRepository, gateway *fakePaymentGateway) http.Handler {
	return NewWithPayments(
		repo, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)), gateway, "webhook-secret",
	)
}

func TestAdminCheckoutSearchRequiresAdminRoleAndUsesBoundedFilters(t *testing.T) {
	repo := &fakePaymentRepository{adminCheckouts: []AdminCheckoutRecord{{
		ID: "00000000-0000-0000-0000-000000000001", Status: "paid",
		PaymentStatus: "succeeded",
	}}}
	handler := paymentTestHandler(repo, &fakePaymentGateway{})

	request := httptest.NewRequest(http.MethodGet, "/admin/checkouts", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if repo.adminSearch.limit != 0 {
		t.Fatal("repository was called without an admin role")
	}

	request = httptest.NewRequest(http.MethodGet,
		"/admin/checkouts?checkoutId=00000000-0000-0000-0000-000000000001&status=paid&disputeStatus=needs_response&limit=20",
		nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-1")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if repo.adminSearch.checkoutID != "00000000-0000-0000-0000-000000000001" ||
		repo.adminSearch.status != "paid" ||
		repo.adminSearch.disputeState != "needs_response" ||
		repo.adminSearch.limit != 20 {
		t.Fatalf("repository filters = %+v", repo.adminSearch)
	}
	if !strings.Contains(recorder.Body.String(), `"paymentStatus":"succeeded"`) {
		t.Fatalf("payment status missing from response: %s", recorder.Body.String())
	}
}

func TestAdminCheckoutSearchRejectsInvalidFilters(t *testing.T) {
	for _, path := range []string{
		"/admin/checkouts?checkoutId=not-a-uuid",
		"/admin/checkouts?status=unknown",
		"/admin/checkouts?disputeStatus=unknown",
		"/admin/checkouts?limit=101",
	} {
		t.Run(path, func(t *testing.T) {
			repo := &fakePaymentRepository{}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
			request.Header.Set("X-ShopStops-Actor", "admin-1")
			request.Header.Set("X-ShopStops-Actor-Role", "admin")
			recorder := httptest.NewRecorder()
			paymentTestHandler(repo, &fakePaymentGateway{}).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if repo.adminSearch.limit != 0 {
				t.Fatal("repository was called with an invalid filter")
			}
		})
	}
}

func TestCreateCheckoutPaymentUsesPersistedInvoiceAndIdempotency(t *testing.T) {
	const checkoutID = "00000000-0000-0000-0000-000000000001"
	repo := &fakePaymentRepository{checkout: CheckoutPayment{
		CheckoutID: checkoutID, Currency: "usd", AmountCents: 3120,
		Status: "quoted", IdempotencyKey: "shopstops-checkout-" + checkoutID,
	}}
	gateway := &fakePaymentGateway{createdIntent: PaymentIntent{
		ID: "pi_test", ClientSecret: "pi_test_secret", Status: "requires_payment_method",
		AmountCents: 3120, Currency: "usd",
	}}
	request := httptest.NewRequest(http.MethodPost, "/checkouts/"+checkoutID+"/payment", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "customer-1")
	recorder := httptest.NewRecorder()
	paymentTestHandler(repo, gateway).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || gateway.createKey != "shopstops-checkout-"+checkoutID ||
		gateway.createAmount != 3120 || repo.recordedIntentID != "pi_test" ||
		repo.recordedStatus != "requires_payment_method" {
		t.Fatalf("status=%d gateway=%+v repository=%+v body=%s",
			recorder.Code, gateway, repo, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"clientSecret":"pi_test_secret"`) {
		t.Fatalf("client secret missing from customer response: %s", recorder.Body.String())
	}
}

func TestCreateCheckoutPaymentRejectsOtherCustomer(t *testing.T) {
	repo := &fakePaymentRepository{
		checkout: CheckoutPayment{
			CheckoutID: "00000000-0000-0000-0000-000000000001",
			Currency:   "usd", AmountCents: 3120, Status: "quoted",
		},
	}
	request := httptest.NewRequest(
		http.MethodPost, "/checkouts/00000000-0000-0000-0000-000000000001/payment", nil,
	)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "another-customer")
	recorder := httptest.NewRecorder()
	paymentTestHandler(repo, &fakePaymentGateway{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestStripeWebhookRejectsInvalidSignature(t *testing.T) {
	gateway := &fakePaymentGateway{verifyErr: errors.New("signature mismatch")}
	request := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader("{}"))
	recorder := httptest.NewRecorder()
	paymentTestHandler(&fakePaymentRepository{}, gateway).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestStripeWebhookProcessesPaymentAndTransfersIdempotently(t *testing.T) {
	const checkoutID = "00000000-0000-0000-0000-000000000001"
	repo := &fakePaymentRepository{transfers: []ConnectTransfer{{
		CheckoutID: checkoutID, MerchantID: "merchant-1", Destination: "acct_test",
		ChargeID: "ch_test", AmountCents: 1440, Currency: "usd",
	}}}
	gateway := &fakePaymentGateway{
		event: StripePaymentEvent{
			ID: "evt_test", Type: "payment_intent.succeeded", CheckoutID: checkoutID,
			PaymentIntentID: "pi_test", AmountCents: 3120, Currency: "usd",
			PaymentStatus: "succeeded", ChargeID: "ch_test",
		},
		summary:            json.RawMessage(`{"eventId":"evt_test"}`),
		processingFeeCents: 100, transferID: "tr_test",
	}
	request := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader("{}"))
	request.Header.Set("Stripe-Signature", "verified-by-gateway")
	recorder := httptest.NewRecorder()
	paymentTestHandler(repo, gateway).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.eventFee != 100 ||
		repo.event.ID != "evt_test" || gateway.transferID != "tr_test" ||
		gateway.transferKey != "shopstops-transfer-"+checkoutID+"-merchant-1" ||
		len(repo.recordedTransfers) != 1 || repo.recordedTransfers[0] != "tr_test" {
		t.Fatalf("status=%d repository=%+v gateway=%+v body=%s",
			recorder.Code, repo, gateway, recorder.Body.String())
	}
}

func TestStripeGatewayWebhookSignatureVerification(t *testing.T) {
	payload := []byte(`{"id":"evt_test","object":"event","api_version":"` +
		stripe.APIVersion + `","created":1792390000,"data":{"object":{"id":"pi_test","object":"payment_intent","amount":3120,"amount_received":3120,"currency":"usd","status":"succeeded","client_secret":"test-client-secret","metadata":{"shopstops_checkout_id":"00000000-0000-0000-0000-000000000001"},"latest_charge":"ch_test"}},"livemode":false,"pending_webhooks":1,"type":"payment_intent.succeeded"}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload, Secret: "webhook-test-secret",
	}).Header
	gateway := NewStripeGateway("unused-test-key")
	event, summary, err := gateway.VerifyWebhook(payload, signed, "webhook-test-secret")
	if err != nil || event.PaymentIntentID != "pi_test" ||
		event.CheckoutID != "00000000-0000-0000-0000-000000000001" ||
		event.AmountCents != 3120 || event.ChargeID != "ch_test" {
		t.Fatalf("verified event=%+v err=%v", event, err)
	}
	if strings.Contains(string(summary), "client_secret") {
		t.Fatalf("sanitized event summary contains a client secret: %s", summary)
	}
	_, _, err = gateway.VerifyWebhook(payload, signed+"x", "webhook-test-secret")
	if err == nil {
		t.Fatal("modified Stripe signature was accepted")
	}
}

func TestMerchantBillingRequiresTrustedMerchantRole(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/merchant/subscription", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "00000000-0000-0000-0000-000000000001")
	recorder := httptest.NewRecorder()
	paymentTestHandler(&fakePaymentRepository{}, &fakePaymentGateway{}).
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestMerchantSetupIntentCreatesCustomerAndReturnsClientSecret(t *testing.T) {
	const merchantID = "00000000-0000-0000-0000-000000000001"
	repo := &fakePaymentRepository{billingProfile: MerchantBillingProfile{
		MerchantID: merchantID, Name: "Test Market", Status: "incomplete",
	}}
	gateway := &fakePaymentGateway{
		customerID: "cus_test", setupIntentID: "seti_test",
		setupClientSecret: "seti_test_secret",
	}
	request := httptest.NewRequest(
		http.MethodPost, "/merchant/subscription/setup-intent", nil,
	)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", merchantID)
	request.Header.Set("X-ShopStops-Actor-Role", "merchant")
	request.Header.Set("Idempotency-Key", "billing-setup-1")
	recorder := httptest.NewRecorder()
	NewWithPayments(repo, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)), gateway, "webhook-secret").
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || gateway.setupMerchantID != merchantID ||
		!strings.Contains(recorder.Body.String(), `"clientSecret":"seti_test_secret"`) {
		t.Fatalf("status=%d gateway=%+v body=%s", recorder.Code, gateway, recorder.Body.String())
	}
}

func TestMerchantSubscriptionUsesConfiguredPriceAndPersistsIncompleteUntilPaid(t *testing.T) {
	const merchantID = "00000000-0000-0000-0000-000000000001"
	repo := &fakePaymentRepository{billingProfile: MerchantBillingProfile{
		MerchantID: merchantID, CustomerID: "cus_test",
		PaymentMethodID: "pm_test", Status: "incomplete",
	}}
	gateway := &fakePaymentGateway{
		subscriptionID: "sub_test", subscriptionStatus: "active",
	}
	request := httptest.NewRequest(http.MethodPost, "/merchant/subscription", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", merchantID)
	request.Header.Set("X-ShopStops-Actor-Role", "merchant")
	recorder := httptest.NewRecorder()
	NewWithPayments(repo, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)), gateway, "webhook-secret",
		"price_merchant_199").
		ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || gateway.subscriptionPrice != "price_merchant_199" ||
		!strings.Contains(recorder.Body.String(), `"status":"incomplete"`) {
		t.Fatalf("status=%d gateway=%+v body=%s", recorder.Code, gateway, recorder.Body.String())
	}
}

func TestAdminRefundRequiresAdminRoleAndUsesIdempotencyKey(t *testing.T) {
	const checkoutID = "00000000-0000-0000-0000-000000000001"
	repo := &fakePaymentRepository{}
	gateway := &fakePaymentGateway{refundID: "re_test", refundStatus: "pending"}
	request := httptest.NewRequest(http.MethodPost,
		"/admin/checkouts/"+checkoutID+"/refund",
		strings.NewReader(`{"reason":"requested_by_customer"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	request.Header.Set("X-ShopStops-Actor", "admin-1")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
	request.Header.Set("Idempotency-Key", "refund-key-1")
	recorder := httptest.NewRecorder()
	paymentTestHandler(repo, gateway).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || repo.refund.IdempotencyKey != "refund-key-1" ||
		gateway.refundEvent.AmountCents != 3120 ||
		repo.refundRecord.StripeRefundID != "re_test" {
		t.Fatalf("status=%d repository=%+v gateway=%+v body=%s",
			recorder.Code, repo, gateway, recorder.Body.String())
	}
}

func TestRefundWebhookCreatesIdempotentTransferReversals(t *testing.T) {
	const checkoutID = "00000000-0000-0000-0000-000000000001"
	repo := &fakePaymentRepository{
		reversals: []TransferReversal{{
			ID: "reversal-row", RefundID: "refund-row", CheckoutID: checkoutID,
			MerchantID: "merchant-1", TransferID: "tr_test",
			IdempotencyKey: "refund-reversal-key", AmountCents: 1440,
		}},
	}
	gateway := &fakePaymentGateway{
		event: StripePaymentEvent{
			ID: "evt_refund", Type: "refund.updated", RefundID: "re_test",
			RefundStatus: "succeeded",
		},
		summary:    json.RawMessage(`{"eventId":"evt_refund"}`),
		reversalID: "trr_test",
	}
	request := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader("{}"))
	recorder := httptest.NewRecorder()
	paymentTestHandler(repo, gateway).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || repo.refundEvent.RefundID != "re_test" ||
		len(repo.reversalIDs) != 1 || repo.reversalIDs[0] != "trr_test" ||
		gateway.reversalEvent.IdempotencyKey != "refund-reversal-key" {
		t.Fatalf("status=%d repository=%+v gateway=%+v body=%s",
			recorder.Code, repo, gateway, recorder.Body.String())
	}
}

func TestStripeLifecycleEventParserExtractsMerchantPaymentSetup(t *testing.T) {
	event, err := parseStripeLifecycleEvent(StripePaymentEvent{
		ID: "evt_setup", Type: "setup_intent.succeeded",
	}, json.RawMessage(`{
		"id":"seti_test","customer":"cus_test","payment_method":"pm_test",
		"metadata":{"shopstops_merchant_id":"00000000-0000-0000-0000-000000000001"},
		"client_secret":"secret-not-to-store"
	}`))
	if err != nil || event.MerchantID != "00000000-0000-0000-0000-000000000001" ||
		event.CustomerID != "cus_test" || event.PaymentMethodID != "pm_test" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
}
