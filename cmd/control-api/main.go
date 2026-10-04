// Command control-api serves the admin-facing API behind the console:
// tool registry, policies, approvals and audit queries. It shares no state
// with the gateway process but will share internal packages (auth, policy
// types) with it as those land.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/approval"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/budget"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/httpapi"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/policy"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/registry"
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

	redisAddr := envOr("REDIS_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})

	toolRegistry := registry.New(pgConn)
	// auditProducer is passed nil here: control-api only reads/writes
	// policy rows directly (ListVersions/CreateVersion/Activate), it never
	// calls Authorize, so there's no decision to publish.
	policyEngine := policy.New(pgConn, redisClient, nil)
	// Must match the gateway's own budget.New call (cmd/gateway/main.go) —
	// same Redis keys, same limit/period, so the console reports the spend
	// the gateway is actually enforcing rather than a second, divergent
	// notion of it. No per-scope config exists yet (see docs/MILESTONES.md
	// spec-completeness tracking on per-team budgets), so this is the one
	// hardcoded value shared by both processes.
	budgetEnforcer := budget.New(redisClient, 100, time.Hour)

	var auditPubKey ed25519.PublicKey
	if pubHex := os.Getenv("WARDEN_AUDIT_PUBLIC_KEY"); pubHex != "" {
		if pubBytes, err := hex.DecodeString(pubHex); err == nil && len(pubBytes) == ed25519.PublicKeySize {
			auditPubKey = ed25519.PublicKey(pubBytes)
		} else {
			slog.Error("invalid WARDEN_AUDIT_PUBLIC_KEY, checkpoint fast-path verification disabled")
		}
	}

	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			slog.Error("generate admin token failed", "error", err)
			os.Exit(1)
		}
		adminToken = hex.EncodeToString(buf)
		slog.Warn("ADMIN_TOKEN not set — generated a random one for this process only; the console needs this exact value",
			"adminToken", adminToken)
	}

	approvalsHandler := &httpapi.ApprovalsHandler{Manager: approvalManager}
	toolsHandler := &httpapi.ToolsHandler{Registry: toolRegistry}
	policiesHandler := &httpapi.PoliciesHandler{Engine: policyEngine}
	auditHandler := &httpapi.AuditHandler{DB: pgConn, AuditPubKey: auditPubKey}
	spendHandler := &httpapi.SpendHandler{Budget: budgetEnforcer}

	startPeriodicAuditVerification(ctx, pgConn, auditPubKey, 5*time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("POST /approvals/{id}/decide", approvalsHandler.Decide)
	mux.HandleFunc("GET /approvals/{id}", approvalsHandler.Get)
	mux.HandleFunc("GET /approvals", approvalsHandler.List)
	mux.HandleFunc("GET /tools", toolsHandler.List)
	mux.HandleFunc("POST /tools/{server}/{name}/approve", toolsHandler.Approve)
	mux.HandleFunc("GET /policies", policiesHandler.List)
	mux.HandleFunc("POST /policies", policiesHandler.Create)
	mux.HandleFunc("POST /policies/{version}/activate", policiesHandler.Activate)
	mux.HandleFunc("GET /audit", auditHandler.List)
	mux.HandleFunc("POST /audit/verify", auditHandler.Verify)
	mux.HandleFunc("GET /spend", spendHandler.List)

	srv := &http.Server{
		Addr:         addr,
		Handler:      otelhttp.NewHandler(httpapi.RequireAdminToken(adminToken, mux), "control-api"),
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

// startPeriodicAuditVerification is what makes "alerts on audit-chain
// verification failures" (a named production-readiness requirement) mean
// something continuous rather than only reacting to an admin manually
// clicking "verify" in the console. Every interval, it runs the same
// check wardenctl and the console's button run (the checkpoint fast path
// when a public key is configured, same reasoning as AuditHandler.Verify
// — full-chain verification on every tick would get slower as the chain
// grows, which is exactly what checkpoints exist to avoid) and records
// the result via audit.RecordVerifyResult — what the Grafana alert rule
// (deploy/docker/grafana/provisioning/alerting) actually watches.
func startPeriodicAuditVerification(ctx context.Context, db *sql.DB, pubKey ed25519.PublicKey, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var result audit.VerifyResult
				var err error
				if pubKey != nil {
					result, err = audit.VerifyFromLatestCheckpoint(ctx, db, pubKey)
				} else {
					result, err = audit.VerifyFull(ctx, db)
				}
				if err != nil {
					slog.Error("periodic audit verification failed to run", "error", err)
					continue
				}
				audit.RecordVerifyResult(ctx, "periodic", result)
				if !result.OK {
					slog.Error("periodic audit verification found tampering",
						"failureAt", result.FailureAt, "reason", result.FailureReason)
				}
			}
		}
	}()
}
