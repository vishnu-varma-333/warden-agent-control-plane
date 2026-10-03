package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// ChainWriter is the single consumer that turns published events into
// chained, hashed rows. Running more than one instance is fine for
// correctness — not just tolerated, but actually fine — because the
// serialization point isn't "only one consumer process exists," it's the
// SELECT ... FOR UPDATE on audit_chain_state inside Append: whichever
// consumer gets there first holds the lock until it commits, so two
// consumers racing on the same Kafka partition still can't produce two
// records claiming the same prev_hash.
type ChainWriter struct {
	db     *sql.DB
	client *kgo.Client
	topic  string
}

func NewChainWriter(db *sql.DB, brokers []string, topic string) (*ChainWriter, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup("warden-audit-chain-writer"),
	)
	if err != nil {
		return nil, fmt.Errorf("audit: new kafka consumer: %w", err)
	}
	return &ChainWriter{db: db, client: client, topic: topic}, nil
}

func (w *ChainWriter) Close() { w.client.Close() }

// Run polls Kafka and appends each event until ctx is cancelled.
func (w *ChainWriter) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		fetches := w.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			slog.Error("audit: fetch error", "topic", topic, "partition", partition, "error", err)
		})
		fetches.EachRecord(func(r *kgo.Record) {
			var e Event
			if err := json.Unmarshal(r.Value, &e); err != nil {
				slog.Error("audit: decode event failed, skipping", "error", err)
				return
			}
			if err := w.Append(ctx, e); err != nil {
				slog.Error("audit: append failed", "eventID", e.EventID, "error", err)
			}
		})
	}
}

// Append chains one event onto Postgres, atomically. Idempotent: if
// event.EventID was already recorded (Kafka redelivered it), this is a
// no-op, not a duplicate chain entry.
func (w *ChainWriter) Append(ctx context.Context, e Event) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit: begin tx: %w", err)
	}
	defer tx.Rollback()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE event_id = $1)`, e.EventID).Scan(&exists); err != nil {
		return fmt.Errorf("audit: dedup check: %w", err)
	}
	if exists {
		return nil
	}

	var lastSeq int64
	var lastHash string
	if err := tx.QueryRowContext(ctx, `SELECT last_seq, last_hash FROM audit_chain_state WHERE id = 1 FOR UPDATE`).
		Scan(&lastSeq, &lastHash); err != nil {
		return fmt.Errorf("audit: lock chain state: %w", err)
	}

	rec := Record{
		Seq: lastSeq + 1, PrevHash: lastHash, EventID: e.EventID,
		Decision: e.Decision, Reason: e.Reason, AgentID: e.AgentID, ActingAs: e.ActingAs,
		Action: e.Action, ResourceType: e.ResourceType, ResourceID: e.ResourceID,
		PayloadRef: e.PayloadRef,
		// Postgres TIMESTAMPTZ only stores microsecond precision, while
		// Go's time.Now() carries nanoseconds; hashing the full-precision
		// value here and reading back a truncated one in Verify would make
		// every untampered record look edited. Normalizing to what
		// Postgres will actually store, before hashing, is what makes the
		// two sides agree.
		OccurredAt: e.OccurredAt.UTC().Truncate(time.Microsecond),
	}
	hash := ComputeHash(rec)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_events (seq, prev_hash, hash, event_id, decision, reason, agent_id, acting_as, action, resource_type, resource_id, payload_ref, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		rec.Seq, rec.PrevHash, hash, rec.EventID, rec.Decision, rec.Reason, rec.AgentID, rec.ActingAs,
		rec.Action, rec.ResourceType, rec.ResourceID, rec.PayloadRef, rec.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("audit: insert record: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE audit_chain_state SET last_seq = $1, last_hash = $2 WHERE id = 1`, rec.Seq, hash); err != nil {
		return fmt.Errorf("audit: update chain state: %w", err)
	}

	return tx.Commit()
}
