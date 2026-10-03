CREATE TABLE IF NOT EXISTS approvals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Client-supplied, not server-derived: only the caller knows whether a
    -- retry is "the same operation again" or "a new, separately-approvable
    -- action that happens to look identical." NULL (no key supplied) never
    -- collides with anything in Postgres, so a caller that opts out of
    -- idempotency gets a fresh approval every time, which is the safe
    -- default.
    idempotency_key TEXT UNIQUE,

    agent_id        TEXT NOT NULL,
    acting_as       TEXT NOT NULL,
    action          TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_id     TEXT NOT NULL,
    call_snapshot   JSONB NOT NULL,

    state           TEXT NOT NULL DEFAULT 'pending'
                        CHECK (state IN ('pending', 'approved', 'rejected', 'expired')),
    decided_by      TEXT,

    -- Set exactly once, atomically, right before the downstream call is
    -- actually made. Whoever's UPDATE sets this first is the only one that
    -- proceeds to execute - see internal/approval's ClaimExecution.
    executed_at     TIMESTAMPTZ,

    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_approvals_pending_expiry
    ON approvals (expires_at)
    WHERE state = 'pending';

-- Which (action, resource) pairs require human approval, independent of
-- whether Cedar policy already permits them - approval is an additional
-- gate on top of normal authorization, not a replacement for it.
CREATE TABLE IF NOT EXISTS approval_rules (
    action        TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    PRIMARY KEY (action, resource_type, resource_id)
);
