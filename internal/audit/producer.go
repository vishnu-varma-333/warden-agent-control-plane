package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer publishes an audit event. Publish is fire-and-forget from the
// caller's point of view — it must never block the hot path on Kafka's
// ack, which is the entire reason audit events go through Kafka instead of
// a direct, synchronous Postgres write (see DECISIONS.md).
type Producer interface {
	Publish(ctx context.Context, e Event)
}

type KafkaProducer struct {
	client *kgo.Client
	topic  string
	bgCtx  context.Context
}

// NewKafkaProducer takes bgCtx separately from any future Publish call's
// ctx: bgCtx should live for the whole process (e.g. the context main()
// cancels on SIGTERM), not any single request. This matters because
// Produce is asynchronous — the actual network send often happens AFTER
// the HTTP handler that triggered it has already returned, by which point
// that request's own context is cancelled. Using the caller's per-request
// ctx for the underlying send (the first version of this code did)
// produced "context canceled" on essentially every publish, caught during
// this milestone's own live verification, not a test.
func NewKafkaProducer(bgCtx context.Context, brokers []string, topic string) (*KafkaProducer, error) {
	// AllowAutoTopicCreation is a local-dev convenience so a fresh
	// environment doesn't need a manual `rpk topic create` step; a real
	// deployment should provision topics explicitly (Terraform, alongside
	// the rest of the AWS infra - milestone 10), not rely on this.
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.AllowAutoTopicCreation())
	if err != nil {
		return nil, fmt.Errorf("audit: new kafka client: %w", err)
	}
	return &KafkaProducer{client: client, topic: topic, bgCtx: bgCtx}, nil
}

func (p *KafkaProducer) Publish(_ context.Context, e Event) {
	if e.EventID == "" {
		e.EventID = uuid.NewString()
	}
	b, err := json.Marshal(e)
	if err != nil {
		slog.Error("audit: marshal event failed", "error", err)
		return
	}
	// Produce (not ProduceSync): returns immediately, the promise runs
	// later when the broker acks. The decision that triggered this event
	// has already been made and returned to its caller by this point —
	// publishing the audit trail is not allowed to add latency to that.
	// Uses p.bgCtx, deliberately not the ctx passed to this call — see the
	// constructor's doc comment for why.
	p.client.Produce(p.bgCtx, &kgo.Record{Topic: p.topic, Key: []byte(e.EventID), Value: b},
		func(_ *kgo.Record, err error) {
			if err != nil {
				slog.Error("audit: publish failed", "eventID", e.EventID, "error", err)
			}
		},
	)
}

func (p *KafkaProducer) Close() { p.client.Close() }

// NoopProducer discards events — used where audit publishing isn't the
// thing under test (e.g. policy/approval unit tests that predate this
// milestone and shouldn't need a real Kafka broker to run).
type NoopProducer struct{}

func (NoopProducer) Publish(context.Context, Event) {}
