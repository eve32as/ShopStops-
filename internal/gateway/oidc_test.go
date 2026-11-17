package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// All JWTs/keys in this file are generated ephemerally for a loopback test
// issuer. No actual IdP, external account, production token or secret is used.
func signedTestToken(t *testing.T, key any, alg jose.SignatureAlgorithm, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	object, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := object.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRemoteSignedOIDCVerification(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	forgedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.URL, "jwks_uri": issuer.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256", "HS256"}})
		case "/keys":
			requests.Add(1)
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
		default:
			t.Error("unexpected issuer operation")
			w.WriteHeader(404)
		}
	}))
	defer issuer.Close()
	c := testConfig("http://127.0.0.1:1")
	c.Issuer = issuer.URL
	if _, err := New(context.Background(), c); err == nil {
		t.Fatal("production allowed HTTP discovery")
	}
	v, err := discover(context.Background(), c, true)
	if err != nil {
		t.Fatal(err)
	}
	claims := func() map[string]any {
		return map[string]any{"iss": issuer.URL, "sub": "test-subject", "aud": c.Audience, "exp": time.Now().Add(time.Hour).Unix(), RoleClaim: "admin"}
	}
	good := signedTestToken(t, key, jose.RS256, claims())
	id, err := v.verify(context.Background(), good)
	if err != nil || id.role != "customer" || id.actor != "test-subject" {
		t.Fatal("valid signed JWT/default role failed")
	}
	if requests.Load() != 1 {
		t.Fatal("remote JWKS not used")
	}
	v.config.TrustedClaims = true
	id, err = v.verify(context.Background(), good)
	if err != nil || id.role != "admin" {
		t.Fatal("configured trusted admin contract rejected")
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"issuer", func(c map[string]any) { c["iss"] = "https://forged.example" }},
		{"audience", func(c map[string]any) { c["aud"] = "another-client" }},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{"missing expiry", func(c map[string]any) { delete(c, "exp") }},
		{"future nbf", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{"missing sub", func(c map[string]any) { delete(c, "sub") }},
		{"header unsafe sub", func(c map[string]any) { c["sub"] = "actor\r\nspoof" }},
		{"invalid trusted role", func(c map[string]any) { c[RoleClaim] = "root" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := claims()
			tc.mutate(payload)
			if _, err := v.verify(context.Background(), signedTestToken(t, key, jose.RS256, payload)); err == nil {
				t.Fatal("invalid signed JWT accepted")
			}
		})
	}
	for _, raw := range []string{
		signedTestToken(t, forgedKey, jose.RS256, claims()),
		signedTestToken(t, []byte(strings.Repeat("x", 32)), jose.HS256, claims()),
		"not-a-jwt",
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"admin"}`)) + ".",
	} {
		if _, err := v.verify(context.Background(), raw); err == nil {
			t.Fatal("forged/unsigned/wrong-algorithm JWT accepted")
		}
	}
	parts := strings.Split(good, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"admin"}`))
	if _, err := v.verify(context.Background(), strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered JWT accepted")
	}
}

func TestDiscoveryFailsClosed(t *testing.T) {
	for _, tc := range []string{"redirect", "issuer mismatch", "HTTP nonloopback JWKS", "bad response"} {
		t.Run(tc, func(t *testing.T) {
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer target.Close()
			var issuer *httptest.Server
			issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc == "redirect" {
					http.Redirect(w, r, target.URL, 302)
					return
				}
				if tc == "bad response" {
					_, _ = w.Write([]byte("invalid"))
					return
				}
				metadata := map[string]string{"issuer": issuer.URL, "jwks_uri": issuer.URL + "/keys"}
				if tc == "issuer mismatch" {
					metadata["issuer"] = "https://different.example"
				}
				if tc == "HTTP nonloopback JWKS" {
					metadata["jwks_uri"] = "http://untrusted.example/keys"
				}
				_ = json.NewEncoder(w).Encode(metadata)
			}))
			defer issuer.Close()
			c := testConfig("http://api.internal")
			c.Issuer = issuer.URL
			if _, err := discover(context.Background(), c, true); err == nil {
				t.Fatal("bad discovery accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("discovery redirect followed")
			}
		})
	}
}
