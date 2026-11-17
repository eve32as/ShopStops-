package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type identity struct{ actor, role string }
type verifier interface {
	verify(context.Context, string) (identity, error)
}
type oidcVerifier struct {
	remote *oidc.IDTokenVerifier
	config Config
}

// New performs mandatory discovery before accepting traffic. There is no fake
// verifier, unsigned-token mode, or insecure discovery option in the public API.
func New(ctx context.Context, c Config) (http.Handler, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	v, err := discover(ctx, c, false)
	if err != nil {
		return nil, errors.New("OIDC discovery failed")
	}
	return newHandler(c, v), nil
}

// testLoopback is only used by package tests; New always passes false.
func discover(ctx context.Context, c Config, testLoopback bool) (*oidcVerifier, error) {
	if !secureIssuer(c.Issuer, testLoopback) {
		return nil, errors.New("invalid issuer")
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
		Transport: discoveryTransport{base: &http.Transport{
			Proxy: nil, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
			MaxResponseHeaderBytes: 32 * 1024, IdleConnTimeout: 60 * time.Second,
		}, testLoopback: testLoopback},
	}
	clientCtx := oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(clientCtx, c.Issuer)
	if err != nil {
		return nil, err
	}
	var metadata struct {
		Issuer string `json:"issuer"`
		JWKS   string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, err
	}
	if metadata.Issuer != c.Issuer || !secureIssuer(metadata.JWKS, testLoopback) {
		return nil, errors.New("invalid discovery metadata")
	}
	return &oidcVerifier{remote: provider.VerifierContext(clientCtx, &oidc.Config{
		ClientID: c.Audience, SupportedSigningAlgs: c.SigningAlgs,
	}), config: c}, nil
}

type discoveryTransport struct {
	base         http.RoundTripper
	testLoopback bool
}

func (t discoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !secureIssuer(r.URL.String(), t.testLoopback) {
		return nil, errors.New("insecure OIDC request refused")
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &boundedBody{Reader: io.LimitReader(resp.Body, 1024*1024), Closer: resp.Body}
	return resp, nil
}

type boundedBody struct {
	io.Reader
	io.Closer
}

func (v *oidcVerifier) verify(ctx context.Context, raw string) (identity, error) {
	if strings.Count(raw, ".") != 2 {
		return identity{}, errors.New("compact JWT required")
	}
	token, err := v.remote.Verify(ctx, raw)
	if err != nil {
		return identity{}, errors.New("invalid bearer token")
	}
	// Require exact issuer (including providers for which the library allows an alias).
	if token.Issuer != v.config.Issuer || !token.Expiry.After(time.Now()) || !validText(token.Subject, 200) {
		return identity{}, errors.New("invalid identity")
	}
	var claims map[string]json.RawMessage
	if err := token.Claims(&claims); err != nil {
		return identity{}, errors.New("invalid claims")
	}
	return claimsIdentity(token.Subject, claims, v.config.TrustedClaims)
}

func claimsIdentity(subject string, claims map[string]json.RawMessage, trusted bool) (identity, error) {
	if !validText(subject, 200) {
		return identity{}, errors.New("invalid subject")
	}
	id := identity{actor: subject, role: "customer"}
	if !trusted {
		return id, nil
	}
	if raw, ok := claims[RoleClaim]; ok {
		var value *string
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return identity{}, errors.New("invalid role")
		}
		id.role = *value
	}
	switch id.role {
	case "customer": // Customer references always use sub, never client-selected IDs.
	case "admin", "merchant", "driver":
		if raw, ok := claims[ActorClaim]; ok {
			var value *string
			if err := json.Unmarshal(raw, &value); err != nil || value == nil {
				return identity{}, errors.New("invalid actor")
			}
			id.actor = *value
		} else if id.role == "merchant" {
			return identity{}, errors.New("merchant binding missing")
		}
	default:
		return identity{}, errors.New("unknown role")
	}
	if !validText(id.actor, 200) || (id.role == "merchant" && !uuidPattern.MatchString(id.actor)) {
		return identity{}, errors.New("invalid actor binding")
	}
	return id, nil
}
