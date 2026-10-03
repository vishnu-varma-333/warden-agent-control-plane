// Package audit implements Warden's tamper-evident audit log: every
// decision is published to Kafka/Redpanda (off the hot path), a single
// chain-writer consumer appends each one to Postgres with a SHA-256 hash
// chained to its predecessor, and a verification pass can prove — not
// just claim — that no record has been edited, deleted, or reordered
// since it was written.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Event is what gets published to Kafka — the decision as it happened,
// before it has a place in the chain yet (no seq/hash: those only exist
// once the chain writer assigns them).
type Event struct {
	EventID      string    `json:"eventId"` // generated at publish time; see record_id dedup note in the migration
	Decision     string    `json:"decision"`
	Reason       string    `json:"reason"`
	AgentID      string    `json:"agentId"`
	ActingAs     string    `json:"actingAs"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	PayloadRef   string    `json:"payloadRef"`
	OccurredAt   time.Time `json:"occurredAt"`
}

// Record is one row of the chain, as actually stored — this is the
// canonical shape both the chain writer (computing a new hash) and the
// verifier (recomputing an existing one) hash over. They share this type
// specifically so there's exactly one place that defines "what counts as
// the record's content" — a mismatch between write-time and verify-time
// hashing would make every record look tampered.
type Record struct {
	Seq          int64     `json:"seq"`
	PrevHash     string    `json:"prevHash"`
	Hash         string    `json:"hash"` // populated by readers (e.g. ListRecent); ComputeHash ignores it, it's never part of what gets hashed
	EventID      string    `json:"eventId"`
	Decision     string    `json:"decision"`
	Reason       string    `json:"reason"`
	AgentID      string    `json:"agentId"`
	ActingAs     string    `json:"actingAs"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	PayloadRef   string    `json:"payloadRef"`
	OccurredAt   time.Time `json:"occurredAt"`
}

// ComputeHash is the entire tamper-evidence mechanism: hash = SHA256(prev_hash
// || canonical_json(record)). Changing anything about a record — including
// something subtle like its reason text — changes its hash, which breaks
// the hash of every record after it, all the way to the current tip. That
// propagation is *why* a single-record edit is detectable without having
// to separately checksum every record against some external log: tampering
// with record N invalidates N, N+1, N+2, ... all the way to HEAD.
func ComputeHash(r Record) string {
	// .UTC() matters here, not just as a style choice: pgx's stdlib driver
	// decodes a TIMESTAMPTZ column back as time.Time in the server process's
	// LOCAL location (e.g. IST), not UTC — Equal() still holds (it's the
	// same instant), but json.Marshal's RFC3339 output differs by
	// Location ("...Z" vs "...+05:30"), which would make every untampered
	// record fail verification simply because it was re-read on a machine
	// in a different timezone than the one that wrote it. Normalizing here,
	// at the one place both the writer and the verifier compute a hash,
	// makes the hash depend only on the actual instant, never on which
	// process or timezone happened to read it back.
	occurredAt := r.OccurredAt.UTC()

	// A fixed field order via a struct (not a map) makes this
	// deterministic — json.Marshal of a struct always emits fields in
	// declaration order, unlike a map (which Go deliberately randomizes
	// iteration order for, though encoding/json happens to sort map keys;
	// using a struct avoids relying on that incidental behavior).
	canonical := struct {
		Seq          int64     `json:"seq"`
		PrevHash     string    `json:"prevHash"`
		EventID      string    `json:"eventId"`
		Decision     string    `json:"decision"`
		Reason       string    `json:"reason"`
		AgentID      string    `json:"agentId"`
		ActingAs     string    `json:"actingAs"`
		Action       string    `json:"action"`
		ResourceType string    `json:"resourceType"`
		ResourceID   string    `json:"resourceId"`
		PayloadRef   string    `json:"payloadRef"`
		OccurredAt   time.Time `json:"occurredAt"`
	}{
		Seq: r.Seq, PrevHash: r.PrevHash, EventID: r.EventID,
		Decision: r.Decision, Reason: r.Reason, AgentID: r.AgentID, ActingAs: r.ActingAs,
		Action: r.Action, ResourceType: r.ResourceType, ResourceID: r.ResourceID,
		PayloadRef: r.PayloadRef, OccurredAt: occurredAt,
	}
	b, _ := json.Marshal(canonical) // fixed shape, cannot fail
	sum := sha256.Sum256(append([]byte(r.PrevHash), b...))
	return hex.EncodeToString(sum[:])
}
