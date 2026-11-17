package httpapi

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtraPickupConfigFailClosedAndIndependent(t *testing.T) {
	for _, flag := range []string{"", "false", "true", "TRUE", "1", " true", "true ", "False"} {
		for _, key := range []string{"", "sk_test_fixture", "sk_live_fixture", "rk_test_fixture", "sk_test_", "sk_test_fixture "} {
			for _, secret := range []string{"", " ", "whsec_fixture"} {
				e := map[string]string{"SHOPSTOPS_EXTRA_PICKUP_TEST_ONLY": flag, "STRIPE_SECRET_KEY": key, "STRIPE_WEBHOOK_SECRET": secret}
				c, err := ExtraPickupConfigFromEnv(func(k string) string { return e[k] })
				disabled := flag == "" || flag == "false"
				valid := disabled || (flag == "true" && key == "sk_test_fixture" && secret == "whsec_fixture")
				if (err == nil) != valid || c.enabled != (valid && !disabled) {
					t.Fatal(flag, "unexpected config result")
				}
				if err != nil && key != "" && strings.Contains(err.Error(), key) {
					t.Fatal("key leaked")
				}
			}
		}
	}
	p := NewPostgresRepository(nil, 0, 0).WithExtraPickupPricing(ExtraPickupConfig{enabled: true})
	if !p.extraPickup.enabled || p.priority.enabled {
		t.Fatal("extra pickups require priority")
	}
}

func TestExtraPickupPaymentGuardIncludingRetry(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	for _, mode := range []string{"live", "test", "unknown"} {
		for _, retry := range []bool{false, true} {
			p := &fakePaymentRepository{checkout: CheckoutPayment{CheckoutID: id, Currency: "usd", Status: "quoted", AmountCents: 5413, IdempotencyKey: "stable", ExtraPickupTestOnly: true}}
			intent := PaymentIntent{ID: "pi_fixture", ClientSecret: "synthetic", AmountCents: 5413, Currency: "usd", Status: "requires_payment_method"}
			g := &priorityPaymentGateway{isTest: mode == "test", fakePaymentGateway: fakePaymentGateway{createdIntent: intent, retrievedIntent: intent}}
			if retry {
				p.checkout.PaymentIntentID = intent.ID
			}
			var gateway PaymentGateway = g
			if mode == "unknown" {
				gateway = &g.fakePaymentGateway
			}
			r := httptest.NewRequest("POST", "/checkouts/"+id+"/payment", nil)
			r.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
			r.Header.Set("X-ShopStops-Actor", "customer-1")
			r.Header.Set("X-ShopStops-Actor-Role", "customer")
			w := httptest.NewRecorder()
			NewWithPayments(p, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)), gateway, "fixture").ServeHTTP(w, r)
			if mode == "test" {
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"testOnly":true`) || (!retry && g.createAmount != 5413) {
					t.Fatal(mode, retry, w.Code, w.Body)
				}
			} else if w.Code != 503 || g.createAmount != 0 {
				t.Fatal("unsafe gateway accepted", mode, retry, w.Code)
			}
		}
	}
}
