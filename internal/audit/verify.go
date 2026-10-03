package audit

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

type VerifyResult struct {
	OK              bool
	RecordsVerified int64
	StartSeq        int64 // first seq actually checked (1 for full verification)
	FailureAt       int64 // seq where a mismatch was found; 0 if OK
	FailureReason   string
	Duration        time.Duration
}

// Verify walks the chain from (fromSeq+1) onward, trusting fromHash as the
// prev_hash of the first record it checks, and recomputes every hash along
// the way. fromSeq=0, fromHash="" verifies the entire chain from genesis.
//
// This is the actual tamper-evidence proof, not just a description of one:
//   - An EDITED record fails immediately — its stored hash won't match the
//     hash recomputed from its (possibly altered) content.
//   - A DELETED record breaks the seq sequence (a gap) or, if the deletion
//     was disguised by also renumbering everything after it, the next
//     record's prev_hash won't match the hash actually computed for its
//     new "predecessor."
//   - A REORDERED pair is caught the same way: record B's prev_hash was
//     computed against the real A, so swapping A and B (or inserting a
//     forged record between them) produces a prev_hash that doesn't match
//     what Verify independently recomputes for whatever now precedes it.
//
// In short: there's no way to alter one record without it being
// inconsistent with either its own stored hash or its neighbor's, and this
// function is what actually checks that, rather than asserting it.
func Verify(ctx context.Context, db *sql.DB, fromSeq int64, fromHash string) (VerifyResult, error) {
	start := time.Now()
	rows, err := db.QueryContext(ctx, `
		SELECT seq, prev_hash, hash, event_id, decision, reason, agent_id, acting_as, action, resource_type, resource_id, payload_ref, occurred_at
		FROM audit_events WHERE seq > $1 ORDER BY seq ASC`, fromSeq)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("audit: verify query: %w", err)
	}
	defer rows.Close()

	expectedSeq := fromSeq + 1
	expectedPrevHash := fromHash
	var count int64

	for rows.Next() {
		var rec Record
		var storedHash string
		if err := rows.Scan(&rec.Seq, &rec.PrevHash, &storedHash, &rec.EventID, &rec.Decision, &rec.Reason,
			&rec.AgentID, &rec.ActingAs, &rec.Action, &rec.ResourceType, &rec.ResourceID, &rec.PayloadRef, &rec.OccurredAt); err != nil {
			return VerifyResult{}, fmt.Errorf("audit: verify scan: %w", err)
		}

		if rec.Seq != expectedSeq {
			return VerifyResult{OK: false, RecordsVerified: count, StartSeq: fromSeq + 1,
				FailureAt: rec.Seq, FailureReason: fmt.Sprintf("expected seq %d, found %d (a record was deleted or reordered)", expectedSeq, rec.Seq),
				Duration: time.Since(start)}, nil
		}
		if rec.PrevHash != expectedPrevHash {
			return VerifyResult{OK: false, RecordsVerified: count, StartSeq: fromSeq + 1,
				FailureAt: rec.Seq, FailureReason: "prev_hash does not match the preceding record's actual hash (chain broken: a record was altered, deleted, or reordered)",
				Duration: time.Since(start)}, nil
		}

		recomputed := ComputeHash(rec)
		if recomputed != storedHash {
			return VerifyResult{OK: false, RecordsVerified: count, StartSeq: fromSeq + 1,
				FailureAt: rec.Seq, FailureReason: "stored hash does not match the hash recomputed from this record's own content (this record was edited)",
				Duration: time.Since(start)}, nil
		}

		expectedSeq++
		expectedPrevHash = storedHash
		count++
	}
	if err := rows.Err(); err != nil {
		return VerifyResult{}, fmt.Errorf("audit: verify rows: %w", err)
	}

	return VerifyResult{OK: true, RecordsVerified: count, StartSeq: fromSeq + 1, Duration: time.Since(start)}, nil
}

func VerifyFull(ctx context.Context, db *sql.DB) (VerifyResult, error) {
	return Verify(ctx, db, 0, "")
}

// VerifyFromLatestCheckpoint is the fast path: instead of recomputing
// every record from genesis, it trusts the latest signed checkpoint (after
// verifying ITS signature) and only walks the chain from there forward.
// This is the direct answer to "what does a checkpoint prove, and how much
// faster is verification with one": the checkpoint's Ed25519 signature
// proves Warden itself produced it (nobody without the private key could
// forge a valid signature over a fabricated seq/hash pair), which is what
// licenses skipping re-verification of everything before it.
func VerifyFromLatestCheckpoint(ctx context.Context, db *sql.DB, public ed25519.PublicKey) (VerifyResult, error) {
	var seq int64
	var hash, sigHex string
	err := db.QueryRowContext(ctx, `SELECT seq, root_hash, signature FROM audit_checkpoints ORDER BY seq DESC LIMIT 1`).
		Scan(&seq, &hash, &sigHex)
	if err == sql.ErrNoRows {
		return VerifyFull(ctx, db) // no checkpoint yet: nothing to fast-forward from
	}
	if err != nil {
		return VerifyResult{}, fmt.Errorf("audit: load latest checkpoint: %w", err)
	}

	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("audit: decode checkpoint signature: %w", err)
	}
	if !ed25519.Verify(public, checkpointMessage(seq, hash), sig) {
		return VerifyResult{OK: false, FailureAt: seq, FailureReason: "checkpoint signature is invalid — cannot trust this checkpoint at all, falling back is required"}, nil
	}

	// The checkpoint's own claim about record `seq` must also match what's
	// actually stored there — a valid signature only proves Warden signed
	// SOME (seq, hash) pair; confirming that pair still matches the live
	// row is what ties the trusted signature to the data being verified.
	var storedHash string
	if err := db.QueryRowContext(ctx, `SELECT hash FROM audit_events WHERE seq = $1`, seq).Scan(&storedHash); err != nil {
		return VerifyResult{}, fmt.Errorf("audit: load checkpointed record: %w", err)
	}
	if storedHash != hash {
		return VerifyResult{OK: false, FailureAt: seq, FailureReason: "record at the checkpointed seq no longer matches the checkpoint's signed hash"}, nil
	}

	return Verify(ctx, db, seq, hash)
}
