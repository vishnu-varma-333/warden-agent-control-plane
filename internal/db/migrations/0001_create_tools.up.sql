CREATE TABLE IF NOT EXISTS tools (
    mcp_server      TEXT NOT NULL,
    name            TEXT NOT NULL,
    -- The hash currently trusted/pinned. A tool stays callable only while
    -- the live definition's hash matches this one.
    definition_hash TEXT NOT NULL,
    -- Set when a live definition's hash no longer matches definition_hash;
    -- holds the new hash until a human approval promotes it.
    pending_hash    TEXT,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'changed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (mcp_server, name)
);
