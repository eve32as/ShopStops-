package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/eve32as/ShopStops-/internal/finance"
	"github.com/eve32as/ShopStops-/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL := os.Getenv("DATABASE_URL")
	apiToken := os.Getenv("SHOPSTOPS_INTERNAL_API_TOKEN")
	if databaseURL == "" || len(apiToken) < 32 {
		logger.Error("DATABASE_URL and a 32-character SHOPSTOPS_INTERNAL_API_TOKEN are required")
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

	repository := httpapi.NewPostgresRepository(db, finance.DefaultAssumptions())
	var handler http.Handler
	if stripeSecretKey == "" {
		handler = httpapi.New(repository, apiToken, logger)
	} else {
		handler = httpapi.NewWithPayments(
			repository, apiToken, logger,
			httpapi.NewStripeGateway(stripeSecretKey), stripeWebhookSecret,
			stripeMerchantPriceID,
		)
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

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
