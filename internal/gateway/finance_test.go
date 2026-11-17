package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFinanceExactAdminPoliciesAndLimits(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer backend.Close()
	for _, op := range []struct {
		method, path string
		limit        int
	}{{"POST", "/admin/finance/scenarios", 8192}, {"POST", "/admin/finance/costs", 8192}, {"GET", "/admin/finance/costs", 0}} {
		for _, role := range []string{"customer", "merchant", "driver", "admin"} {
			t.Run(op.method+op.path+role, func(t *testing.T) {
				h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: role}})
				r := httptest.NewRequest(op.method, "/v1"+op.path, nil)
				r.Header.Set("Authorization", "Bearer test-token")
				r.Header.Set("X-ShopStops-Actor-Role", "admin")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				want := 403
				if role == "admin" {
					want = 200
				}
				if w.Code != want {
					t.Fatal(w.Code)
				}
			})
		}
		h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: "admin"}})
		r := httptest.NewRequest(op.method, op.path, strings.NewReader(strings.Repeat("x", op.limit+1)))
		r.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 413 {
			t.Fatalf("body cap %s %d", op.path, w.Code)
		}
	}
	for _, path := range []string{"/admin/finance", "/admin/finance/scenarios/", "/admin/finance/costs/" + testID, "/admin/finance/payouts", "/admin/finance/costs/approve"} {
		h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: "admin"}})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", path, nil)
		r.Header.Set("Authorization", "Bearer test-token")
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal(w.Code, path)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("denied request forwarded: %d", calls.Load())
	}
}
