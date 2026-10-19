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

func paymentTestHandler(repo *fakePaymentRepository, gateway *fakePaymentGateway) http.Handler {
	return NewWithPayments(
		repo, strings.Repeat("t", 32),
		slog.New(slog.NewTextHandler(io.Discard, nil)), gateway, "webhook-secret",
	)
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
