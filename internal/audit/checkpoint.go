package audit

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"
)

// Checkpointer periodically signs the current chain tip. A checkpoint lets
// verification trust everything up to it without re-walking every record —
// see Verify's doc comment for why that's sound: the signature is what's
// actually being trusted, not the checkpoint row itself.
type Checkpointer struct {
	db      *sql.DB
	private ed25519.PrivateKey
}

func NewCheckpointer(db *sql.DB, private ed25519.PrivateKey) *Checkpointer {
	return &Checkpointer{db: db, private: private}
}

// CreateCheckpoint locks the chain state (consistent with how Append does,
// so a checkpoint never signs a tip that a concurrent append is mid-way
// through changing), signs it, and stores it.
func (c *Checkpointer) CreateCheckpoint(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit: checkpoint begin tx: %w", err)
	}
	defer tx.Rollback()

	var seq int64
	var hash string
	if err := tx.QueryRowContext(ctx, `SELECT last_seq, last_hash FROM audit_chain_state WHERE id = 1 FOR UPDATE`).
		Scan(&seq, &hash); err != nil {
		return fmt.Errorf("audit: checkpoint lock chain state: %w", err)
	}
	if seq == 0 {
		return nil // nothing recorded yet; no tip to checkpoint
	}

	sig := ed25519.Sign(c.private, checkpointMessage(seq, hash))

	_, err = tx.ExecContext(ctx,
		`INSERT INTO audit_checkpoints (seq, root_hash, signature) VALUES ($1, $2, $3)
		 ON CONFLICT (seq) DO NOTHING`,
		seq, hash, hex.EncodeToString(sig),
	)
	if err != nil {
		return fmt.Errorf("audit: insert checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("audit: checkpoint commit: %w", err)
	}
	slog.Info("audit checkpoint created", "seq", seq, "hash", hash)
	return nil
}

func (c *Checkpointer) StartPeriodic(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.CreateCheckpoint(ctx); err != nil {
					slog.Error("audit: periodic checkpoint failed", "error", err)
				}
			}
		}
	}()
}

// checkpointMessage is what actually gets signed — shared between signing
// and verification so there's one definition of "what a checkpoint
// attests to," the same reasoning as Record/ComputeHash being shared
// between the writer and the verifier.
func checkpointMessage(seq int64, hash string) []byte {
	return []byte(fmt.Sprintf("%d:%s", seq, hash))
}
