package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 4 * 1024 * 1024

type handler struct {
	config   Config
	verifier verifier
	backend  *url.URL
	client   *http.Client
}

func newHandler(c Config, v verifier) *handler {
	u, _ := backendURL(c.BackendURL) // New validates before construction.
	return &handler{config: c, verifier: v, backend: u, client: &http.Client{
		Timeout:       28 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
		Transport: &http.Transport{
			Proxy: nil, DialContext: privateDial, TLSHandshakeTimeout: 5 * time.Second,
			ResponseHeaderTimeout: 27 * time.Second, MaxResponseHeaderBytes: 32 * 1024,
			IdleConnTimeout: 60 * time.Second, MaxIdleConnsPerHost: 16,
		},
	}}
}

// Revalidate DNS at every dial and connect to the validated IP, not the name.
// This prevents an allowed private hostname resolving/rebinding to public IPs.
func privateDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid backend address")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("backend lookup failed")
	}
	for _, ip := range ips {
		if !ip.IP.IsPrivate() && !ip.IP.IsLoopback() {
			return nil, errors.New("non-private backend address refused")
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("backend connection failed")
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Vary", "Origin")
	path := r.URL.Path
	// No URL normalization, redirects, escaped separators, double encoding,
	// dot segments, backslashes, or ServeMux subtree matching at this boundary.
	if r.URL.EscapedPath() != path || (r.URL.RawPath != "" && r.URL.RawPath != path) ||
		!strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\%\x00") || strings.Contains(path, "//") {
		fail(w, 400, "ambiguous path")
		return
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			fail(w, 400, "ambiguous path")
			return
		}
	}
	if strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 {
		fail(w, 403, "origin refused")
		return
	}
	origin := r.Header.Get("Origin")
	if len(origins) != 0 {
		allowed := false
		for _, value := range h.config.AllowedOrigins {
			allowed = allowed || (origin != "" && origin == value)
		}
		if !allowed {
			fail(w, 403, "origin refused")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	method := r.Method
	if method == "OPTIONS" {
		method = r.Header.Get("Access-Control-Request-Method")
	}
	op, ok := route(method, path)
	if !ok {
		fail(w, 404, "operation not supported")
		return
	}
	if op.role == "blocked" {
		fail(w, 403, "operation disabled pending identity binding review")
		return
	}
	if r.Method == "OPTIONS" {
		if origin == "" || len(r.Header.Values("Access-Control-Request-Method")) != 1 || len(r.Header.Values("Access-Control-Request-Headers")) > 1 {
			fail(w, 403, "preflight refused")
			return
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			switch strings.ToLower(strings.TrimSpace(header)) {
			case "", "authorization", "content-type", "idempotency-key", "stripe-signature":
			default:
				fail(w, 403, "preflight header refused")
				return
			}
		}
		w.Header().Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		w.Header().Set("Access-Control-Allow-Methods", method)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Stripe-Signature")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var id identity
	raw := ""
	if op.role != "" {
		values := r.Header.Values("Authorization")
		if len(values) != 1 {
			fail(w, 401, "invalid bearer token")
			return
		}
		parts := strings.Split(values[0], " ")
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !validText(parts[1], 16*1024) {
			fail(w, 401, "invalid bearer token")
			return
		}
		raw = parts[1]
		var err error
		id, err = h.verifier.verify(ctx, raw)
		if err != nil {
			fail(w, 401, "invalid bearer token")
			return
		}
		if id.role != op.role || !validText(id.actor, 200) || (id.role == "merchant" && !uuidPattern.MatchString(id.actor)) {
			fail(w, 403, "role required")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, op.limit))
	if err != nil {
		fail(w, 413, "request body too large or unreadable")
		return
	}
	u := *h.backend
	u.Path, u.RawPath, u.RawQuery = path, "", r.URL.RawQuery
	upstream, err := http.NewRequestWithContext(ctx, r.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		fail(w, 502, "backend unavailable")
		return
	}
	// Rebuild, rather than mutate client headers: no cookies, auth, forwarding,
	// hop-by-hop, internal headers, user-selected Host or identity can survive.
	for _, key := range []string{"Accept", "Content-Type", "Idempotency-Key"} {
		if values := r.Header.Values(key); len(values) == 1 {
			upstream.Header.Set(key, values[0])
		}
	}
	if path == "/webhooks/stripe" {
		if values := r.Header.Values("Stripe-Signature"); len(values) == 1 {
			upstream.Header.Set("Stripe-Signature", values[0])
		}
	}
	if op.role != "" {
		upstream.Header.Set("Authorization", "Bearer "+h.config.InternalToken)
		upstream.Header.Set("X-ShopStops-Actor", id.actor)
		upstream.Header.Set("X-ShopStops-Actor-Role", id.role)
	}
	resp, err := h.client.Do(upstream)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		fail(w, 502, "backend unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		fail(w, 502, "backend redirect or upgrade refused")
		return
	}
	if resp.StatusCode >= 500 {
		code := http.StatusBadGateway
		if path == "/healthz" && resp.StatusCode == http.StatusServiceUnavailable {
			code = http.StatusServiceUnavailable
		}
		fail(w, code, "backend unavailable")
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes || containsCredential(data, h.config.InternalToken, raw) {
		fail(w, 502, "invalid backend response")
		return
	}
	// Intentionally expose no upstream response headers (cookies, Location,
	// Authorization, internal/debug headers), only a safe content type.
	w.Header().Set("Content-Type", "application/octet-stream")
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
}

// Inspect JSON strings as well as raw bytes so JSON escaping cannot conceal a
// reflected service/bearer credential. Stream tokens rather than constructing
// a second object graph for a potentially large response.
func containsCredential(data []byte, credentials ...string) bool {
	for _, credential := range credentials {
		if credential != "" && bytes.Contains(data, []byte(credential)) {
			return true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if value, ok := token.(string); ok {
			for _, credential := range credentials {
				if credential != "" && strings.Contains(value, credential) {
					return true
				}
			}
		}
	}
}

func fail(w http.ResponseWriter, code int, message string) { http.Error(w, message, code) }
