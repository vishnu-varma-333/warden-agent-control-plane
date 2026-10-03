// Command gateway is Warden's hot-path service: every agent tool and model
// call passes through it on the way to a provider or MCP server.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redis/go-redis/v9"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/approval"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/budget"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/guard"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/cache"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/httpapi"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/identity"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/mcpgateway"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/policy"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider/mock"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/ratelimit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/registry"
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
	if ms := os.Getenv("MOCK_PRIMARY_LATENCY_MS"); ms != "" {
		if d, err := time.ParseDuration(ms + "ms"); err == nil {
			primary.SetLatency(d)
		}
	}

	r := router.New()
	r.RegisterProvider(primary)
	r.RegisterProvider(secondary)
	r.AddRoute(router.Route{ModelAlias: "mock-model", Providers: []string{"mock-primary", "mock-secondary"}})

	keycloakIssuer := os.Getenv("KEYCLOAK_ISSUER")
	if keycloakIssuer == "" {
		keycloakIssuer = "http://localhost:8180/realms/warden"
	}
	verifier, err := initIdentityVerifier(ctx, keycloakIssuer)
	if err != nil {
		slog.Error("identity verifier init failed", "error", err)
		os.Exit(1)
	}

	// Starting limits: 60 requests/minute and 100 cost-units/hour per agent.
	// "Cost unit" is a placeholder (see httpapi.costPerCall) until real
	// provider usage reporting exists. Overridable for local demos/tests.
	requestsPerMinute := 60
	if v := os.Getenv("RATE_LIMIT_PER_MINUTE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			requestsPerMinute = n
		}
	}
	limiter := ratelimit.New(redisClient, requestsPerMinute, time.Minute)
	budgetPerHour := 100.0
	if v := os.Getenv("BUDGET_LIMIT_PER_HOUR"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			budgetPerHour = n
		}
	}
	budgetEnforcer := budget.New(redisClient, budgetPerHour, time.Hour)

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

	redpandaBrokers := strings.Split(envOr("REDPANDA_BROKERS", "localhost:9092"), ",")
	const auditTopic = "warden.audit.events"

	auditProducer, err := audit.NewKafkaProducer(ctx, redpandaBrokers, auditTopic)
	if err != nil {
		slog.Error("audit producer init failed", "error", err)
		os.Exit(1)
	}
	defer auditProducer.Close()

	signingKey := loadOrGenerateAuditSigningKey()

	chainWriter, err := audit.NewChainWriter(pgConn, redpandaBrokers, auditTopic)
	if err != nil {
		slog.Error("audit chain writer init failed", "error", err)
		os.Exit(1)
	}
	defer chainWriter.Close()
	go chainWriter.Run(ctx)

	checkpointer := audit.NewCheckpointer(pgConn, signingKey)
	checkpointer.StartPeriodic(ctx, 60*time.Second)

	policyEngine := policy.New(pgConn, redisClient, auditProducer)
	if err := policyEngine.SeedIfEmpty(ctx); err != nil {
		slog.Error("policy seed failed", "error", err)
		os.Exit(1)
	}
	if err := policyEngine.Refresh(ctx); err != nil {
		slog.Error("initial policy load failed", "error", err)
		os.Exit(1)
	}
	policyEngine.StartPeriodicRefresh(ctx, 10*time.Second)

	chatHandler := &httpapi.ChatHandler{Router: r, Cache: respCache, RateLimit: limiter, Budget: budgetEnforcer, Policy: policyEngine}

	controlAPIBaseURL := os.Getenv("CONTROL_API_BASE_URL")
	if controlAPIBaseURL == "" {
		controlAPIBaseURL = "http://localhost:8081"
	}
	var approvalNotifier approval.Notifier
	if webhookURL := os.Getenv("WEBHOOK_URL"); webhookURL != "" {
		approvalNotifier = &approval.WebhookNotifier{URL: webhookURL}
	} else {
		approvalNotifier = &approval.LogNotifier{ControlAPIBaseURL: controlAPIBaseURL}
	}
	approvalManager := approval.New(pgConn, approvalNotifier, auditProducer)
	if err := approvalManager.SeedRuleIfMissing(ctx, "CallTool", "Tool", "delete_data"); err != nil {
		slog.Error("approval rule seed failed", "error", err)
		os.Exit(1)
	}
	approvalManager.StartExpirySweep(ctx, 10*time.Second)

	toolRegistry := registry.New(pgConn)
	guardAddr := envOr("GUARD_CLASSIFIER_ADDR", "localhost:50051")
	// 50ms, not a round-number guess: measured classifier latency is
	// ~5-9ms (see DECISIONS.md and services/guard-classifier/BENCHMARKS),
	// so this leaves real headroom for a slow call while still being a
	// "strict" budget relative to typical request latency.
	guardClient, err := guard.New(guardAddr, 50*time.Millisecond)
	if err != nil {
		slog.Error("guard client init failed", "error", err)
		os.Exit(1)
	}
	defer guardClient.Close()

	mcpGW := mcpgateway.New(toolRegistry, policyEngine, approvalManager, guardClient, verifier, &mcp.Implementation{Name: "warden", Version: "v1"})

	demoMCPAddr := os.Getenv("DEMO_MCP_URL")
	if demoMCPAddr == "" {
		demoMCPAddr = "http://localhost:9090"
	}
	if err := mcpGW.ConnectUpstream(ctx, mcpgateway.UpstreamConfig{Name: "demo-tools", URL: demoMCPAddr}); err != nil {
		// Non-fatal: the model gateway is still useful with no tools
		// connected yet, and periodic sync will pick the upstream up once
		// it's reachable. See DECISIONS.md.
		slog.Error("mcp upstream connect failed; continuing without it", "error", err)
	}
	mcpGW.StartPeriodicSync(ctx, 30*time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/readyz", handleReadyz)
	mux.Handle("/v1/chat/completions", identity.Middleware(verifier)(chatHandler))
	mux.Handle("/mcp", identity.Middleware(verifier)(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpGW.Server() }, nil,
	)))

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

// initIdentityVerifier retries fetching Keycloak's JWKS a few times: in
// local dev, the gateway and Keycloak often start at roughly the same
// time, and Keycloak's first boot (realm import) is slow enough that a
// single immediate attempt would routinely lose this race.
func initIdentityVerifier(ctx context.Context, issuer string) (*identity.Verifier, error) {
	const attempts = 10
	var lastErr error
	for i := 1; i <= attempts; i++ {
		v, err := identity.NewVerifier(ctx, issuer)
		if err == nil {
			return v, nil
		}
		lastErr = err
		slog.Info("waiting for Keycloak", "attempt", i, "of", attempts, "error", err)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadOrGenerateAuditSigningKey reads a persistent Ed25519 seed from
// WARDEN_AUDIT_SIGNING_KEY if set, or generates a fresh one and logs its
// public key. This is a known, stated gap, not a silent one: a freshly
// generated key on every restart means checkpoints signed before a restart
// can't be verified against the new public key afterward. The real fix is
// loading this from a secrets manager (already tracked as a broader
// production-readiness gap in docs/MILESTONES.md's spec-completeness
// section) — local dev doesn't need that machinery, but should still make
// the limitation visible rather than quietly signing with throwaway keys.
func loadOrGenerateAuditSigningKey() ed25519.PrivateKey {
	if seedHex := os.Getenv("WARDEN_AUDIT_SIGNING_KEY"); seedHex != "" {
		seed, err := hex.DecodeString(seedHex)
		if err != nil || len(seed) != ed25519.SeedSize {
			slog.Error("invalid WARDEN_AUDIT_SIGNING_KEY, must be a hex-encoded 32-byte seed", "error", err)
			os.Exit(1)
		}
		return ed25519.NewKeyFromSeed(seed)
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		slog.Error("generate audit signing key failed", "error", err)
		os.Exit(1)
	}
	slog.Warn("no WARDEN_AUDIT_SIGNING_KEY set; generated an ephemeral one for this process",
		"publicKey", hex.EncodeToString(pub),
		"note", "checkpoints signed this run won't verify after a restart unless this exact key is persisted and reused")
	return priv
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
