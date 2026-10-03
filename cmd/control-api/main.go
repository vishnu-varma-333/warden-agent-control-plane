// Command control-api serves the admin-facing API behind the console:
// tool registry, policies, approvals and audit queries. It shares no state
// with the gateway process but will share internal packages (auth, policy
// types) with it as those land.
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

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/approval"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/httpapi"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Init(ctx, "control-api")
	if err != nil {
		slog.Error("telemetry init failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := shutdownTelemetry(context.Background()); err != nil {
			slog.Error("telemetry shutdown failed", "error", err)
		}
	}()

	addr := os.Getenv("CONTROL_API_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	pgDSN := os.Getenv("DATABASE_URL")
	if pgDSN == "" {
		pgDSN = "postgres://warden:warden@localhost:5432/warden?sslmode=disable"
	}
	pgConn, err := db.Connect(pgDSN)
	if err != nil {
		slog.Error("database connect/migrate failed", "error", err)
		os.Exit(1)
	}
	defer pgConn.Close()

	controlAPIBaseURL := os.Getenv("CONTROL_API_BASE_URL")
	if controlAPIBaseURL == "" {
		controlAPIBaseURL = "http://localhost" + addr
	}
	var notifier approval.Notifier
	if webhookURL := os.Getenv("WEBHOOK_URL"); webhookURL != "" {
		notifier = &approval.WebhookNotifier{URL: webhookURL}
	} else {
		notifier = &approval.LogNotifier{ControlAPIBaseURL: controlAPIBaseURL}
	}
	// control-api publishes to the same Kafka topic the gateway's single
	// chain writer consumes from — any process that makes a decision can
	// be an audit producer; the chain itself only ever has one writer
	// (serialized via the Postgres row lock, not by process count). See
	// DECISIONS.md.
	redpandaBrokers := []string{envOr("REDPANDA_BROKERS", "localhost:9092")}
	auditProducer, err := audit.NewKafkaProducer(ctx, redpandaBrokers, "warden.audit.events")
	if err != nil {
		slog.Error("audit producer init failed", "error", err)
		os.Exit(1)
	}
	defer auditProducer.Close()

	approvalManager := approval.New(pgConn, notifier, auditProducer)
	approvalManager.StartExpirySweep(ctx, 10*time.Second)

	approvalsHandler := &httpapi.ApprovalsHandler{Manager: approvalManager}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("POST /approvals/{id}/decide", approvalsHandler.Decide)
	mux.HandleFunc("GET /approvals/{id}", approvalsHandler.Get)
	mux.HandleFunc("GET /approvals", approvalsHandler.List)

	srv := &http.Server{
		Addr:         addr,
		Handler:      otelhttp.NewHandler(mux, "control-api"),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("control-api listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("control-api server failed", "error", err)
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
	slog.Info("control-api stopped cleanly")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
