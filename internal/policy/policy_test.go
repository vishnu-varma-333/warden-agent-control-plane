package policy

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
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

	e := New(conn, rdb, audit.NoopProducer{})
	if err := e.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	return e
}

// uniqueVersion must be fresh per RUN, not just per test name: the Redis
// decision cache is keyed on the policy version (see policy.go), with no
// explicit cleanup. A deterministic, name-derived version collides with
// itself on a second run within the cache's TTL, making a cache MISS look
// like a HIT — this is exactly how TestDecisionIsCachedOnSecondCall failed
// when the suite was run twice in a row. A random version per run sidesteps
// it entirely: a stale cache entry from a prior run is keyed to a version
// number this run will never ask for again.
func uniqueVersion(t *testing.T) int {
	return 100000 + rand.Intn(900000)
}

// deactivateAllAndInsert swaps in a test-only active policy, and restores
// whatever was active before on cleanup — without this, running these
// tests against the same Postgres a dev gateway is pointed at would
// permanently leave that gateway with no active policy (and therefore,
// per the fail-closed design, denying everything) once the test exits.
// This is not hypothetical: it happened during this milestone's own
// verification, which is exactly why it's fixed rather than left as a
// "don't run tests against a live dev DB" caveat.
func deactivateAllAndInsert(t *testing.T, conn *sql.DB, version int, source string) {
	t.Helper()
	ctx := context.Background()

	var previouslyActive []int
	rows, err := conn.QueryContext(ctx, `SELECT version FROM policies WHERE active = true`)
	if err != nil {
		t.Fatalf("read previously active versions: %v", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			t.Fatalf("scan previously active version: %v", err)
		}
		previouslyActive = append(previouslyActive, v)
	}
	rows.Close()

	if _, err := conn.ExecContext(ctx, `UPDATE policies SET active = false WHERE active = true`); err != nil {
		t.Fatalf("deactivate existing: %v", err)
	}
	_, err = conn.ExecContext(ctx,
		`INSERT INTO policies (version, cedar_source, active) VALUES ($1, $2, true)
		 ON CONFLICT (version) DO UPDATE SET cedar_source = EXCLUDED.cedar_source, active = true`,
		version, source,
	)
	if err != nil {
		t.Fatalf("insert test policy: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		conn.ExecContext(ctx, `DELETE FROM policies WHERE version = $1`, version)
		for _, v := range previouslyActive {
			conn.ExecContext(ctx, `UPDATE policies SET active = true WHERE version = $1`, v)
		}
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

func TestCreateVersionRejectsInvalidCedar(t *testing.T) {
	e := newTestEngine(t, DefaultSeedPolicy)
	if _, err := e.CreateVersion(context.Background(), "this is not cedar at all"); err == nil {
		t.Fatal("expected an error for invalid cedar source")
	}
}

func TestCreateListAndActivateVersion(t *testing.T) {
	e := newTestEngine(t, DefaultSeedPolicy)
	ctx := context.Background()

	newSource := `permit(principal, action == Warden::Action::"CallModel", resource == Warden::Model::"mock-model");`
	version, err := e.CreateVersion(ctx, newSource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { e.db.ExecContext(context.Background(), `DELETE FROM policies WHERE version = $1`, version) })

	versions, err := e.ListVersions(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, v := range versions {
		if v.Version == version {
			found = true
			if v.Active {
				t.Fatal("expected a newly created version to start inactive")
			}
		}
	}
	if !found {
		t.Fatal("expected ListVersions to include the newly created version")
	}

	if err := e.Activate(ctx, version); err != nil {
		t.Fatalf("unexpected error activating: %v", err)
	}
	versions, err = e.ListVersions(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var activeCount int
	for _, v := range versions {
		if v.Active {
			activeCount++
			if v.Version != version {
				t.Fatalf("expected only version %d active, found version %d active too", version, v.Version)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active version, got %d", activeCount)
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
