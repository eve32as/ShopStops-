package identitygateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
)

type TokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}

type Gateway struct {
	verifier       TokenVerifier
	upstream       *url.URL
	internalToken  string
	roleClaim      string
	actorClaim     string
	allowedOrigins map[string]struct{}
	proxy          *httputil.ReverseProxy
}

type identityContextKey struct{}

type trustedIdentity struct {
	actor string
	role  string
}

func New(
	verifier TokenVerifier, upstream *url.URL, internalToken, roleClaim string,
	origins ...string,
) (*Gateway, error) {
	return NewWithActorClaim(verifier, upstream, internalToken, roleClaim, "sub", origins...)
}

func NewWithActorClaim(
	verifier TokenVerifier, upstream *url.URL, internalToken, roleClaim, actorClaim string,
	origins ...string,
) (*Gateway, error) {
	if verifier == nil || upstream == nil ||
		(upstream.Scheme != "http" && upstream.Scheme != "https") ||
		upstream.Host == "" || upstream.User != nil ||
		upstream.RawQuery != "" || upstream.Fragment != "" || upstream.Path != "" && upstream.Path != "/" ||
		len(internalToken) < 32 || strings.TrimSpace(roleClaim) != roleClaim || roleClaim == "" ||
		strings.TrimSpace(actorClaim) != actorClaim || actorClaim == "" {
		return nil, errors.New("invalid identity gateway configuration")
	}
	gateway := &Gateway{
		verifier: verifier, upstream: upstream,
		internalToken: internalToken, roleClaim: roleClaim, actorClaim: actorClaim,
		allowedOrigins: make(map[string]struct{}, len(origins)),
	}
	for _, origin := range origins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || parsed.User != nil ||
			parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Scheme != "https" &&
				!(parsed.Scheme == "http" &&
					(parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1"))) {
			return nil, errors.New("invalid identity gateway CORS origin")
		}
		gateway.allowedOrigins[origin] = struct{}{}
	}
	gateway.proxy = &httputil.ReverseProxy{
		Rewrite: gateway.rewrite,
	}
	return gateway, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.handleCORS(w, r) {
		return
	}
	if (r.URL.Path == "/v1/healthz" || r.URL.Path == "/v1/readyz") &&
		r.Method == http.MethodGet {
		g.proxy.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/v1/webhooks/stripe" && r.Method == http.MethodPost {
		g.proxy.ServeHTTP(w, r)
		return
	}

	requiredRoles, ok := requiredRoles(r.Method, strings.TrimPrefix(r.URL.Path, "/v1"))
	if !ok || !strings.HasPrefix(r.URL.Path, "/v1/") {
		http.NotFound(w, r)
		return
	}
	authorization := r.Header.Get("Authorization")
	if isAdminDispatchStreamUpgrade(r) {
		protocolToken, protocols, valid := dispatchStreamProtocolToken(
			r.Header.Get("Sec-WebSocket-Protocol"),
		)
		if !valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if protocolToken != "" {
			if authorization != "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			authorization = "Bearer " + protocolToken
		}
		if len(protocols) > 0 {
			r.Header.Set("Sec-WebSocket-Protocol", strings.Join(protocols, ", "))
		}
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) || len(authorization) <= len(prefix) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token, err := g.verifier.Verify(r.Context(), strings.TrimSpace(strings.TrimPrefix(authorization, prefix)))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	actor := token.Subject
	if g.actorClaim != "sub" {
		var claims map[string]json.RawMessage
		if token.Claims(&claims) != nil || json.Unmarshal(claims[g.actorClaim], &actor) != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if !validActor(actor) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	roles, err := tokenRoles(token, g.roleClaim)
	if err != nil || !hasAnyRole(roles, requiredRoles) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	actorRole := requiredRoles[0]
	if actorRole == "dispatch" && contains(roles, "admin") {
		actorRole = "admin"
	}
	identity := trustedIdentity{actor: actor, role: actorRole}
	r = r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity))
	g.proxy.ServeHTTP(w, r)
}

func isAdminDispatchStreamUpgrade(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		r.URL.Path == "/v1/admin/dispatch/stream" &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func dispatchStreamProtocolToken(header string) (string, []string, bool) {
	var token string
	var protocols []string
	for _, protocol := range strings.Split(header, ",") {
		protocol = strings.TrimSpace(protocol)
		if protocol == "" {
			continue
		}
		if strings.HasPrefix(protocol, "bearer.") {
			if token != "" || len(protocol) == len("bearer.") {
				return "", nil, false
			}
			encoded := strings.TrimPrefix(protocol, "bearer.")
			decoded, err := base64.RawURLEncoding.DecodeString(encoded)
			if err != nil || len(decoded) == 0 {
				return "", nil, false
			}
			token = string(decoded)
			continue
		}
		if protocol != "shopstops-admin-v1" {
			return "", nil, false
		}
		protocols = append(protocols, protocol)
	}
	if token != "" && len(protocols) != 1 {
		return "", nil, false
	}
	return token, protocols, true
}

func validActor(actor string) bool {
	if actor == "" || len(actor) > 200 || strings.TrimSpace(actor) != actor {
		return false
	}
	for _, char := range actor {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func (g *Gateway) handleCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		if _, ok := g.allowedOrigins[origin]; !ok {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	if r.Method != http.MethodOptions {
		return true
	}
	if origin == "" {
		http.Error(w, "origin required", http.StatusForbidden)
		return false
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Authorization, Content-Type, Idempotency-Key, Last-Event-ID, Stripe-Signature")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
	return false
}

func (g *Gateway) rewrite(proxyRequest *httputil.ProxyRequest) {
	proxyRequest.SetURL(g.upstream)
	proxyRequest.Out.URL.Path = strings.TrimPrefix(proxyRequest.In.URL.Path, "/v1")
	if proxyRequest.Out.URL.Path == "" {
		proxyRequest.Out.URL.Path = "/"
	}
	proxyRequest.Out.URL.RawPath = ""
	proxyRequest.Out.Host = g.upstream.Host
	proxyRequest.Out.Header.Del("Authorization")
	proxyRequest.Out.Header.Del("X-ShopStops-Actor")
	proxyRequest.Out.Header.Del("X-ShopStops-Actor-Role")
	proxyRequest.Out.Header.Del("X-Forwarded-For")
	proxyRequest.Out.Header.Del("X-Forwarded-Host")
	proxyRequest.Out.Header.Del("X-Forwarded-Proto")
	proxyRequest.Out.Header.Del("Forwarded")
	proxyRequest.Out.Header.Del("X-ShopStops-Requested-Role")
	proxyRequest.Out.Header.Del("Cookie")
	proxyRequest.Out.Header.Del("X-Original-URL")
	proxyRequest.Out.Header.Del("X-Rewrite-URL")
	proxyRequest.Out.Header.Set("Authorization", "Bearer "+g.internalToken)
	if identity, ok := proxyRequest.In.Context().Value(identityContextKey{}).(trustedIdentity); ok {
		proxyRequest.Out.Header.Set("X-ShopStops-Actor", identity.actor)
		proxyRequest.Out.Header.Set("X-ShopStops-Actor-Role", identity.role)
	}
}

func tokenRoles(token *oidc.IDToken, claimName string) ([]string, error) {
	var claims map[string]json.RawMessage
	if err := token.Claims(&claims); err != nil {
		return nil, err
	}
	raw, ok := claims[claimName]
	if !ok {
		return nil, errors.New("role claim missing")
	}
	var role string
	if err := json.Unmarshal(raw, &role); err == nil {
		return []string{role}, nil
	}
	var roles []string
	if err := json.Unmarshal(raw, &roles); err != nil || len(roles) == 0 {
		return nil, errors.New("invalid role claim")
	}
	return roles, nil
}

func requiredRoles(method, path string) ([]string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 0 {
		return nil, false
	}
	switch {
	case method == http.MethodGet && (path == "/healthz" || path == "/readyz"):
		return nil, true
	case method == http.MethodGet && len(segments) == 3 &&
		segments[0] == "stores" && isUUID(segments[1]) &&
		(segments[2] == "nearby-merchants" || segments[2] == "catalog"):
		return []string{"customer"}, true
	case method == http.MethodPost && path == "/orders":
		return []string{"customer"}, true
	case method == http.MethodGet && len(segments) == 3 &&
		segments[0] == "orders" && isUUID(segments[1]) && segments[2] == "events":
		return []string{"customer"}, true
	case method == http.MethodPost && path == "/checkouts":
		return []string{"customer"}, true
	case method == http.MethodPost && len(segments) == 3 &&
		segments[0] == "checkouts" && isUUID(segments[1]) && segments[2] == "payment":
		return []string{"customer"}, true
	case strings.HasPrefix(path, "/merchant/"):
		return methodRole(method, "merchant")
	case strings.HasPrefix(path, "/driver/"):
		return methodRole(method, "driver")
	case strings.HasPrefix(path, "/dispatch/"):
		if method == http.MethodPost &&
			(path == "/dispatch/bundles" || path == "/dispatch/routes") {
			return []string{"dispatch", "admin"}, true
		}
	case strings.HasPrefix(path, "/admin/"):
		return methodRole(method, "admin")
	}
	return nil, false
}

func methodRole(method, role string) ([]string, bool) {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPatch:
		return []string{role}, true
	default:
		return nil, false
	}
}

func hasAnyRole(roles, required []string) bool {
	for _, role := range required {
		if contains(roles, role) {
			return true
		}
	}
	return false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
		} else if !((char >= '0' && char <= '9') ||
			(char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
