package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eve32as/ShopStops-/internal/gateway"
)

func main() {
	if err := run(); err != nil {
		// Deliberately omit error details, URLs, claims and tokens from logs.
		slog.Error("gateway stopped or configuration/discovery failed")
		os.Exit(1)
	}
}

func run() error {
	c, err := gateway.ConfigFromEnv()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h, err := gateway.New(ctx, c)
	if err != nil {
		return err
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 35 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
