package identitygateway

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	testIssuer   = "https://identity.example.test"
	testAudience = "shopstops-api"
)

var testToken = strings.Repeat("t", 32)

func TestGatewayVerifiesTokenAndReplacesUntrustedHeaders(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := oidc.NewVerifier(testIssuer, &oidc.StaticKeySet{
		PublicKeys: []crypto.PublicKey{&key.PublicKey},
	}, &oidc.Config{
		ClientID: testAudience, SupportedSigningAlgs: []string{"RS256"},
	})

	var received http.Header
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		receivedPath = r.URL.RequestURI()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewWithActorClaim(
		verifier, upstreamURL, testToken, "roles", "shopstops_actor_id",
	)
	if err != nil {
		t.Fatal(err)
	}

	token := signedToken(t, key, map[string]any{
		"iss": testIssuer, "sub": "oidc-user-123",
		"shopstops_actor_id": "customer-123", "aud": testAudience,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"roles": []string{"customer", "merchant"},
	})
	request := httptest.NewRequest(http.MethodGet,
		"/v1/orders/00000000-0000-0000-0000-000000000001/events?waitSeconds=0", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-ShopStops-Actor", "attacker")
	request.Header.Set("X-ShopStops-Actor-Role", "admin")
	request.Header.Set("X-Forwarded-For", "203.0.113.10")
	request.Header.Set("Cookie", "session=secret")
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if received.Get("Authorization") != "Bearer "+testToken ||
		received.Get("X-ShopStops-Actor") != "customer-123" ||
		received.Get("X-ShopStops-Actor-Role") != "customer" ||
		received.Get("X-Forwarded-For") != "" || received.Get("Cookie") != "" {
		t.Fatalf("upstream received unsafe headers: %+v", received)
	}
	if receivedPath != "/orders/00000000-0000-0000-0000-000000000001/events?waitSeconds=0" {
		t.Fatalf("upstream path = %q", receivedPath)
	}
}

func TestAdminDispatchWebSocketProtocolTokenIsVerifiedAndStripped(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := oidc.NewVerifier(testIssuer, &oidc.StaticKeySet{
		PublicKeys: []crypto.PublicKey{&key.PublicKey},
	}, &oidc.Config{
		ClientID: testAudience, SupportedSigningAlgs: []string{"RS256"},
	})
	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	gateway, err := NewWithActorClaim(
		verifier, upstreamURL, testToken, "roles", "sub",
	)
	if err != nil {
		t.Fatal(err)
	}
	token := signedToken(t, key, map[string]any{
		"iss": testIssuer, "sub": "admin-123", "aud": testAudience,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"roles": []string{"admin"},
	})
	encodedToken := base64.RawURLEncoding.EncodeToString([]byte(token))
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/dispatch/stream", nil)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Protocol", "shopstops-admin-v1, bearer."+encodedToken)
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent ||
		received.Get("Authorization") != "Bearer "+testToken ||
		received.Get("X-ShopStops-Actor") != "admin-123" ||
		received.Get("X-ShopStops-Actor-Role") != "admin" ||
		received.Get("Sec-WebSocket-Protocol") != "shopstops-admin-v1" {
		t.Fatalf("status=%d upstream headers=%+v body=%s",
			recorder.Code, received, recorder.Body.String())
	}
	if strings.Contains(received.Get("Sec-WebSocket-Protocol"), token) {
		t.Fatal("OIDC token was forwarded to the API")
	}
}

func TestGatewayRejectsInvalidTokensAndMissingRoles(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := oidc.NewVerifier(testIssuer, &oidc.StaticKeySet{
		PublicKeys: []crypto.PublicKey{&key.PublicKey},
	}, &oidc.Config{
		ClientID: testAudience, SupportedSigningAlgs: []string{"RS256"},
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthorized request reached upstream")
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	gateway, err := New(verifier, upstreamURL, testToken, "roles")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/admin/checkouts", nil)
	request.Header.Set("Authorization", "******")
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	token := signedToken(t, key, map[string]any{
		"iss": testIssuer, "sub": "customer-123", "aud": testAudience,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"roles": []string{"customer"},
	})
	request = httptest.NewRequest(http.MethodGet, "/v1/admin/checkouts", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	gateway.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("role mismatch status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestGatewayHealthAndStripeWebhookArePublicButUnknownRoutesAreHidden(t *testing.T) {
	var paths []string
	var forwardedActors []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		forwardedActors = append(forwardedActors, r.Header.Get("X-ShopStops-Actor"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	gateway, err := New(oidc.NewVerifier(testIssuer, &oidc.StaticKeySet{}, &oidc.Config{
		ClientID: testAudience,
	}), upstreamURL, testToken, "roles")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/healthz"},
		{http.MethodGet, "/v1/readyz"},
		{http.MethodPost, "/v1/webhooks/stripe"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(target.method, target.path, nil)
		request.Header.Set("X-ShopStops-Actor", "spoofed")
		request.Header.Set("X-ShopStops-Actor-Role", "admin")
		gateway.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s %s status = %d", target.method, target.path, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/private", nil))
	if recorder.Code != http.StatusNotFound || len(paths) != 3 ||
		forwardedActors[0] != "" || forwardedActors[1] != "" || forwardedActors[2] != "" {
		t.Fatalf("unknown route status=%d upstream paths=%v forwarded actors=%v",
			recorder.Code, paths, forwardedActors)
	}
}

func TestGatewayCORSUsesExactConfiguredOrigins(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	gateway, err := New(oidc.NewVerifier(testIssuer, &oidc.StaticKeySet{}, &oidc.Config{
		ClientID: testAudience,
	}), upstreamURL, testToken, "roles", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodOptions, "/v1/admin/checkouts", nil)
	request.Header.Set("Origin", "https://admin.example.test")
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent ||
		recorder.Header().Get("Access-Control-Allow-Origin") != "https://admin.example.test" {
		t.Fatalf("allowed origin response = %d, %+v", recorder.Code, recorder.Header())
	}

	request = httptest.NewRequest(http.MethodOptions, "/v1/admin/checkouts", nil)
	request.Header.Set("Origin", "https://attacker.example.test")
	recorder = httptest.NewRecorder()
	gateway.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unlisted origin status = %d", recorder.Code)
	}
}

func TestRequiredRolesAreScopedByRoute(t *testing.T) {
	cases := []struct {
		method  string
		path    string
		role    string
		ok      bool
		allowed bool
	}{
		{http.MethodGet, "/healthz", "customer", true, false},
		{http.MethodGet, "/readyz", "customer", true, false},
		{http.MethodGet, "/stores/00000000-0000-0000-0000-000000000001/catalog", "customer", true, true},
		{http.MethodPost, "/dispatch/bundles", "dispatch", true, true},
		{http.MethodPost, "/dispatch/bundles", "admin", true, true},
		{http.MethodPost, "/dispatch/routes", "dispatch", true, true},
		{http.MethodPost, "/dispatch/routes", "admin", true, true},
		{http.MethodPost, "/dispatch/routes", "driver", true, false},
		{http.MethodGet, "/driver/routes", "driver", true, true},
		{http.MethodGet, "/admin/checkouts", "admin", true, true},
		{http.MethodGet, "/admin/checkouts", "customer", true, false},
		{http.MethodGet, "/admin/operations", "admin", true, true},
		{http.MethodGet, "/admin/operations", "dispatch", true, false},
		{http.MethodGet, "/admin/delivery-zones", "admin", true, true},
		{http.MethodGet, "/admin/delivery-zones", "dispatch", true, false},
		{http.MethodPatch, "/admin/delivery-zones/00000000-0000-0000-0000-000000000001", "admin", true, true},
		{http.MethodPatch, "/admin/delivery-zones/00000000-0000-0000-0000-000000000001/dispatch-limits", "admin", true, true},
		{http.MethodPatch, "/admin/delivery-zones/00000000-0000-0000-0000-000000000001/dispatch-limits", "dispatch", true, false},
		{http.MethodDelete, "/merchant/catalog", "merchant", false, false},
		{http.MethodGet, "/v1/unknown", "admin", false, false},
	}
	for _, testCase := range cases {
		roles, ok := requiredRoles(testCase.method, testCase.path)
		if ok != testCase.ok || (ok && contains(roles, testCase.role) != testCase.allowed) {
			t.Errorf("%s %s roles=%v ok=%v, expected role=%s allowed=%v route=%v",
				testCase.method, testCase.path, roles, ok,
				testCase.role, testCase.allowed, testCase.ok)
		}
	}
}

func TestActorClaimMustBeSafeForTrustedHeaders(t *testing.T) {
	for _, actor := range []string{"", " actor", "actor\nadmin", strings.Repeat("a", 201)} {
		if validActor(actor) {
			t.Errorf("actor %q unexpectedly accepted", actor)
		}
	}
	for _, actor := range []string{"customer-123", "user@example.test"} {
		if !validActor(actor) {
			t.Errorf("actor %q unexpectedly rejected", actor)
		}
	}
}

func signedToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s.%s", unsigned, base64.RawURLEncoding.EncodeToString(signature))
}
