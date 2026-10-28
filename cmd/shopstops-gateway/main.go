package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/eve32as/ShopStops-/internal/identitygateway"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("identity gateway failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	config, err := configFromEnv()
	if err != nil {
		return err
	}
	certFile := os.Getenv("SHOPSTOPS_GATEWAY_TLS_CERT_FILE")
	keyFile := os.Getenv("SHOPSTOPS_GATEWAY_TLS_KEY_FILE")
	if (certFile == "") != (keyFile == "") {
		return errors.New("both gateway TLS certificate and key files must be configured")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startupContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	gateway, err := identitygateway.New(startupContext, config, logger)
	cancel()
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              envOr("SHOPSTOPS_GATEWAY_HTTP_ADDR", ":8081"),
		Handler:           gateway,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("ShopStops identity gateway listening", "address", server.Addr)
		if certFile == "" {
			serverErrors <- server.ListenAndServe()
			return
		}
		serverErrors <- server.ListenAndServeTLS(certFile, keyFile)
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve identity gateway: %w", err)
		}
		return nil
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shut down identity gateway: %w", err)
	}
	return nil
}

func configFromEnv() (identitygateway.Config, error) {
	var mappings map[string]string
	rawMappings := os.Getenv("SHOPSTOPS_OIDC_ROLE_MAPPINGS")
	if rawMappings == "" || json.Unmarshal([]byte(rawMappings), &mappings) != nil {
		return identitygateway.Config{}, errors.New(
			"SHOPSTOPS_OIDC_ROLE_MAPPINGS must be a JSON object mapping provider roles to ShopStops roles",
		)
	}
	config := identitygateway.Config{
		Issuer:        os.Getenv("SHOPSTOPS_OIDC_ISSUER"),
		Audience:      os.Getenv("SHOPSTOPS_OIDC_AUDIENCE"),
		RoleClaim:     os.Getenv("SHOPSTOPS_OIDC_ROLE_CLAIM"),
		RoleMappings:  mappings,
		InternalToken: os.Getenv("SHOPSTOPS_INTERNAL_API_TOKEN"),
		UpstreamURL:   os.Getenv("SHOPSTOPS_API_UPSTREAM_URL"),
	}
	if config.Issuer == "" || config.Audience == "" || config.RoleClaim == "" ||
		config.InternalToken == "" || config.UpstreamURL == "" {
		return identitygateway.Config{}, errors.New(
			"OIDC issuer, audience, role claim, internal API token, and API upstream URL are required",
		)
	}
	if strings.TrimSpace(config.Issuer) != config.Issuer {
		return identitygateway.Config{}, errors.New("OIDC issuer contains invalid whitespace")
	}
	return config, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
