package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
)

// newTestRegistry connects to the local dev Postgres (docker-compose) and
// runs migrations. A real database, not a fake: this package's whole job is
// SQL-level atomicity and state transitions, which a mocked DB wouldn't
// actually exercise. Testcontainers-based isolation (so this doesn't depend
// on a pre-started local stack) is a production-readiness milestone item.
func newTestRegistry(t *testing.T) (*Registry, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://warden:warden@localhost:5432/warden?sslmode=disable"
	}
	conn, err := db.Connect(dsn)
	if err != nil {
		t.Skipf("skipping: no reachable Postgres at %s (%v) — start it with docker compose -f deploy/docker/docker-compose.yml up -d postgres", dsn, err)
	}
	t.Cleanup(func() { conn.Close() })

	// Isolate this test run's rows under a unique server name instead of
	// truncating the shared table, so parallel/local test runs don't stomp
	// on each other or on data left by a manually-run gateway.
	return New(conn), conn
}

func uniqueServer(t *testing.T) string {
	return fmt.Sprintf("test-server-%s", t.Name())
}

func TestFirstSightPins(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	server := uniqueServer(t)

	status, err := r.Reconcile(ctx, server, "echo", "hash-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusActive {
		t.Fatalf("expected active on first sight, got %q", status)
	}
}

func TestMatchingHashStaysActive(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	server := uniqueServer(t)

	if _, err := r.Reconcile(ctx, server, "echo", "hash-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	status, err := r.Reconcile(ctx, server, "echo", "hash-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusActive {
		t.Fatalf("expected still active for an unchanged hash, got %q", status)
	}
}

func TestChangedHashBlocksAndApproveClears(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	server := uniqueServer(t)

	if _, err := r.Reconcile(ctx, server, "echo", "hash-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	status, err := r.Reconcile(ctx, server, "echo", "hash-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusChanged {
		t.Fatalf("expected changed after a differing hash, got %q", status)
	}

	// Even reverting to the original hash shouldn't self-heal the block.
	status, err = r.Reconcile(ctx, server, "echo", "hash-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusChanged {
		t.Fatalf("expected still changed even after reverting to the original hash, got %q", status)
	}

	if err := r.Approve(ctx, server, "echo"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	status, ok, err := r.Status(ctx, server, "echo")
	if err != nil || !ok {
		t.Fatalf("unexpected error/ok after approve: %v, %v", err, ok)
	}
	if status != StatusActive {
		t.Fatalf("expected active after approval, got %q", status)
	}
}

func TestListIncludesReconciledTools(t *testing.T) {
	r, _ := newTestRegistry(t)
	ctx := context.Background()
	server := uniqueServer(t)

	if _, err := r.Reconcile(ctx, server, "echo", "hash-a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, err := r.List(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, tl := range tools {
		if tl.MCPServer == server && tl.Name == "echo" {
			found = true
			if tl.DefinitionHash != "hash-a" || tl.Status != StatusActive {
				t.Fatalf("unexpected tool record: %+v", tl)
			}
		}
	}
	if !found {
		t.Fatal("expected List to include the just-reconciled tool")
	}
}

func TestStatusUnknownTool(t *testing.T) {
	r, _ := newTestRegistry(t)
	_, ok, err := r.Status(context.Background(), uniqueServer(t), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a tool never reconciled")
	}
}
