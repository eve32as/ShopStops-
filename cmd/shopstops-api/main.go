package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/eve32as/ShopStops-/internal/httpapi"
	"github.com/eve32as/ShopStops-/internal/routing"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL := os.Getenv("DATABASE_URL")
	apiToken := os.Getenv("SHOPSTOPS_INTERNAL_API_TOKEN")
	if databaseURL == "" || len(apiToken) < 32 {
		logger.Error("DATABASE_URL and a 32-character SHOPSTOPS_INTERNAL_API_TOKEN are required")
		os.Exit(1)
	}
	deliveryFeeCents, err := feeFromEnv("SHOPSTOPS_DELIVERY_FEE_CENTS")
	if err != nil {
		logger.Error("invalid delivery fee configuration", "error", err)
		os.Exit(1)
	}
	serviceFeeCents, err := feeFromEnv("SHOPSTOPS_SERVICE_FEE_CENTS")
	if err != nil {
		logger.Error("invalid service fee configuration", "error", err)
		os.Exit(1)
	}
	stripeSecretKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	stripeMerchantPriceID := os.Getenv("STRIPE_MERCHANT_MONTHLY_PRICE_ID")
	if (stripeSecretKey == "") != (stripeWebhookSecret == "") {
		logger.Error("STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET must both be configured")
		os.Exit(1)
	}
	if stripeMerchantPriceID != "" && stripeSecretKey == "" {
		logger.Error("STRIPE_MERCHANT_MONTHLY_PRICE_ID requires Stripe credentials")
		os.Exit(1)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := db.PingContext(startupCtx); err != nil {
		startupCancel()
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	startupCancel()

	repository := httpapi.NewPostgresRepository(db, deliveryFeeCents, serviceFeeCents)
	var router *routing.OSRMClient
	if osrmBaseURL := os.Getenv("SHOPSTOPS_OSRM_BASE_URL"); osrmBaseURL != "" {
		router, err = routing.NewOSRMClient(osrmBaseURL)
		if err != nil {
			logger.Error("invalid OSRM base URL", "error", err)
			os.Exit(1)
		}
	}
	var handler http.Handler
	if stripeSecretKey == "" {
		if router == nil {
			handler = httpapi.New(repository, apiToken, logger)
		} else {
			handler = httpapi.NewWithRouting(repository, apiToken, logger, router)
		}
	} else {
		if router == nil {
			handler = httpapi.NewWithPayments(
				repository, apiToken, logger,
				httpapi.NewStripeGateway(stripeSecretKey), stripeWebhookSecret,
				stripeMerchantPriceID,
			)
		} else {
			handler = httpapi.NewWithPaymentsAndRouting(
				repository, apiToken, logger,
				httpapi.NewStripeGateway(stripeSecretKey), stripeWebhookSecret,
				router, stripeMerchantPriceID,
			)
		}
	}

	server := &http.Server{
		Addr:              envOr("HTTP_ADDR", ":8080"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("ShopStops API listening", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", "error", err)
	}
}

func feeFromEnv(name string) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || int64(value) > 2147483647 {
		return 0, errors.New("fee must be a non-negative 32-bit integer number of cents")
	}
	return value, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
