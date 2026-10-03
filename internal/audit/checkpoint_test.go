package audit

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"testing"
)

func TestCheckpointAndVerifyFromItFastPath(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 5)
	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cp := NewCheckpointer(conn, priv)
	if err := cp.CreateCheckpoint(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// More records after the checkpoint - the fast path must still verify
	// these, not just stop at the checkpoint.
	seedChain(t, w, 3)

	result, err := VerifyFromLatestCheckpoint(ctx, conn, pub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got failure at seq %d: %s", result.FailureAt, result.FailureReason)
	}
	if result.StartSeq != 6 {
		t.Fatalf("expected the fast path to start checking at seq 6 (right after the checkpoint at seq 5), got %d", result.StartSeq)
	}
	if result.RecordsVerified != 3 {
		t.Fatalf("expected only the 3 post-checkpoint records to be walked, got %d", result.RecordsVerified)
	}
}

func TestVerifyFromLatestCheckpointRejectsForgedSignature(t *testing.T) {
	conn := testDB(t)
	w := &ChainWriter{db: conn}
	seedChain(t, w, 3)
	ctx := context.Background()

	realPub, realPriv, _ := ed25519.GenerateKey(nil)
	cp := NewCheckpointer(conn, realPriv)
	if err := cp.CreateCheckpoint(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Simulate an attacker forging a checkpoint signature with a DIFFERENT
	// key, hoping VerifyFromLatestCheckpoint will trust it blindly.
	_, forgedPriv, _ := ed25519.GenerateKey(nil)
	forgedSig := ed25519.Sign(forgedPriv, checkpointMessage(3, mustGetHash(t, conn, 3)))
	conn.ExecContext(ctx, `UPDATE audit_checkpoints SET signature = $1 WHERE seq = 3`, hex.EncodeToString(forgedSig))

	result, err := VerifyFromLatestCheckpoint(ctx, conn, realPub) // verifying against the REAL public key
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OK {
		t.Fatal("expected a forged checkpoint signature to be rejected, got OK")
	}
}

func mustGetHash(t *testing.T, conn *sql.DB, seq int64) string {
	t.Helper()
	var hash string
	if err := conn.QueryRowContext(context.Background(), `SELECT hash FROM audit_events WHERE seq = $1`, seq).Scan(&hash); err != nil {
		t.Fatalf("get hash: %v", err)
	}
	return hash
}
