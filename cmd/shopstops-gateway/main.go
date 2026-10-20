package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
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
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" || len(via) >= 5 {
				return errors.New("OIDC discovery redirects must remain HTTPS")
			}
			return nil
		},
	}
	discoveryCtx = oidc.ClientContext(discoveryCtx, httpClient)
	provider, err := oidc.NewProvider(discoveryCtx, issuer)
	if err != nil {
		logger.Error("discover OIDC issuer", "error", err)
		os.Exit(1)
	}
	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		logger.Error("read OIDC issuer metadata", "error", err)
		os.Exit(1)
	}
	jwksURL, err := url.Parse(metadata.JWKSURI)
	if err != nil || jwksURL.Scheme != "https" || jwksURL.Host == "" ||
		jwksURL.User != nil || jwksURL.RawQuery != "" || jwksURL.Fragment != "" {
		logger.Error("OIDC issuer must provide a valid HTTPS jwks_uri")
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
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	shutdownSignals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverErrors:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return
		}
		logger.Error("identity gateway failed", "error", err)
		os.Exit(1)
	case <-shutdownSignals.Done():
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("identity gateway shutdown failed", "error", err)
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
