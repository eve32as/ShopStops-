package identitygateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	actorHeader         = "X-ShopStops-Actor"
	actorRoleHeader     = "X-ShopStops-Actor-Role"
	roleSelectionHeader = "X-ShopStops-Role-Selection"
	maxBearerTokenBytes = 16 * 1024
)

var supportedRoles = map[string]struct{}{
	"admin": {}, "customer": {}, "dispatch": {}, "driver": {}, "merchant": {},
}

type Config struct {
	Issuer        string
	Audience      string
	RoleClaim     string
	RoleMappings  map[string]string
	InternalToken string
	UpstreamURL   string
}

type Gateway struct {
	verifier     *oidc.IDTokenVerifier
	roleClaim    string
	roleMappings map[string]string
	internal     string
	proxy        *httputil.ReverseProxy
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Gateway, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	oidcClient := &http.Client{Timeout: 10 * time.Second}
	discoveryContext, cancel := context.WithTimeout(
		context.WithValue(ctx, oauth2.HTTPClient, oidcClient), 10*time.Second,
	)
	provider, err := oidc.NewProvider(discoveryContext, config.Issuer)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}

	jwksContext := context.WithValue(context.Background(), oauth2.HTTPClient, oidcClient)
	verifier := provider.VerifierContext(jwksContext, &oidc.Config{
		ClientID:             config.Audience,
		SupportedSigningAlgs: []string{"RS256", "ES256"},
	})
	upstream, _ := url.Parse(config.UpstreamURL)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.Out.Host = upstream.Host
			for _, header := range []string{
				"Forwarded", "X-Forwarded-For", "X-Forwarded-Host",
				"X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-Ip",
			} {
				request.Out.Header.Del(header)
			}
			if address := clientIP(request.In.RemoteAddr); address != "" {
				request.Out.Header.Set("X-Forwarded-For", address)
			}
		},
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxConnsPerHost:       100,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			logger.Error("identity gateway upstream request failed")
			http.Error(w, "upstream service unavailable", http.StatusBadGateway)
		},
	}

	mappings := make(map[string]string, len(config.RoleMappings))
	for claimRole, applicationRole := range config.RoleMappings {
		mappings[claimRole] = applicationRole
	}
	return &Gateway{
		verifier: verifier, roleClaim: config.RoleClaim, roleMappings: mappings,
		internal: config.InternalToken, proxy: proxy,
	}, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if (r.Method == http.MethodGet && r.URL.Path == "/healthz") ||
		(r.Method == http.MethodPost && r.URL.Path == "/webhooks/stripe") {
		g.forward(w, r, "", "")
		return
	}

	rawToken, ok := bearerToken(r.Header.Values("Authorization"))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token, err := g.verifier.Verify(r.Context(), rawToken)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !validActor(token.Subject) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var claims map[string]any
	if err := token.Claims(&claims); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claim, found := nestedClaim(claims, g.roleClaim)
	if !found {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	roles, valid := g.mappedRoles(claim)
	if !valid || len(roles) == 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	role, ok := requestedRole(r.Header.Values(roleSelectionHeader), roles)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	g.forward(w, r, token.Subject, role)
}

func (g *Gateway) forward(
	w http.ResponseWriter, r *http.Request, actor, role string,
) {
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	sanitizeHeaders(request.Header)
	if actor != "" {
		request.Header.Set("Authorization", "Bearer "+g.internal)
		request.Header.Set(actorHeader, actor)
		request.Header.Set(actorRoleHeader, role)
	}
	g.proxy.ServeHTTP(w, request)
}

func (g *Gateway) mappedRoles(value any) (map[string]struct{}, bool) {
	var claimRoles []string
	switch roles := value.(type) {
	case string:
		claimRoles = []string{roles}
	case []any:
		for _, role := range roles {
			value, ok := role.(string)
			if !ok {
				return nil, false
			}
			claimRoles = append(claimRoles, value)
		}
	case []string:
		claimRoles = roles
	default:
		return nil, false
	}

	mapped := make(map[string]struct{})
	for _, claimRole := range claimRoles {
		if role, exists := g.roleMappings[claimRole]; exists {
			mapped[role] = struct{}{}
		}
	}
	return mapped, true
}

func requestedRole(values []string, roles map[string]struct{}) (string, bool) {
	if len(values) > 1 {
		return "", false
	}
	if len(values) == 1 {
		role := values[0]
		if _, ok := roles[role]; !ok {
			return "", false
		}
		return role, true
	}
	if len(roles) != 1 {
		return "", false
	}
	for role := range roles {
		return role, true
	}
	return "", false
}

func nestedClaim(claims map[string]any, path string) (any, bool) {
	var value any = claims
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") ||
		len(parts[1]) > maxBearerTokenBytes {
		return "", false
	}
	return parts[1], true
}

func validActor(actor string) bool {
	if actor == "" || len(actor) > 200 || strings.TrimSpace(actor) != actor {
		return false
	}
	return !strings.ContainsFunc(actor, unicode.IsControl)
}

func sanitizeHeaders(headers http.Header) {
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "Cookie",
		actorHeader, actorRoleHeader, roleSelectionHeader,
		"Forwarded", "X-Forwarded-For", "X-Forwarded-Host",
		"X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-Ip",
		"X-Forwarded-Client-Cert",
	} {
		headers.Del(name)
	}
}

func clientIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	if address := net.ParseIP(host); address != nil {
		return address.String()
	}
	return ""
}

func validateConfig(config Config) error {
	if err := validateIssuer(config.Issuer); err != nil {
		return err
	}
	if strings.TrimSpace(config.Audience) == "" {
		return errors.New("OIDC audience is required")
	}
	if strings.TrimSpace(config.RoleClaim) == "" ||
		strings.TrimSpace(config.RoleClaim) != config.RoleClaim {
		return errors.New("OIDC role claim is required")
	}
	if len(config.InternalToken) < 32 {
		return errors.New("internal API token must contain at least 32 characters")
	}
	if err := validateUpstream(config.UpstreamURL); err != nil {
		return err
	}
	if len(config.RoleMappings) == 0 {
		return errors.New("at least one OIDC role mapping is required")
	}
	for claimRole, role := range config.RoleMappings {
		if strings.TrimSpace(claimRole) == "" || strings.TrimSpace(claimRole) != claimRole {
			return errors.New("OIDC role mapping keys must be non-empty and trimmed")
		}
		if _, ok := supportedRoles[role]; !ok {
			return fmt.Errorf("unsupported ShopStops role %q", role)
		}
	}
	return nil
}

func validateIssuer(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimSpace(raw) != raw {
		return errors.New("OIDC issuer must be an absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return errors.New("OIDC issuer must use HTTPS except for a loopback development issuer")
}

func validateUpstream(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		strings.TrimSpace(raw) != raw {
		return errors.New("upstream URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil &&
		net.ParseIP(host).IsLoopback()
}
