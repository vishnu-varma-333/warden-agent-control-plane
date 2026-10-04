package audit

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
)

// testDB connects to a genuinely separate database (warden_test), not the
// shared dev database other packages' tests use. Audit has exactly one
// global chain (that's the point - a real chain has one history), so
// unlike registry/policy/approval tests there's no per-test key to
// namespace rows by; running against the shared dev DB would mean these
// tests permanently corrupt whatever a manually-run gateway has recorded,
// the same class of mistake found and fixed in milestone 6's DECISIONS.md.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_AUDIT_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://warden:warden@localhost:5432/warden_test?sslmode=disable"
	}
	conn, err := db.Connect(dsn)
	if err != nil {
		t.Skipf("skipping: no reachable test Postgres at %s (%v)", dsn, err)
	}
	t.Cleanup(func() { conn.Close() })

	ctx := context.Background()
	conn.ExecContext(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_update`)
	conn.ExecContext(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_delete`)
	conn.ExecContext(ctx, `TRUNCATE audit_events, audit_checkpoints`)
	conn.ExecContext(ctx, `UPDATE audit_chain_state SET last_seq = 0, last_hash = '' WHERE id = 1`)
	return conn
}

// bypassImmutabilityTrigger disables the DB-level write-prevention trigger
// (0005_audit_write_prevention.up.sql) for the duration of one test, then
// re-enables it — ALTER TABLE ... [DISABLE|ENABLE] TRIGGER is a catalog
// change, not scoped to a session or transaction, so leaving it disabled
// would silently weaken every test that runs afterward in the same suite.
// Tests that use this are deliberately simulating an attacker with raw
// database access bypassing the application (and, with this migration, the
// normal write path) entirely — exactly the scenario Verify exists to
// catch; see TestAuditEventsRejectDirectTamperingByDefault for the
// trigger's own positive-path proof, which does NOT use this helper.
func bypassImmutabilityTrigger(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `ALTER TABLE audit_events DISABLE TRIGGER audit_events_no_update`); err != nil {
		t.Fatalf("disable update trigger: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE audit_events DISABLE TRIGGER audit_events_no_delete`); err != nil {
		t.Fatalf("disable delete trigger: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		conn.ExecContext(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_update`)
		conn.ExecContext(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_delete`)
	})
}

func sampleEvent() Event {
	return Event{
		EventID: uuid.NewString(), Decision: "allow", Reason: "policy0",
		AgentID: "agent-demo", ActingAs: "user-1", Action: "CallModel",
		ResourceType: "Model", ResourceID: "mock-model", OccurredAt: time.Now().UTC(),
	}
}

func TestAppendBuildsAChain(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	ctx := context.Background()

	e1, e2 := sampleEvent(), sampleEvent()
	if err := w.Append(ctx, e1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := w.Append(ctx, e2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var seq1, seq2 int64
	var prevHash1, hash1, prevHash2 string
	conn.QueryRowContext(ctx, `SELECT seq, prev_hash, hash FROM audit_events WHERE event_id = $1`, e1.EventID).
		Scan(&seq1, &prevHash1, &hash1)
	conn.QueryRowContext(ctx, `SELECT seq, prev_hash FROM audit_events WHERE event_id = $1`, e2.EventID).
		Scan(&seq2, &prevHash2)

	if seq1 != 1 || seq2 != 2 {
		t.Fatalf("expected seq 1 then 2, got %d then %d", seq1, seq2)
	}
	if prevHash1 != "" {
		t.Fatalf("expected the first record's prev_hash to be empty (genesis), got %q", prevHash1)
	}
	if prevHash2 != hash1 {
		t.Fatalf("expected record 2's prev_hash to equal record 1's hash, got %q vs %q", prevHash2, hash1)
	}
}

func TestAppendIsIdempotentOnRedeliveredEventID(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	ctx := context.Background()

	e := sampleEvent()
	if err := w.Append(ctx, e); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := w.Append(ctx, e); err != nil { // simulate Kafka redelivery of the same message
		t.Fatalf("unexpected error on redelivery: %v", err)
	}

	var count int
	conn.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE event_id = $1`, e.EventID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 row despite 2 appends of the same event_id, got %d", count)
	}

	var lastSeq int64
	conn.QueryRowContext(ctx, `SELECT last_seq FROM audit_chain_state WHERE id = 1`).Scan(&lastSeq)
	if lastSeq != 1 {
		t.Fatalf("expected chain state to still be at seq 1 (no phantom advance), got %d", lastSeq)
	}
}

func TestConcurrentAppendsProduceAValidSingleChain(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	ctx := context.Background()

	const n = 25
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if err := w.Append(ctx, sampleEvent()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	result, err := VerifyFull(ctx, conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected a valid chain after %d concurrent appends, got failure at seq %d: %s", n, result.FailureAt, result.FailureReason)
	}
	if result.RecordsVerified != n {
		t.Fatalf("expected %d records, verified %d", n, result.RecordsVerified)
	}
}
