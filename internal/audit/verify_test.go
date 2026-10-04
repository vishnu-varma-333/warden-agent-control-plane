package audit

import (
	"context"
	"strings"
	"testing"
)

func seedChain(t *testing.T, w *ChainWriter, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := w.Append(ctx, sampleEvent()); err != nil {
			t.Fatalf("seed append %d: %v", i, err)
		}
	}
}

func TestVerifyPassesOnAnUntamperedChain(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 5)

	result, err := VerifyFull(context.Background(), conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got failure at seq %d: %s", result.FailureAt, result.FailureReason)
	}
	if result.RecordsVerified != 5 {
		t.Fatalf("expected 5 records verified, got %d", result.RecordsVerified)
	}
}

func TestListRecentOrdersNewestFirstAndRespectsLimit(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 5)

	records, err := ListRecent(context.Background(), conn, 3, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records (limit), got %d", len(records))
	}
	if records[0].Seq != 5 || records[1].Seq != 4 || records[2].Seq != 3 {
		t.Fatalf("expected newest-first seqs 5,4,3, got %d,%d,%d", records[0].Seq, records[1].Seq, records[2].Seq)
	}
	if records[0].Hash == "" {
		t.Fatal("expected ListRecent to populate each record's own hash")
	}

	older, err := ListRecent(context.Background(), conn, 10, records[2].Seq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(older) != 2 || older[0].Seq != 2 || older[1].Seq != 1 {
		t.Fatalf("expected paging before seq 3 to return seqs 2,1, got %+v", older)
	}
}

func TestVerifyDetectsAnEditedRecord(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 5)
	ctx := context.Background()
	bypassImmutabilityTrigger(t, conn)

	// Tamper directly, bypassing the application entirely - simulating
	// someone with raw database access editing history, which is exactly
	// the threat this mechanism exists to catch.
	if _, err := conn.ExecContext(ctx, `UPDATE audit_events SET reason = 'forged reason' WHERE seq = 3`); err != nil {
		t.Fatalf("tamper setup failed: %v", err)
	}

	result, err := VerifyFull(ctx, conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Fatal("expected tampering to be detected, got OK")
	}
	if result.FailureAt != 3 {
		t.Fatalf("expected the failure to be pinpointed at seq 3, got %d", result.FailureAt)
	}
	if result.RecordsVerified != 2 {
		t.Fatalf("expected records 1-2 to verify cleanly before the break, got %d", result.RecordsVerified)
	}
}

func TestVerifyDetectsADeletedRecord(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 5)
	ctx := context.Background()
	bypassImmutabilityTrigger(t, conn)

	if _, err := conn.ExecContext(ctx, `DELETE FROM audit_events WHERE seq = 3`); err != nil {
		t.Fatalf("tamper setup failed: %v", err)
	}

	result, err := VerifyFull(ctx, conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Fatal("expected a deleted record to be detected, got OK")
	}
	if result.FailureAt != 4 {
		t.Fatalf("expected the gap to surface at seq 4 (the record whose prev_hash now points to a hash nobody computed), got %d", result.FailureAt)
	}
}

func TestVerifyDetectsReorderedRecords(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 5)
	ctx := context.Background()
	bypassImmutabilityTrigger(t, conn)

	// Swap the (prev_hash, hash) of records 2 and 3 to simulate reordering
	// while keeping seq numbers contiguous (the harder case to catch than
	// a simple gap).
	var hash2, prev2, hash3, prev3 string
	conn.QueryRowContext(ctx, `SELECT prev_hash, hash FROM audit_events WHERE seq = 2`).Scan(&prev2, &hash2)
	conn.QueryRowContext(ctx, `SELECT prev_hash, hash FROM audit_events WHERE seq = 3`).Scan(&prev3, &hash3)
	conn.ExecContext(ctx, `UPDATE audit_events SET prev_hash = $1, hash = $2 WHERE seq = 2`, prev3, hash3)
	conn.ExecContext(ctx, `UPDATE audit_events SET prev_hash = $1, hash = $2 WHERE seq = 3`, prev2, hash2)

	result, err := VerifyFull(ctx, conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Fatal("expected reordering to be detected, got OK")
	}
	if result.FailureAt != 2 {
		t.Fatalf("expected the break to surface at seq 2, got %d", result.FailureAt)
	}
}

// TestAuditEventsRejectDirectTamperingByDefault proves the DB-level write
// prevention itself (0005_audit_write_prevention.up.sql) — the other
// tamper tests in this file prove the hash chain DETECTS tampering after
// the fact; this proves the database PREVENTS it in the first place, for
// the connection the real application actually uses (no
// bypassImmutabilityTrigger call here, deliberately — that's the whole
// point).
func TestAuditEventsRejectDirectTamperingByDefault(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 1)
	ctx := context.Background()

	_, err := conn.ExecContext(ctx, `UPDATE audit_events SET reason = 'forged' WHERE seq = 1`)
	if err == nil {
		t.Fatal("expected UPDATE on audit_events to be rejected by the DB trigger, it succeeded")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected the trigger's own error message, got: %v", err)
	}

	_, err = conn.ExecContext(ctx, `DELETE FROM audit_events WHERE seq = 1`)
	if err == nil {
		t.Fatal("expected DELETE on audit_events to be rejected by the DB trigger, it succeeded")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("expected the trigger's own error message, got: %v", err)
	}

	result, err := VerifyFull(ctx, conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected the chain to still verify clean after both writes were rejected, got failure at seq %d: %s", result.FailureAt, result.FailureReason)
	}
}
