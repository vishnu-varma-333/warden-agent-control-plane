CREATE TABLE IF NOT EXISTS policies (
    version      INT PRIMARY KEY,
    cedar_source TEXT NOT NULL,
    active       BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one active version at a time, enforced by the database itself
-- (not just application logic): a partial unique index over rows where
-- active is true means a second concurrent "activate" can't both succeed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_policies_one_active
    ON policies (active)
    WHERE active;
