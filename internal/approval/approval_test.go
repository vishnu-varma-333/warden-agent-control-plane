package approval

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
)

type recordingNotifier struct {
	mu    sync.Mutex
	count int
}

func (n *recordingNotifier) Notify(context.Context, Approval, CallSnapshot) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.count++
	return nil
}

func newTestManager(t *testing.T) (*Manager, *recordingNotifier, *sql.DB) {
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

	// Tests scope their rows by resource_id (see testSnapshot), but don't
	// clean up after themselves otherwise — without this, a prior run's
	// leftover row collides with "first" inserts in idempotency-key tests,
	// since the key is derived from the deterministic test name.
	t.Cleanup(func() {
		conn.ExecContext(context.Background(),
			`DELETE FROM approvals WHERE resource_id = $1`, "test-resource-"+t.Name())
	})

	notifier := &recordingNotifier{}
	return New(conn, notifier, audit.NoopProducer{}), notifier, conn
}

func testSnapshot(t *testing.T) CallSnapshot {
	return CallSnapshot{
		AgentID: "agent-demo", ActingAs: "user-1",
		Action: "CallTool", ResourceType: "Tool", ResourceID: "test-resource-" + t.Name(),
	}
}

func TestRequestWithoutKeyNeverDeduplicates(t *testing.T) {
	m, notifier, _ := newTestManager(t)
	ctx := context.Background()
	snap := testSnapshot(t)

	a1, err := m.Request(ctx, "", snap, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a2, err := m.Request(ctx, "", snap, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a1.ID == a2.ID {
		t.Fatal("expected two separate approvals when no idempotency key is given")
	}
	if notifier.count != 2 {
		t.Fatalf("expected 2 notifications, got %d", notifier.count)
	}
}

func TestRequestWithSameKeyDeduplicatesAndNotifiesOnce(t *testing.T) {
	m, notifier, _ := newTestManager(t)
	ctx := context.Background()
	snap := testSnapshot(t)
	key := "idem-" + t.Name()

	a1, err := m.Request(ctx, key, snap, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a2, err := m.Request(ctx, key, snap, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatal("expected the same approval to be returned for a reused idempotency key")
	}
	if notifier.count != 1 {
		t.Fatalf("expected exactly 1 notification despite 2 requests, got %d", notifier.count)
	}
}

func TestDecideIsAtomicAgainstDoubleDecision(t *testing.T) {
	m, _, _ := newTestManager(t)
	ctx := context.Background()
	a, err := m.Request(ctx, "", testSnapshot(t), time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := m.Decide(ctx, a.ID, StateApproved, "alice"); err != nil {
		t.Fatalf("first decide should succeed: %v", err)
	}
	if err := m.Decide(ctx, a.ID, StateRejected, "bob"); err == nil {
		t.Fatal("expected second decide on an already-decided approval to fail")
	}

	state, err := m.getState(ctx, a.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateApproved {
		t.Fatalf("expected state to remain 'approved' (bob's rejection must not have applied), got %q", state)
	}
}

func TestClaimExecutionOnlyAllowsOneWinner(t *testing.T) {
	m, _, _ := newTestManager(t)
	ctx := context.Background()
	a, err := m.Request(ctx, "", testSnapshot(t), time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := m.Decide(ctx, a.ID, StateApproved, "alice"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Simulate concurrent retries racing to execute the same approved action.
	const attempts = 20
	var claimedCount int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			claimed, err := m.ClaimExecution(ctx, a.ID)
			if err != nil {
				t.Error(err)
				return
			}
			if claimed {
				atomic.AddInt64(&claimedCount, 1)
			}
		}()
	}
	wg.Wait()

	if claimedCount != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent claims to win, got %d", attempts, claimedCount)
	}
}

func TestWaitForDecisionReturnsOnceDecided(t *testing.T) {
	m, _, _ := newTestManager(t)
	ctx := context.Background()
	a, err := m.Request(ctx, "", testSnapshot(t), time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		if err := m.Decide(context.Background(), a.ID, StateApproved, "alice"); err != nil {
			t.Error(err)
		}
	}()

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	state, err := m.WaitForDecision(waitCtx, a.ID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateApproved {
		t.Fatalf("expected approved, got %q", state)
	}
}

func TestExpiresAfterTTL(t *testing.T) {
	m, _, _ := newTestManager(t)
	ctx := context.Background()
	a, err := m.Request(ctx, "", testSnapshot(t), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	state, err := m.WaitForDecision(ctx, a.ID, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateExpired {
		t.Fatalf("expected expired, got %q", state)
	}
}
