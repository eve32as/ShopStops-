package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testID = "11111111-1111-4111-8111-111111111111"

type fakeVerifier struct {
	id    identity
	err   error
	calls atomic.Int32
}

func (v *fakeVerifier) verify(ctx context.Context, raw string) (identity, error) {
	v.calls.Add(1)
	if raw != "test-token" || ctx.Err() != nil {
		return identity{}, errors.New("invalid test token")
	}
	return v.id, v.err
}

func testConfig(backend string) Config {
	return Config{Issuer: "https://issuer.example", Audience: "shopstops-api", BackendURL: backend,
		InternalToken: strings.Repeat("t", 32), SigningAlgs: []string{"RS256"}}
}

func TestForwardingRebuildsHeaders(t *testing.T) {
	for _, tc := range []struct{ name, method, path, role string }{
		{"catalog", "GET", "/v1/stores/" + testID + "/catalog", "customer"},
		{"priority pricing", "GET", "/v1/checkout-pricing/priority", "customer"},
		{"nearby", "GET", "/stores/" + testID + "/nearby-merchants?radiusMeters=50", "customer"},
		{"order", "POST", "/v1/orders", "customer"},
		{"merchant", "POST", "/merchant/catalog", "merchant"},
		{"admin", "POST", "/admin/checkouts/" + testID + "/refund", "admin"},
		{"finance scenario", "POST", "/v1/admin/finance/scenarios", "admin"},
		{"finance cost", "POST", "/v1/admin/finance/costs", "admin"},
		{"finance report", "GET", "/v1/admin/finance/costs?period=2026-11&limit=50", "admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) ||
					r.Header.Get("X-ShopStops-Actor") != testID || r.Header.Get("X-ShopStops-Actor-Role") != tc.role {
					t.Error("verified identity/service credential missing")
				}
				for _, key := range []string{"X-ShopStops-Internal-Token", "X-Forwarded-For", "Forwarded", "Cookie", "Proxy-Authorization", "Connection", "Stripe-Signature"} {
					if r.Header.Get(key) != "" {
						t.Errorf("untrusted %s forwarded", key)
					}
				}
				if r.Header.Get("Idempotency-Key") != "order-key" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("required safe headers missing")
				}
				if strings.HasPrefix(r.URL.Path, "/v1") {
					t.Error("prefix not removed")
				}
				if tc.name == "nearby" && r.URL.RawQuery != "radiusMeters=50" {
					t.Error("query lost")
				}
				w.Header().Set("Authorization", "Bearer secret")
				w.Header().Set("X-ShopStops-Actor", "secret")
				w.Header().Set("Set-Cookie", "credential=secret")
				w.Header().Set("Location", "https://private.internal")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer backend.Close()
			v := &fakeVerifier{id: identity{actor: testID, role: tc.role}}
			h := newHandler(testConfig(backend.URL), v)
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("X-ShopStops-Actor", "victim")
			req.Header.Set("X-ShopStops-Actor-Role", "admin")
			for _, key := range []string{"X-ShopStops-Internal-Token", "X-Forwarded-For", "Forwarded", "Cookie", "Proxy-Authorization", "Connection", "Stripe-Signature"} {
				req.Header.Set(key, "forged")
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "order-key")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			for _, key := range []string{"Authorization", "X-ShopStops-Actor", "Set-Cookie", "Location"} {
				if w.Header().Get(key) != "" {
					t.Errorf("response header %s leaked", key)
				}
			}
		})
	}
}

func TestPublicForwardingHasNoCredentials(t *testing.T) {
	for _, path := range []string{"/healthz", "/v1/healthz", "/webhooks/stripe", "/v1/webhooks/stripe"} {
		t.Run(path, func(t *testing.T) {
			webhook := strings.HasSuffix(path, "/stripe")
			payload := ""
			method := "GET"
			if webhook {
				method, payload = "POST", `{"event":"synthetic"}`
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for key := range r.Header {
					if strings.HasPrefix(strings.ToLower(key), "x-shopstops-") || strings.EqualFold(key, "Authorization") {
						t.Error("public credential/identity forwarded")
					}
				}
				data, _ := io.ReadAll(r.Body)
				if string(data) != payload {
					t.Error("webhook bytes changed")
				}
				if webhook && r.Header.Get("Stripe-Signature") != "synthetic-signature" {
					t.Error("signature lost")
				}
				w.WriteHeader(200)
			}))
			defer backend.Close()
			v := &fakeVerifier{err: errors.New("must not verify public requests")}
			h := newHandler(testConfig(backend.URL), v)
			r := httptest.NewRequest(method, path, strings.NewReader(payload))
			r.Header.Set("Authorization", "Bearer forged")
			r.Header.Set("X-ShopStops-Actor", "forged")
			r.Header.Set("X-ShopStops-Actor-Role", "admin")
			r.Header.Set("X-ShopStops-Internal-Token", "forged")
			r.Header.Set("Stripe-Signature", "synthetic-signature")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || v.calls.Load() != 0 {
				t.Fatalf("public forwarding status=%d verifier calls=%d", w.Code, v.calls.Load())
			}
		})
	}
}

func TestFailClosedRoutesAndTokens(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer backend.Close()
	for _, tc := range []struct {
		name, method, path, auth, role string
		code                           int
		duplicate                      bool
	}{
		{"missing", "GET", "/stores/" + testID + "/catalog", "", "customer", 401, false},
		{"bad", "GET", "/stores/" + testID + "/catalog", "Bearer forged", "customer", 401, false},
		{"duplicate", "POST", "/orders", "Bearer test-token", "customer", 401, true},
		{"wrong scheme", "POST", "/orders", "Basic test-token", "customer", 401, false},
		{"escalation", "POST", "/admin/checkouts/" + testID + "/refund", "Bearer test-token", "customer", 403, false},
		{"merchant escalation", "GET", "/merchant/catalog", "Bearer test-token", "customer", 403, false},
		{"customer role separation", "POST", "/orders", "Bearer test-token", "merchant", 403, false},
		{"priority role separation", "GET", "/checkout-pricing/priority", "Bearer test-token", "merchant", 403, false},
		{"checkout", "POST", "/checkouts", "Bearer test-token", "customer", 403, false},
		{"payment", "POST", "/checkouts/" + testID + "/payment", "Bearer test-token", "customer", 403, false},
		{"driver", "GET", "/driver/routes/" + testID, "Bearer test-token", "driver", 403, false},
		{"driver location", "POST", "/driver/routes/" + testID + "/location", "Bearer test-token", "driver", 403, false},
		{"driver event", "POST", "/driver/routes/" + testID + "/stops/" + testID + "/events", "Bearer test-token", "driver", 403, false},
		{"legacy driver bundle", "GET", "/driver/bundles/" + testID, "Bearer test-token", "driver", 403, false},
		{"legacy driver event", "POST", "/driver/bundles/" + testID + "/stops/" + testID + "/events", "Bearer test-token", "driver", 403, false},
		{"admin dispatch", "POST", "/dispatch/bundles", "Bearer test-token", "admin", 403, false},
		{"unknown", "GET", "/admin/secrets", "Bearer test-token", "admin", 404, false},
		{"wrong method", "DELETE", "/orders", "Bearer test-token", "admin", 404, false},
		{"implicit head", "HEAD", "/healthz", "", "customer", 404, false},
		{"trailing slash", "GET", "/healthz/", "", "customer", 404, false},
		{"encoded slash", "GET", "/v1%2fhealthz", "", "customer", 400, false},
		{"double escape", "GET", "/v1/%252e%252e/healthz", "", "customer", 400, false},
		{"dot", "GET", "/v1/../healthz", "", "customer", 400, false},
		{"double slash", "GET", "/v1//healthz", "", "customer", 400, false},
		{"encoded uuid", "GET", "/stores/%31" + testID[1:] + "/catalog", "Bearer test-token", "customer", 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: tc.role}})
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			if tc.duplicate {
				r.Header.Add("Authorization", "Bearer test-token")
			}
			r.Header.Set("X-ShopStops-Actor-Role", "admin")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("got %d want %d", w.Code, tc.code)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("denied request reached backend")
	}
}

func TestPublicReadinessSanitizesFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Debug", "private details")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "private database details")
	}))
	defer backend.Close()
	w := httptest.NewRecorder()
	newHandler(testConfig(backend.URL), nil).ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("X-Backend-Debug") != "" || strings.Contains(w.Body.String(), "private") {
		t.Fatal("readiness status lost or private backend detail disclosed")
	}
}

func TestCORS(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer backend.Close()
	c := testConfig(backend.URL)
	c.AllowedOrigins = []string{"https://admin.example"}
	h := newHandler(c, &fakeVerifier{id: identity{actor: testID, role: "customer"}})
	for _, tc := range []struct {
		method, origin, headers string
		code                    int
	}{
		{"GET", "https://evil.example", "", 403},
		{"GET", "null", "", 403},
		{"OPTIONS", "https://admin.example", "authorization,content-type,idempotency-key", 204},
		{"OPTIONS", "https://admin.example", "x-shopstops-actor", 403},
		{"OPTIONS", "", "", 403},
		{"GET", "https://admin.example", "", 200},
	} {
		r := httptest.NewRequest(tc.method, "/v1/stores/"+testID+"/catalog", nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		r.Header.Set("Authorization", "Bearer test-token")
		r.Header.Set("Access-Control-Request-Method", "GET")
		r.Header.Set("Access-Control-Request-Headers", tc.headers)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Errorf("%s %s got %d", tc.method, tc.origin, w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") == "*" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Error("unsafe CORS")
		}
		if tc.origin == "https://admin.example" && w.Header().Get("Access-Control-Allow-Origin") != tc.origin {
			t.Error("allowed origin missing")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("preflight/denied origin forwarded: %d", calls.Load())
	}
	c.AllowedOrigins = nil
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Header.Set("Origin", "https://admin.example")
	w := httptest.NewRecorder()
	newHandler(c, nil).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CORS default not closed")
	}
}

func TestLimitsRedirectsAndResponseLeaks(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		code           int
	}{
		{"redirect", "", 302, 502},
		{"token leak", strings.Repeat("t", 32), 200, 502},
		{"JSON escaped token leak", `{"credential":"` + strings.Repeat(`\u0074`, 32) + `"}`, 200, 502},
		{"bearer leak", "test-token", 200, 502},
		{"JSON escaped bearer leak", `{"credential":"t\u0065st-token"}`, 200, 502},
		{"oversized response", strings.Repeat("x", maxResponseBytes+1), 200, 502},
		{"upstream error", "private database host credentials", 500, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
			defer target.Close()
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.response)
			}))
			defer backend.Close()
			h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: "customer"}})
			r := httptest.NewRequest("POST", "/orders", nil)
			r.Header.Set("Authorization", "Bearer test-token")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code || redirected.Load() != 0 {
				t.Fatalf("status %d redirects %d", w.Code, redirected.Load())
			}
			if w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "test-token") || strings.Contains(w.Body.String(), strings.Repeat("t", 32)) {
				t.Fatal("credential leaked")
			}
		})
	}
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer backend.Close()
	h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: "customer"}})
	for _, tc := range []struct {
		method, path string
		size         int
	}{{"POST", "/orders", 32*1024 + 1}, {"GET", "/healthz", 1}, {"POST", "/webhooks/stripe", 1024*1024 + 1}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(strings.Repeat("x", tc.size)))
		r.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 413 {
			t.Errorf("oversize %s: %d", tc.path, w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("oversize body forwarded")
	}
}

func TestUpstreamUsesRequestContext(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer backend.Close()
	h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: "customer"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/orders", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer test-token")
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream context not cancelled")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway did not finish")
	}
}

func TestClaimsContract(t *testing.T) {
	claims := func(role, actor string) map[string]json.RawMessage {
		result := map[string]json.RawMessage{RoleClaim: json.RawMessage(`"` + role + `"`)}
		if actor != "" {
			result[ActorClaim] = json.RawMessage(`"` + actor + `"`)
		}
		return result
	}
	id, err := claimsIdentity("customer-sub", claims("admin", "victim"), false)
	if err != nil || id.role != "customer" || id.actor != "customer-sub" {
		t.Fatal("default claims elevated privilege")
	}
	id, err = claimsIdentity("customer-sub", claims("customer", "victim"), true)
	if err != nil || id.actor != "customer-sub" {
		t.Fatal("customer actor override allowed")
	}
	for _, role := range []string{"admin", "merchant", "driver"} {
		id, err := claimsIdentity("subject", claims(role, testID), true)
		if err != nil || id.role != role || id.actor != testID {
			t.Fatalf("trusted %s rejected", role)
		}
	}
	for _, c := range []map[string]json.RawMessage{claims("merchant", ""), claims("merchant", "not-uuid"), claims("root", testID), {RoleClaim: json.RawMessage(`["admin"]`)}, {RoleClaim: json.RawMessage(`null`)}, claims("admin", "bad actor")} {
		if _, err := claimsIdentity("subject", c, true); err == nil {
			t.Error("invalid trusted identity accepted")
		}
	}
}

func TestConfigFailsClosed(t *testing.T) {
	valid := testConfig("http://api.internal:8080")
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Issuer = "http://issuer.example" },
		func(c *Config) { c.Issuer = "http://127.0.0.1" },
		func(c *Config) { c.Issuer = "https://user:password@issuer.example" },
		func(c *Config) { c.Audience = "" },
		func(c *Config) { c.InternalToken = strings.Repeat("x", 31) },
		func(c *Config) { c.InternalToken = strings.Repeat("x", 32) + "\n" },
		func(c *Config) { c.BackendURL = "https://public.example" },
		func(c *Config) { c.BackendURL = "http://8.8.8.8" },
		func(c *Config) { c.BackendURL = "http://api.internal:65536" },
		func(c *Config) { c.BackendURL = "http://*.internal" },
		func(c *Config) { c.BackendURL = "http://169.254.169.254" },
		func(c *Config) { c.BackendURL = "http://api.internal/path" },
		func(c *Config) { c.BackendURL = "http://user:pass@api.internal" },
		func(c *Config) { c.SigningAlgs = []string{"none"} },
		func(c *Config) { c.SigningAlgs = []string{"HS256"} },
		func(c *Config) { c.SigningAlgs = nil },
		func(c *Config) { c.AllowedOrigins = []string{"*"} },
		func(c *Config) { c.AllowedOrigins = []string{"https://*.example"} },
		func(c *Config) { c.AllowedOrigins = []string{"http://public.example"} },
		func(c *Config) { c.AllowedOrigins = []string{"https://admin.example/"} },
	} {
		c := valid
		change(&c)
		if _, err := New(context.Background(), c); err == nil {
			t.Error("invalid configuration accepted")
		}
	}
	if _, err := privateDial(context.Background(), "tcp", "8.8.8.8:443"); err == nil {
		t.Fatal("public dial accepted")
	}
}
