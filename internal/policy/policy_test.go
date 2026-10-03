package policy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
)

// newTestEngine gives each test its own policy version (so engines don't
// interfere with each other or with a manually-run gateway sharing the
// same Postgres/Redis), seeded directly rather than via SeedIfEmpty (which
// only seeds version 1 globally).
func newTestEngine(t *testing.T, source string) *Engine {
	t.Helper()

	pgDSN := os.Getenv("TEST_DATABASE_URL")
	if pgDSN == "" {
		pgDSN = "postgres://warden:warden@localhost:5432/warden?sslmode=disable"
	}
	conn, err := db.Connect(pgDSN)
	if err != nil {
		t.Skipf("skipping: no reachable Postgres (%v)", err)
	}
	t.Cleanup(func() { conn.Close() })

	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: no reachable Redis (%v)", err)
	}

	version := uniqueVersion(t)
	deactivateAllAndInsert(t, conn, version, source)

	e := New(conn, rdb)
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	return e
}

// uniqueVersion derives a stable-per-test, unlikely-to-collide int from the
// test name, so parallel/repeated runs don't fight over the single-active
// partial-unique-index invariant.
func uniqueVersion(t *testing.T) int {
	h := 0
	for _, c := range t.Name() {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return 100000 + (h % 100000)
}

func deactivateAllAndInsert(t *testing.T, conn *sql.DB, version int, source string) {
	t.Helper()
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `UPDATE policies SET active = false WHERE active = true`); err != nil {
		t.Fatalf("deactivate existing: %v", err)
	}
	_, err := conn.ExecContext(ctx,
		`INSERT INTO policies (version, cedar_source, active) VALUES ($1, $2, true)
		 ON CONFLICT (version) DO UPDATE SET cedar_source = EXCLUDED.cedar_source, active = true`,
		version, source,
	)
	if err != nil {
		t.Fatalf("insert test policy: %v", err)
	}
	t.Cleanup(func() {
		conn.ExecContext(context.Background(), `DELETE FROM policies WHERE version = $1`, version)
	})
}

func TestPermitAllows(t *testing.T) {
	e := newTestEngine(t, DefaultSeedPolicy)
	d, err := e.Authorize(context.Background(), "agent-demo", "user-1", "CallModel", "Model", "mock-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Allow {
		t.Fatalf("expected allow for a permitted model, got deny: %v", d.Reasons)
	}
}

func TestDefaultDeniesUnknownResource(t *testing.T) {
	e := newTestEngine(t, DefaultSeedPolicy)
	d, err := e.Authorize(context.Background(), "agent-demo", "user-1", "CallModel", "Model", "some-other-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Allow {
		t.Fatal("expected deny for a model with no matching permit rule")
	}
}

func TestForbidOverridesPermit(t *testing.T) {
	e := newTestEngine(t, DefaultSeedPolicy)
	ctx := context.Background()

	// echo is permitted in general...
	d, err := e.Authorize(ctx, "agent-demo", "user-1", "CallTool", "Tool", "echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Allow {
		t.Fatalf("expected allow for echo as user-1, got deny: %v", d.Reasons)
	}

	// ...but explicitly forbidden when acting as user-2, despite the permit.
	d, err = e.Authorize(ctx, "agent-demo", "user-2", "CallTool", "Tool", "echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Allow {
		t.Fatal("expected forbid to override the permit for user-2")
	}
}

func TestDecisionIsCachedOnSecondCall(t *testing.T) {
	e := newTestEngine(t, DefaultSeedPolicy)
	ctx := context.Background()

	d1, err := e.Authorize(ctx, "agent-demo", "user-1", "CallModel", "Model", "mock-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d1.CacheHit {
		t.Fatal("expected first call to be a cache miss")
	}

	d2, err := e.Authorize(ctx, "agent-demo", "user-1", "CallModel", "Model", "mock-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d2.CacheHit {
		t.Fatal("expected second identical call to be a cache hit")
	}
	if d2.Allow != d1.Allow {
		t.Fatal("expected cached decision to match the original")
	}
}

func TestFailsClosedBeforeAnyPolicyLoaded(t *testing.T) {
	// An Engine that never had Refresh succeed must deny, not allow.
	e := &Engine{}
	d, err := e.Authorize(context.Background(), "agent-demo", "user-1", "CallModel", "Model", "mock-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Allow {
		t.Fatal("expected fail-closed deny when no policy has ever loaded")
	}
	if fmt.Sprint(d.Reasons) != "[no policy loaded]" {
		t.Fatalf("expected a clear reason, got %v", d.Reasons)
	}
}
