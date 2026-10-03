// Command gateway is Warden's hot-path service: every agent tool and model
// call passes through it on the way to a provider or MCP server.
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

	"github.com/redis/go-redis/v9"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/cache"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/httpapi"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider/mock"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/router"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Init(ctx, "gateway")
	if err != nil {
		slog.Error("telemetry init failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := shutdownTelemetry(context.Background()); err != nil {
			slog.Error("telemetry shutdown failed", "error", err)
		}
	}()

	addr := os.Getenv("GATEWAY_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	respCache := cache.New(redisClient, 10*time.Minute)

	// Two mock providers wired as primary/secondary for one route. This is
	// what milestone 2 needs to prove fallback/caching/streaming without a
	// real provider key; see DECISIONS.md for when a real provider gets
	// plugged in instead of (or alongside) these.
	primary := mock.New("mock-primary")
	secondary := mock.New("mock-secondary")
	if os.Getenv("MOCK_PRIMARY_UNHEALTHY") == "true" {
		primary.SetUnhealthy(true)
	}

	r := router.New()
	r.RegisterProvider(primary)
	r.RegisterProvider(secondary)
	r.AddRoute(router.Route{ModelAlias: "mock-model", Providers: []string{"mock-primary", "mock-secondary"}})

	chatHandler := &httpapi.ChatHandler{Router: r, Cache: respCache}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/readyz", handleReadyz)
	mux.Handle("/v1/chat/completions", chatHandler)

	srv := &http.Server{
		Addr:         addr,
		Handler:      otelhttp.NewHandler(mux, "gateway"),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("gateway listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown signal received, draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("gateway stopped cleanly")
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReadyz will later check Postgres, Redis and the policy engine before
// reporting ready; for now it mirrors healthz until those deps land.
func handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
