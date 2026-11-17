// Package gateway is the public identity boundary for the private ShopStops API.
package gateway

import (
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	ActorClaim = "https://shopstops.example/claims/actor"
	RoleClaim  = "https://shopstops.example/claims/role"
)

var dnsHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$`)

type Config struct {
	Issuer, Audience, BackendURL, InternalToken string
	// Enable only after reviewing the issuer's non-user-editable claim contract.
	TrustedClaims  bool
	SigningAlgs    []string
	AllowedOrigins []string
}

func ConfigFromEnv() (Config, error) {
	c := Config{
		Issuer:        os.Getenv("SHOPSTOPS_OIDC_ISSUER"),
		Audience:      os.Getenv("SHOPSTOPS_OIDC_AUDIENCE"),
		BackendURL:    os.Getenv("SHOPSTOPS_BACKEND_URL"),
		InternalToken: os.Getenv("SHOPSTOPS_INTERNAL_API_TOKEN"),
		SigningAlgs:   []string{"RS256"},
	}
	if value := os.Getenv("SHOPSTOPS_OIDC_TRUSTED_CLAIMS"); value != "" {
		var err error
		c.TrustedClaims, err = strconv.ParseBool(value)
		if err != nil {
			return Config{}, errors.New("invalid trusted claims setting")
		}
	}
	if value := os.Getenv("SHOPSTOPS_OIDC_SIGNING_ALGS"); value != "" {
		c.SigningAlgs = strings.Split(value, ",")
	}
	if value := os.Getenv("SHOPSTOPS_CORS_ORIGINS"); value != "" {
		c.AllowedOrigins = strings.Split(value, ",")
	}
	return c, c.validate()
}

func (c Config) validate() error {
	if !secureIssuer(c.Issuer, false) {
		return errors.New("OIDC issuer must be an absolute HTTPS URL without credentials, query or fragment")
	}
	if c.Audience == "" || strings.TrimSpace(c.Audience) != c.Audience {
		return errors.New("OIDC audience is required")
	}
	if !validText(c.InternalToken, 4096) || len(c.InternalToken) < 32 {
		return errors.New("internal API token must contain at least 32 non-whitespace ASCII characters")
	}
	if _, err := backendURL(c.BackendURL); err != nil {
		return err
	}
	if len(c.SigningAlgs) == 0 {
		return errors.New("signing algorithm allowlist is required")
	}
	for _, alg := range c.SigningAlgs {
		switch alg {
		case "RS256", "RS384", "RS512", "ES256", "ES384", "ES512", "PS256", "PS384", "PS512":
		default:
			return errors.New("unsupported signing algorithm")
		}
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || strings.Contains(u.Host, "*") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
			(u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname()))) {
			return errors.New("CORS origins must be exact HTTPS origins (HTTP only for loopback)")
		}
	}
	return nil
}

func validText(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, b := range []byte(s) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}

func loopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func secureIssuer(raw string, testLoopback bool) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		(u.Scheme == "https" || (testLoopback && u.Scheme == "http" && loopbackHost(u.Hostname())))
}

func backendURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("backend must be a private HTTP(S) origin without credentials or path")
	}
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, errors.New("invalid backend port")
		}
	}
	ip := net.ParseIP(host)
	private := ip != nil && (ip.IsPrivate() || ip.IsLoopback())
	if ip == nil {
		private = len(host) <= 253 && dnsHostPattern.MatchString(host) && (host == "localhost" || !strings.Contains(host, ".") ||
			strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local"))
	}
	if !private {
		return nil, errors.New("backend hostname must be private IP, loopback, single-label DNS, .internal or .svc[.cluster.local]")
	}
	return u, nil
}
