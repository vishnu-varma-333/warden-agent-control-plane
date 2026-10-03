-- A singleton row that every append locks (SELECT ... FOR UPDATE) to
-- serialize hash-chain writes: whoever holds this row's lock is the only
-- writer that can compute the next (seq, prev_hash) pair, so concurrent
-- appends — even from multiple consumer processes — can never race to
-- produce two records claiming the same predecessor.
CREATE TABLE IF NOT EXISTS audit_chain_state (
    id        INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_seq  BIGINT NOT NULL DEFAULT 0,
    last_hash TEXT NOT NULL DEFAULT ''
);
INSERT INTO audit_chain_state (id, last_seq, last_hash)
    VALUES (1, 0, '')
    ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS audit_events (
    seq           BIGINT PRIMARY KEY,
    prev_hash     TEXT NOT NULL,
    hash          TEXT NOT NULL,

    -- event_id dedupes Kafka's at-least-once delivery: if the chain writer
    -- crashes after committing but before the consumer offset is marked,
    -- the same message is redelivered. Appending it twice would be wrong
    -- (two chain entries for one real decision); event_id lets the writer
    -- detect "already recorded" and skip, not double-append.
    event_id      TEXT NOT NULL UNIQUE,

    decision      TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    agent_id      TEXT NOT NULL,
    acting_as     TEXT NOT NULL,
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    payload_ref   TEXT NOT NULL DEFAULT '',
    occurred_at   TIMESTAMPTZ NOT NULL,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_checkpoints (
    seq        BIGINT PRIMARY KEY,
    root_hash  TEXT NOT NULL,
    signature  TEXT NOT NULL, -- hex-encoded Ed25519 signature over "seq:root_hash"
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
