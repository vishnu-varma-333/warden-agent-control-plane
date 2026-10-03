package audit

import (
	"context"
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

func TestVerifyDetectsAnEditedRecord(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 5)
	ctx := context.Background()

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
