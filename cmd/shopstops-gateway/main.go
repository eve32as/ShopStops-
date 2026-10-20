package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/eve32as/ShopStops-/internal/identitygateway"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	issuer := strings.TrimSpace(os.Getenv("SHOPSTOPS_OIDC_ISSUER"))
	audience := strings.TrimSpace(os.Getenv("SHOPSTOPS_OIDC_AUDIENCE"))
	internalToken := os.Getenv("SHOPSTOPS_INTERNAL_API_TOKEN")
	if issuer == "" || audience == "" || len(internalToken) < 32 {
		logger.Error("SHOPSTOPS_OIDC_ISSUER, SHOPSTOPS_OIDC_AUDIENCE, and a 32-character SHOPSTOPS_INTERNAL_API_TOKEN are required")
		os.Exit(1)
	}
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Scheme != "https" || issuerURL.Host == "" ||
		issuerURL.User != nil || issuerURL.RawQuery != "" || issuerURL.Fragment != "" {
		logger.Error("SHOPSTOPS_OIDC_ISSUER must be a valid HTTPS issuer URL")
		os.Exit(1)
	}
	upstream, err := url.Parse(envOr("SHOPSTOPS_API_URL", "http://127.0.0.1:8080"))
	if err != nil || (upstream.Scheme != "http" && upstream.Scheme != "https") ||
		upstream.Host == "" || upstream.User != nil ||
		upstream.RawQuery != "" || upstream.Fragment != "" {
		logger.Error("SHOPSTOPS_API_URL must be an HTTP(S) URL without credentials, query, or fragment")
		os.Exit(1)
	}

	discoveryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	discoveryCtx = oidc.ClientContext(discoveryCtx, httpClient)
	provider, err := oidc.NewProvider(discoveryCtx, issuer)
	if err != nil {
		logger.Error("discover OIDC issuer", "error", err)
		os.Exit(1)
	}
	verifier := provider.VerifierContext(
		oidc.ClientContext(context.Background(), httpClient),
		&oidc.Config{ClientID: audience},
	)
	handler, err := identitygateway.NewWithActorClaim(
		verifier, upstream, internalToken,
		envOr("SHOPSTOPS_OIDC_ROLE_CLAIM", "roles"),
		envOr("SHOPSTOPS_OIDC_ACTOR_CLAIM", "sub"),
		configuredOrigins(os.Getenv("SHOPSTOPS_GATEWAY_ALLOWED_ORIGINS"))...,
	)
	if err != nil {
		logger.Error("configure identity gateway", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              envOr("HTTP_ADDR", ":8081"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("ShopStops identity gateway listening", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("identity gateway failed", "error", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func configuredOrigins(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	origins := strings.Split(value, ",")
	for index := range origins {
		origins[index] = strings.TrimSpace(origins[index])
	}
	return origins
}
