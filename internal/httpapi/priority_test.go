package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

// All amounts/keys here are synthetic test fixtures, never operational defaults.
func priorityTestEnv() map[string]string {
	return map[string]string{
		"SHOPSTOPS_PRIORITY_TEST_ONLY": "true", "STRIPE_SECRET_KEY": "sk_test_fixture",
		"STRIPE_WEBHOOK_SECRET": "fixture", "SHOPSTOPS_PRIORITY_SHOPPING_CENTS": "137",
		"SHOPSTOPS_PRIORITY_DELIVERY_CENTS": "263", "SHOPSTOPS_DELIVERY_FEE_CENTS": "311",
		"SHOPSTOPS_SERVICE_FEE_CENTS": "89",
	}
}

func TestPriorityConfigurationFailClosed(t *testing.T) {
	for _, flag := range []string{"", "false"} {
		c, err := PriorityConfigFromEnv(func(name string) string {
			if name == "SHOPSTOPS_PRIORITY_TEST_ONLY" {
				return flag
			}
			return "sk_live_not_used"
		})
		if err != nil || c != (PriorityConfig{}) {
			t.Fatalf("disabled: %+v %v", c, err)
		}
	}
	env := priorityTestEnv()
	c, err := PriorityConfigFromEnv(func(name string) string { return env[name] })
	if err != nil || !c.enabled || c.shoppingCents != 137 || c.deliveryCents != 263 {
		t.Fatal(c, err)
	}
	for _, name := range []string{"SHOPSTOPS_PRIORITY_TEST_ONLY", "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET",
		"SHOPSTOPS_PRIORITY_SHOPPING_CENTS", "SHOPSTOPS_PRIORITY_DELIVERY_CENTS", "SHOPSTOPS_DELIVERY_FEE_CENTS", "SHOPSTOPS_SERVICE_FEE_CENTS"} {
		for _, raw := range []string{"", "-1", "1.5", "1000001", "2147483648", " 1", "true ", "sk_live_secret", "rk_test_secret", "sk_test_"} {
			if name == "SHOPSTOPS_PRIORITY_TEST_ONLY" && raw == "" {
				continue
			}
			if name == "STRIPE_WEBHOOK_SECRET" && raw != "" {
				continue
			}
			t.Run(name+"/"+raw, func(t *testing.T) {
				e := priorityTestEnv()
				e[name] = raw
				_, err := PriorityConfigFromEnv(func(key string) string { return e[key] })
				if err == nil {
					t.Fatal("unsafe config accepted")
				}
				if name == "STRIPE_SECRET_KEY" && raw != "" && strings.Contains(err.Error(), raw) {
					t.Fatal("configuration value leaked")
				}
			})
		}
	}
	for _, name := range []string{"SHOPSTOPS_PRIORITY_SHOPPING_CENTS", "SHOPSTOPS_PRIORITY_DELIVERY_CENTS"} {
		e := priorityTestEnv()
		e[name] = "0"
		if _, err := PriorityConfigFromEnv(func(key string) string { return e[key] }); err == nil {
			t.Fatal("free opt-in accepted")
		}
	}
}

type priorityPricingRepository struct {
	fakeRepository
	config PriorityConfig
}

func (p *priorityPricingRepository) PriorityPricing() PriorityConfig { return p.config }

func TestPriorityPricingAuthenticatedAndNoDummyRates(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, role := range []string{"", "merchant", "driver", "admin", "customer"} {
			p := &priorityPricingRepository{config: PriorityConfig{enabled: enabled}}
			if enabled {
				p.config.shoppingCents = 137
				p.config.deliveryCents = 263
			}
			r := httptest.NewRequest("GET", "/checkout-pricing/priority", nil)
			r.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
			r.Header.Set("X-ShopStops-Actor", "customer-1")
			r.Header.Set("X-ShopStops-Actor-Role", role)
			w := httptest.NewRecorder()
			New(p, strings.Repeat("t", 32), nil).ServeHTTP(w, r)
			if role != "customer" {
				if w.Code != 403 {
					t.Fatal(w.Code)
				}
				continue
			}
			var body map[string]any
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			if body["available"] != enabled || body["testOnly"] != true || body["publicCheckoutAvailable"] != false || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(body)
			}
			if _, exists := body["shoppingCents"]; exists != enabled {
				t.Fatal("dummy/missing rates", body)
			}
			if enabled && body["totalCents"] != float64(400) {
				t.Fatal(body)
			}
		}
	}
	w := httptest.NewRecorder()
	New(&fakeRepository{}, strings.Repeat("t", 32), nil).ServeHTTP(w, httptest.NewRequest("GET", "/checkout-pricing/priority", nil))
	if w.Code != 401 {
		t.Fatal("anonymous pricing")
	}
}

const priorityCheckoutBody = `{"anchorStoreLocationId":"00000000-0000-0000-0000-000000000001","orderIds":["00000000-0000-0000-0000-000000000002"],"dropoff":{"latitude":38.91,"longitude":-76.99},"deliveryWindowStart":"2026-10-19T09:00:00Z","deliveryWindowEnd":"2026-10-19T11:00:00Z"}`

func TestCheckoutStrictCustomerRoleAndNoClientRates(t *testing.T) {
	for _, tc := range []struct {
		role, suffix string
		code         int
	}{
		{"", "", 403}, {"merchant", "", 403}, {"admin", "", 403}, {"driver", "", 403},
		{"customer", `,"customerRef":"victim"`, 400},
		{"customer", `,"priorityShoppingCents":1`, 400},
		{"customer", `,"additionalPickups":1`, 400},
		{"customer", `,"additionalPickupsPerCheckout":1`, 400},
		{"customer", `,"merchandiseSubtotalCents":1`, 400},
		{"customer", `,"extraPickupRateBasisPoints":25`, 400},
		{"customer", `,"extraPickupTestOnly":true`, 400},
		{"customer", `,"priorityService":true`, 409},
		{"customer", `,"priorityService":"true"`, 400},
		{"customer", `,"priorityService":null`, 400},
	} {
		p := &fakeRepository{}
		r := httptest.NewRequest("POST", "/checkouts", strings.NewReader(strings.TrimSuffix(priorityCheckoutBody, "}")+tc.suffix+"}"))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		r.Header.Set("X-ShopStops-Actor", "customer-1")
		r.Header.Set("X-ShopStops-Actor-Role", tc.role)
		r.Header.Set("Idempotency-Key", "test-choice")
		w := httptest.NewRecorder()
		New(p, strings.Repeat("t", 32), nil).ServeHTTP(w, r)
		if w.Code != tc.code || p.checkoutKey != "" {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}

type priorityPaymentGateway struct {
	fakePaymentGateway
	isTest bool
}

func (g *priorityPaymentGateway) TestOnly() bool { return g.isTest }

func TestPriorityPaymentTotalAndTestKeyGuard(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	for _, testMode := range []bool{false, true} {
		p := &fakePaymentRepository{checkout: CheckoutPayment{CheckoutID: id, Currency: "usd", Status: "quoted", AmountCents: 3520, IdempotencyKey: "stable-key", PriorityTestOnly: true}}
		g := &priorityPaymentGateway{isTest: testMode, fakePaymentGateway: fakePaymentGateway{createdIntent: PaymentIntent{ID: "pi_test", ClientSecret: "synthetic", AmountCents: 3520, Currency: "usd", Status: "requires_payment_method"}}}
		r := httptest.NewRequest("POST", "/checkouts/"+id+"/payment", nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		r.Header.Set("X-ShopStops-Actor", "customer-1")
		r.Header.Set("X-ShopStops-Actor-Role", "customer")
		w := httptest.NewRecorder()
		NewWithPayments(p, strings.Repeat("t", 32), slog.New(slog.NewTextHandler(io.Discard, nil)), g, "fixture").ServeHTTP(w, r)
		if testMode {
			if w.Code != 200 || g.createAmount != 3520 || !strings.Contains(w.Body.String(), `"testOnly":true`) {
				t.Fatal(w.Code, w.Body.String())
			}
		} else if w.Code != 503 || g.createAmount != 0 {
			t.Fatal("live/unknown gateway used", w.Code)
		}
	}
	if !NewStripeGateway("sk_test_fixture").TestOnly() || NewStripeGateway("sk_live_fixture").TestOnly() || NewStripeGateway("sk_test_").TestOnly() {
		t.Fatal("key mode detection")
	}
}
