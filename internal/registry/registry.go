// Package registry is Warden's record of every tool it has ever seen from
// every upstream MCP server, keyed by a hash of that tool's definition. It
// implements "pin on first sight, block on silent change" — the mechanism
// behind the tool-poisoning defense that's the project's core pitch.
package registry

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	StatusActive  = "active"
	StatusChanged = "changed"
)

type Registry struct {
	db *sql.DB
}

func New(db *sql.DB) *Registry {
	return &Registry{db: db}
}

// Reconcile is called every time Warden observes a tool's current
// definition hash from an upstream. It returns the resulting status:
//
//   - First time seeing (server, name): pin this hash, status = active.
//   - Already active and hash matches the pinned one: stays active.
//   - Already active and hash DIFFERS: flip to changed, remember the new
//     hash as pending (not yet trusted), don't overwrite the pinned one.
//   - Already changed: stays changed regardless of what the live hash does
//     next — including if it flips back to matching the original. This is
//     deliberate: an attacker alternating between the real and poisoned
//     definition shouldn't be able to "heal" the block just by reverting
//     before anyone looks. Only Approve clears it.
func (r *Registry) Reconcile(ctx context.Context, server, name, hash string) (status string, err error) {
	var pinnedHash, currentStatus string
	err = r.db.QueryRowContext(ctx,
		`SELECT definition_hash, status FROM tools WHERE mcp_server = $1 AND name = $2`,
		server, name,
	).Scan(&pinnedHash, &currentStatus)

	switch {
	case err == sql.ErrNoRows:
		_, err = r.db.ExecContext(ctx,
			`INSERT INTO tools (mcp_server, name, definition_hash, status) VALUES ($1, $2, $3, $4)`,
			server, name, hash, StatusActive,
		)
		if err != nil {
			return "", fmt.Errorf("registry: pin new tool: %w", err)
		}
		return StatusActive, nil

	case err != nil:
		return "", fmt.Errorf("registry: lookup: %w", err)

	case currentStatus == StatusChanged:
		_, err = r.db.ExecContext(ctx,
			`UPDATE tools SET pending_hash = $1, updated_at = now() WHERE mcp_server = $2 AND name = $3`,
			hash, server, name,
		)
		return StatusChanged, err

	case hash != pinnedHash:
		_, err = r.db.ExecContext(ctx,
			`UPDATE tools SET status = $1, pending_hash = $2, updated_at = now() WHERE mcp_server = $3 AND name = $4`,
			StatusChanged, hash, server, name,
		)
		return StatusChanged, err

	default:
		return StatusActive, nil
	}
}

// Status returns the current status for a known tool, or ok=false if
// Warden has never seen it (e.g. it was never registered via Reconcile).
func (r *Registry) Status(ctx context.Context, server, name string) (status string, ok bool, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT status FROM tools WHERE mcp_server = $1 AND name = $2`, server, name,
	).Scan(&status)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("registry: status: %w", err)
	}
	return status, true, nil
}

// Approve promotes a changed tool's pending hash to the trusted one and
// clears the block. Stands in for the real human-approval flow (console +
// durable approvals) that milestone 6 builds; see DECISIONS.md.
func (r *Registry) Approve(ctx context.Context, server, name string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE tools
		 SET definition_hash = COALESCE(pending_hash, definition_hash),
		     pending_hash = NULL,
		     status = $1,
		     updated_at = now()
		 WHERE mcp_server = $2 AND name = $3`,
		StatusActive, server, name,
	)
	if err != nil {
		return fmt.Errorf("registry: approve: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("registry: approve: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("registry: approve: no tool %s/%s", server, name)
	}
	return nil
}
