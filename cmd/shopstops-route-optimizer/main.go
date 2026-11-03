package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/eve32as/ShopStops-/internal/routeoptimization"
	"github.com/eve32as/ShopStops-/internal/routing"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL := os.Getenv("DATABASE_URL")
	osrmBaseURL := os.Getenv("SHOPSTOPS_OSRM_BASE_URL")
	costRate, err := strconv.ParseInt(
		os.Getenv("SHOPSTOPS_ROUTE_COST_CENTS_PER_MILE"), 10, 64,
	)
	if databaseURL == "" || osrmBaseURL == "" || err != nil || costRate <= 0 {
		logger.Error(
			"DATABASE_URL, SHOPSTOPS_OSRM_BASE_URL, and a positive " +
				"SHOPSTOPS_ROUTE_COST_CENTS_PER_MILE are required",
		)
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
	router, err := routing.NewOSRMClient(osrmBaseURL)
	if err != nil {
		logger.Error("invalid OSRM configuration", "error", err)
		os.Exit(1)
	}
	worker := &routeoptimization.Worker{
		Repository: routeoptimization.NewPostgresRepository(db),
		Router:     router, CostCentsPerMile: costRate,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		processed, runErr := worker.RunOnce(runCtx, time.Now().UTC())
		cancel()
		if runErr != nil {
			logger.Error("route optimization job failed", "error", runErr)
		} else if processed {
			logger.Info("route optimization job processed")
		}
		if processed {
			continue
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
