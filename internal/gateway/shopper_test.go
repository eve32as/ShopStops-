package gateway

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "sync/atomic"
    "testing"
)

func TestShopperExactAdminGateway(t *testing.T) {
    var calls atomic.Int32
    backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls.Add(1)
        if r.Header.Get("X-ShopStops-Actor") != testID || r.Header.Get("X-ShopStops-Actor-Role") != "admin" {
            t.Error("untrusted actor forwarded")
        }
        w.WriteHeader(200)
    }))
    defer backend.Close()
    for _, op := range []struct {
        method, path string
        limit        int
    }{{"POST", "/admin/shopper/tasks", 8192}, {"GET", "/admin/shopper/tasks", 0}, {"POST", "/admin/shopper/tasks/" + testID + "/complete", 8192}} {
        for _, role := range []string{"customer", "merchant", "driver", "admin"} {
            h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: role}})
            r := httptest.NewRequest(op.method, "/v1"+op.path, nil)
            r.Header.Set("Authorization", "Bearer test-token")
            r.Header.Set("X-ShopStops-Actor", "spoof")
            r.Header.Set("X-ShopStops-Actor-Role", "admin")
            w := httptest.NewRecorder()
            h.ServeHTTP(w, r)
            want := 403
            if role == "admin" {
                want = 200
            }
            if w.Code != want {
                t.Fatal(op.path, role, w.Code)
            }
        }
        h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: "admin"}})
        r := httptest.NewRequest(op.method, op.path, strings.NewReader(strings.Repeat("x", op.limit+1)))
        r.Header.Set("Authorization", "Bearer test-token")
        w := httptest.NewRecorder()
        h.ServeHTTP(w, r)
        if w.Code != 413 {
            t.Fatal(op.path, w.Code)
        }
    }
    for _, path := range []string{"/admin/shopper/tasks/", "/admin/shopper/tasks/not-uuid/complete", "/admin/shopper/tasks/" + testID + "/approve", "/admin/shopper/tasks/" + testID + "/payout", "/shopper/tasks", "/admin/shopper/earnings"} {
        h := newHandler(testConfig(backend.URL), &fakeVerifier{id: identity{actor: testID, role: "admin"}})
        r := httptest.NewRequest("POST", path, nil)
        r.Header.Set("Authorization", "Bearer test-token")
        w := httptest.NewRecorder()
        h.ServeHTTP(w, r)
        if w.Code != 404 {
            t.Fatal(path, w.Code)
        }
    }
    if calls.Load() != 3 {
        t.Fatal("denied request forwarded", calls.Load())
    }
}