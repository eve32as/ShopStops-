package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/eve32as/ShopStops-/internal/payouts"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL := os.Getenv("DATABASE_URL")
	smtpAddress := os.Getenv("SHOPSTOPS_SMTP_ADDRESS")
	smtpHost := os.Getenv("SHOPSTOPS_SMTP_HOST")
	smtpFrom := os.Getenv("SHOPSTOPS_PAYOUT_FROM")
	mileageRate, err := strconv.ParseInt(os.Getenv("SHOPSTOPS_DRIVER_MILEAGE_RATE_CENTS_PER_MILE"), 10, 64)
	if databaseURL == "" || smtpAddress == "" || smtpHost == "" ||
		smtpFrom == "" || err != nil || mileageRate < 0 {
		logger.Error("DATABASE_URL, SMTP settings, payout sender, and a non-negative mileage rate are required")
		os.Exit(1)
	}
	if _, _, err := net.SplitHostPort(smtpAddress); err != nil {
		logger.Error("SHOPSTOPS_SMTP_ADDRESS must include host and port", "error", err)
		os.Exit(1)
	}
	if (os.Getenv("SHOPSTOPS_SMTP_USERNAME") == "") !=
		(os.Getenv("SHOPSTOPS_SMTP_PASSWORD") == "") {
		logger.Error("SMTP username and password must be configured together")
		os.Exit(1)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := db.PingContext(startupCtx); err != nil {
		startupCancel()
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	startupCancel()

	sender, err := payouts.NewSMTPSender(payouts.SMTPConfig{
		Address: smtpAddress, Host: smtpHost,
		Username: os.Getenv("SHOPSTOPS_SMTP_USERNAME"),
		Password: os.Getenv("SHOPSTOPS_SMTP_PASSWORD"),
		From:     smtpFrom,
	})
	if err != nil {
		logger.Error("invalid SMTP configuration", "error", err)
		os.Exit(1)
	}
	worker := &payouts.Worker{
		Repository:       payouts.NewPostgresRepository(db),
		Sender:           sender,
		MileageRateCents: mileageRate,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		created, sent, runErr := worker.RunOnce(runCtx, time.Now().UTC())
		cancel()
		if runErr != nil {
			logger.Error("weekly payout mailer run failed", "error", runErr)
		} else if created > 0 || sent > 0 {
			logger.Info("weekly payout mailer run completed",
				"statements_created", created, "messages_sent", sent)
		}
		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				logger.Error("worker shutdown", "error", ctx.Err())
			}
			return
		case <-ticker.C:
		}
	}
}
