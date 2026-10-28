package identitygateway

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type capturedRequest struct {
	authorization string
	actor         string
	role          string
	roleSelection string
	cookie        string
	forwardedFor  string
	stripeHeader  string
}

func TestGatewayVerifiesTokensAndForwardsVerifiedIdentity(t *testing.T) {
	harness := newGatewayHarness(t)
	token := harness.sign(map[string]any{
		"sub": "provider|user-42", "aud": harness.audience,
		"roles": []string{"shop-admin"},
	})
	request := httptest.NewRequest(http.MethodGet, "/admin/drivers", nil)
	request.RemoteAddr = "203.0.113.17:4321"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(actorHeader, "forged-actor")
	request.Header.Set(actorRoleHeader, "admin")
	request.Header.Set(roleSelectionHeader, "admin")
	request.Header.Set("Cookie", "session=client-session")
	request.Header.Set("X-Forwarded-For", "192.0.2.1")
	request.Header.Set("X-Forwarded-Client-Cert", "forged-cert")

	response := httptest.NewRecorder()
	harness.gateway.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	got := <-harness.captured
	if got.authorization != "Bearer "+harness.internalToken {
		t.Errorf("forwarded authorization = %q", got.authorization)
	}
	if got.actor != "provider|user-42" || got.role != "admin" {
		t.Errorf("forwarded identity = actor %q, role %q", got.actor, got.role)
	}
	if got.roleSelection != "" || got.cookie != "" {
		t.Errorf("untrusted session headers were forwarded: %+v", got)
	}
	if got.forwardedFor != "203.0.113.17" {
		t.Errorf("forwarded client address = %q", got.forwardedFor)
	}
}

func TestGatewayRejectsInvalidTokensAndRoles(t *testing.T) {
	harness := newGatewayHarness(t)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	validClaims := func() map[string]any {
		return map[string]any{
			"iss": harness.issuer.URL, "sub": "user-42", "aud": harness.audience,
			"exp": time.Now().Add(5 * time.Minute).Unix(),
			"iat": time.Now().Unix(), "roles": "shop-admin",
		}
	}
	tests := []struct {
		name       string
		claims     map[string]any
		key        *rsa.PrivateKey
		selection  string
		wantStatus int
	}{
		{name: "bad signature", claims: validClaims(), key: otherKey, wantStatus: http.StatusUnauthorized},
		{
			name: "wrong issuer", claims: func() map[string]any {
				claims := validClaims()
				claims["iss"] = "https://attacker.example"
				return claims
			}(), wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong audience", claims: func() map[string]any {
				claims := validClaims()
				claims["aud"] = "another-client"
				return claims
			}(), wantStatus: http.StatusUnauthorized,
		},
		{
			name: "expired", claims: func() map[string]any {
				claims := validClaims()
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
				return claims
			}(), wantStatus: http.StatusUnauthorized,
		},
		{
			name: "missing role claim", claims: func() map[string]any {
				claims := validClaims()
				delete(claims, "roles")
				return claims
			}(), wantStatus: http.StatusForbidden,
		},
		{
			name: "unknown role", claims: func() map[string]any {
				claims := validClaims()
				claims["roles"] = "unmapped-role"
				return claims
			}(), wantStatus: http.StatusForbidden,
		},
		{
			name: "ambiguous roles", claims: func() map[string]any {
				claims := validClaims()
				claims["roles"] = []string{"shop-admin", "shop-driver"}
				return claims
			}(), wantStatus: http.StatusForbidden,
		},
		{
			name: "selected role not granted", claims: func() map[string]any {
				claims := validClaims()
				claims["roles"] = []string{"shop-admin", "shop-driver"}
				return claims
			}(), selection: "customer", wantStatus: http.StatusForbidden,
		},
		{
			name: "non-string role", claims: func() map[string]any {
				claims := validClaims()
				claims["roles"] = []any{"shop-admin", 42}
				return claims
			}(), wantStatus: http.StatusForbidden,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := test.key
			if key == nil {
				key = harness.key
			}
			token := signToken(t, key, harness.keyID, test.claims)
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			if test.selection != "" {
				request.Header.Set(roleSelectionHeader, test.selection)
			}
			response := httptest.NewRecorder()

			harness.gateway.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
		})
	}
}

func TestGatewayRequiresAuthorizedRoleSelection(t *testing.T) {
	harness := newGatewayHarness(t)
	token := harness.sign(map[string]any{
		"sub": "user-42", "aud": harness.audience,
		"roles": []string{"shop-admin", "shop-driver"},
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(roleSelectionHeader, "driver")
	response := httptest.NewRecorder()

	harness.gateway.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if got := (<-harness.captured).role; got != "driver" {
		t.Errorf("forwarded selected role = %q, want driver", got)
	}
}

func TestGatewayPublicEndpointsStripClientIdentity(t *testing.T) {
	harness := newGatewayHarness(t)
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/healthz"},
		{method: http.MethodPost, path: "/webhooks/stripe"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader("{}"))
		request.Header.Set("Authorization", "******")
		request.Header.Set(actorHeader, "forged-actor")
		request.Header.Set(actorRoleHeader, "admin")
		request.Header.Set("Stripe-Signature", "verified-by-upstream")
		response := httptest.NewRecorder()

		harness.gateway.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, want %d", test.method, test.path, response.Code, http.StatusOK)
		}
		got := <-harness.captured
		if got.authorization != "" || got.actor != "" || got.role != "" {
			t.Errorf("%s %s forwarded client identity: %+v", test.method, test.path, got)
		}
		if test.path == "/webhooks/stripe" && got.stripeHeader != "verified-by-upstream" {
			t.Errorf("Stripe signature = %q", got.stripeHeader)
		}
	}
}

func TestGatewayOnlyAllowsExactPublicRoutesAndMethods(t *testing.T) {
	harness := newGatewayHarness(t)
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/webhooks/stripe"},
		{method: http.MethodPost, path: "/healthz"},
		{method: http.MethodGet, path: "/healthz/"},
		{method: http.MethodPost, path: "/webhooks/stripe/"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()

		harness.gateway.ServeHTTP(response, request)

		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want %d", test.method, test.path, response.Code, http.StatusUnauthorized)
		}
	}
}

func TestGatewayRequiresSingleBearerTokenHeader(t *testing.T) {
	harness := newGatewayHarness(t)
	tests := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "malformed", values: []string{"Basic token"}},
		{name: "multiple", values: []string{"******", "******"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			for _, value := range test.values {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()

			harness.gateway.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestGatewayConfigurationRejectsUnsafeValues(t *testing.T) {
	valid := Config{
		Issuer: "https://identity.example", Audience: "shopstops",
		RoleClaim: "roles", RoleMappings: map[string]string{"admin": "admin"},
		InternalToken: strings.Repeat("x", 32), UpstreamURL: "http://api.internal:8080",
	}
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{name: "remote HTTP issuer", change: func(c *Config) { c.Issuer = "http://identity.example" }},
		{name: "short internal token", change: func(c *Config) { c.InternalToken = "short" }},
		{name: "unsupported role", change: func(c *Config) { c.RoleMappings["admin"] = "owner" }},
		{name: "upstream credentials", change: func(c *Config) { c.UpstreamURL = "******api.internal" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.RoleMappings = map[string]string{"admin": "admin"}
			test.change(&config)
			if err := validateConfig(config); err == nil {
				t.Fatal("expected invalid configuration to be rejected")
			}
		})
	}
}

type gatewayHarness struct {
	t             *testing.T
	gateway       *Gateway
	issuer        *httptest.Server
	key           *rsa.PrivateKey
	keyID         string
	audience      string
	internalToken string
	captured      chan capturedRequest
}

func newGatewayHarness(t *testing.T) *gatewayHarness {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "test-key"
	captured := make(chan capturedRequest, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- capturedRequest{
			authorization: r.Header.Get("Authorization"),
			actor:         r.Header.Get(actorHeader),
			role:          r.Header.Get(actorRoleHeader),
			roleSelection: r.Header.Get(roleSelectionHeader),
			cookie:        r.Header.Get("Cookie"),
			forwardedFor:  r.Header.Get("X-Forwarded-For"),
			stripeHeader:  r.Header.Get("Stripe-Signature"),
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuerURL(r), "jwks_uri": issuerURL(r) + "/keys",
			})
		case "/keys":
			n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]string{{
					"kty": "RSA", "kid": keyID, "use": "sig", "alg": "RS256",
					"n": n, "e": e,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.Close)

	const audience = "shopstops-api"
	const internalToken = "gateway-internal-token-with-at-least-32-characters"
	gateway, err := New(t.Context(), Config{
		Issuer: issuer.URL, Audience: audience, RoleClaim: "realm_access.roles",
		RoleMappings: map[string]string{
			"shop-admin": "admin", "shop-driver": "driver", "shop-customer": "customer",
		},
		InternalToken: internalToken, UpstreamURL: upstream.URL,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return &gatewayHarness{
		t: t, gateway: gateway, issuer: issuer, key: key, keyID: keyID, audience: audience,
		internalToken: internalToken, captured: captured,
	}
}

func issuerURL(request *http.Request) string {
	return fmt.Sprintf("http://%s", request.Host)
}

func (h *gatewayHarness) sign(additionalClaims map[string]any) string {
	claims := map[string]any{
		"iss": h.issuer.URL, "sub": "test-subject", "aud": h.audience,
		"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(),
		"realm_access": map[string]any{"roles": "shop-admin"},
	}
	for key, value := range additionalClaims {
		claims[key] = value
	}
	if roles, ok := additionalClaims["roles"]; ok {
		claims["realm_access"] = map[string]any{"roles": roles}
	}
	return signToken(h.t, h.key, h.keyID, claims)
}

func signToken(t *testing.T, key *rsa.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": keyID, "typ": "JWT"})
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
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}
