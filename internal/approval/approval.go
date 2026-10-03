// Package approval implements durable human-in-the-loop approvals: a call
// matching an approval rule is paused, a notification goes out, and the
// original caller waits until a human decides or the approval expires.
//
// "Durable" here means two specific, separately-guaranteed things, not one
// vague promise:
//  1. The approval's state is never lost. It lives in Postgres from the
//     moment it's requested, so a crashed gateway doesn't erase a pending
//     decision - whoever restarts can still see and decide it.
//  2. The underlying action is never executed twice. ClaimExecution uses
//     an atomic compare-and-set on executed_at so, even if the same
//     approval is waited on by two concurrent or retried requests, only
//     one of them actually proceeds to call the real tool or model.
package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateRejected = "rejected"
	StateExpired  = "expired"
)

type CallSnapshot struct {
	AgentID      string `json:"agentId"`
	ActingAs     string `json:"actingAs"`
	Action       string `json:"action"`
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
}

type Approval struct {
	ID        string
	State     string
	ExpiresAt time.Time
}

// Detail is the full row, used by the control-api's read endpoints (and by
// the console, eventually) — Approval stays the minimal shape Request/
// WaitForDecision pass around internally.
type Detail struct {
	ID           string          `json:"id"`
	AgentID      string          `json:"agentId"`
	ActingAs     string          `json:"actingAs"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resourceType"`
	ResourceID   string          `json:"resourceId"`
	State        string          `json:"state"`
	DecidedBy    *string         `json:"decidedBy,omitempty"`
	ExecutedAt   *time.Time      `json:"executedAt,omitempty"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	CreatedAt    time.Time       `json:"createdAt"`
}

type Notifier interface {
	Notify(ctx context.Context, a Approval, snapshot CallSnapshot) error
}

type Manager struct {
	db       *sql.DB
	notifier Notifier
}

func New(db *sql.DB, notifier Notifier) *Manager {
	return &Manager{db: db, notifier: notifier}
}

// SeedRuleIfMissing registers (action, resourceType, resourceID) as
// requiring approval, if it isn't already. Safe to call on every startup.
func (m *Manager) SeedRuleIfMissing(ctx context.Context, action, resourceType, resourceID string) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO approval_rules (action, resource_type, resource_id) VALUES ($1, $2, $3)
		 ON CONFLICT (action, resource_type, resource_id) DO NOTHING`,
		action, resourceType, resourceID,
	)
	if err != nil {
		return fmt.Errorf("approval: seed rule: %w", err)
	}
	return nil
}

// RequiresApproval checks whether (action, resourceType, resourceID)
// matches an approval_rules row. This is independent of Cedar policy: a
// call can be policy-permitted and still require a human to sign off.
func (m *Manager) RequiresApproval(ctx context.Context, action, resourceType, resourceID string) (bool, error) {
	var exists bool
	err := m.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM approval_rules WHERE action = $1 AND resource_type = $2 AND resource_id = $3)`,
		action, resourceType, resourceID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("approval: check rule: %w", err)
	}
	return exists, nil
}

// Request creates a new approval, or - if idempotencyKey is non-empty and
// was already used - returns the existing one instead (its current state
// included, which may already be decided). Only sends a notification on
// genuine creation, detected via Postgres's standard "xmax = 0 means this
// row was just inserted, not returned by the ON CONFLICT branch" idiom.
func (m *Manager) Request(ctx context.Context, idempotencyKey string, snapshot CallSnapshot, ttl time.Duration) (Approval, error) {
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return Approval{}, fmt.Errorf("approval: marshal snapshot: %w", err)
	}

	var key any
	if idempotencyKey != "" {
		key = idempotencyKey
	} // else leave as nil -> SQL NULL -> never conflicts, always inserts fresh

	var a Approval
	var inserted bool
	err = m.db.QueryRowContext(ctx, `
		INSERT INTO approvals (idempotency_key, agent_id, acting_as, action, resource_type, resource_id, call_snapshot, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (idempotency_key) DO UPDATE SET id = approvals.id
		RETURNING id, state, expires_at, (xmax = 0) AS inserted`,
		key, snapshot.AgentID, snapshot.ActingAs, snapshot.Action, snapshot.ResourceType, snapshot.ResourceID,
		snapshotJSON, time.Now().Add(ttl),
	).Scan(&a.ID, &a.State, &a.ExpiresAt, &inserted)
	if err != nil {
		return Approval{}, fmt.Errorf("approval: request: %w", err)
	}

	if inserted {
		if err := m.notifier.Notify(ctx, a, snapshot); err != nil {
			slog.Error("approval notification failed", "approvalID", a.ID, "error", err)
		}
	}
	return a, nil
}

// WaitForDecision blocks until the approval leaves "pending" (approved,
// rejected, or expired) or ctx is done, whichever first. It polls rather
// than using Postgres LISTEN/NOTIFY - simpler to get right correctly, at
// the cost of up to one poll interval of added latency; see DECISIONS.md.
func (m *Manager) WaitForDecision(ctx context.Context, approvalID string, pollInterval time.Duration) (string, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		state, err := m.getState(ctx, approvalID)
		if err != nil {
			return "", err
		}
		if state != StatePending {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) getState(ctx context.Context, approvalID string) (string, error) {
	var state string
	var expiresAt time.Time
	err := m.db.QueryRowContext(ctx, `SELECT state, expires_at FROM approvals WHERE id = $1`, approvalID).
		Scan(&state, &expiresAt)
	if err != nil {
		return "", fmt.Errorf("approval: get state: %w", err)
	}
	if state == StatePending && time.Now().After(expiresAt) {
		// Lazily expire on read too, not just via the periodic sweep, so a
		// waiter doesn't have to wait for the next sweep tick after the
		// deadline has strictly already passed.
		if err := m.expireOne(ctx, approvalID); err != nil {
			return "", err
		}
		return StateExpired, nil
	}
	return state, nil
}

func (m *Manager) expireOne(ctx context.Context, approvalID string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE approvals SET state = $1, updated_at = now() WHERE id = $2 AND state = $3`,
		StateExpired, approvalID, StatePending,
	)
	return err
}

// Decide records a human decision. The WHERE state = 'pending' clause
// makes this atomic and idempotent at the database level: a second decide
// call (e.g. a double-clicked approve link) affects zero rows instead of
// overwriting an already-final decision.
func (m *Manager) Decide(ctx context.Context, approvalID, state, decidedBy string) error {
	if state != StateApproved && state != StateRejected {
		return fmt.Errorf("approval: invalid decision state %q", state)
	}
	res, err := m.db.ExecContext(ctx,
		`UPDATE approvals SET state = $1, decided_by = $2, updated_at = now()
		 WHERE id = $3 AND state = $4`,
		state, decidedBy, approvalID, StatePending,
	)
	if err != nil {
		return fmt.Errorf("approval: decide: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("approval: decide: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("approval: %s is not pending (already decided, or doesn't exist)", approvalID)
	}
	return nil
}

// ClaimExecution atomically marks the approval as executed. It returns
// claimed=true only for the caller that wins the race - everyone else
// (a retried request, a concurrent duplicate) gets claimed=false and must
// not call the downstream action again.
func (m *Manager) ClaimExecution(ctx context.Context, approvalID string) (claimed bool, err error) {
	res, err := m.db.ExecContext(ctx,
		`UPDATE approvals SET executed_at = now() WHERE id = $1 AND executed_at IS NULL`,
		approvalID,
	)
	if err != nil {
		return false, fmt.Errorf("approval: claim execution: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("approval: claim execution: %w", err)
	}
	return n == 1, nil
}

// ExpireOverdue sweeps every still-pending approval past its deadline.
// Call it periodically; WaitForDecision also expires lazily on read, so
// this sweep mainly catches approvals nobody is actively waiting on.
func (m *Manager) ExpireOverdue(ctx context.Context) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`UPDATE approvals SET state = $1, updated_at = now() WHERE state = $2 AND expires_at < now()`,
		StateExpired, StatePending,
	)
	if err != nil {
		return 0, fmt.Errorf("approval: expire sweep: %w", err)
	}
	return res.RowsAffected()
}

// Get returns the full detail row for one approval.
func (m *Manager) Get(ctx context.Context, approvalID string) (Detail, error) {
	var d Detail
	err := m.db.QueryRowContext(ctx, `
		SELECT id, agent_id, acting_as, action, resource_type, resource_id,
		       state, decided_by, executed_at, expires_at, created_at
		FROM approvals WHERE id = $1`, approvalID,
	).Scan(&d.ID, &d.AgentID, &d.ActingAs, &d.Action, &d.ResourceType, &d.ResourceID,
		&d.State, &d.DecidedBy, &d.ExecutedAt, &d.ExpiresAt, &d.CreatedAt)
	if err != nil {
		return Detail{}, fmt.Errorf("approval: get: %w", err)
	}
	return d, nil
}

// List returns approvals in a given state (or every approval if state is
// empty), most recent first.
func (m *Manager) List(ctx context.Context, state string) ([]Detail, error) {
	query := `SELECT id, agent_id, acting_as, action, resource_type, resource_id,
	                  state, decided_by, executed_at, expires_at, created_at
	           FROM approvals`
	args := []any{}
	if state != "" {
		query += ` WHERE state = $1`
		args = append(args, state)
	}
	query += ` ORDER BY created_at DESC LIMIT 200`

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("approval: list: %w", err)
	}
	defer rows.Close()

	var out []Detail
	for rows.Next() {
		var d Detail
		if err := rows.Scan(&d.ID, &d.AgentID, &d.ActingAs, &d.Action, &d.ResourceType, &d.ResourceID,
			&d.State, &d.DecidedBy, &d.ExecutedAt, &d.ExpiresAt, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("approval: list scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (m *Manager) StartExpirySweep(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := m.ExpireOverdue(ctx); err != nil {
					slog.Error("approval expiry sweep failed", "error", err)
				} else if n > 0 {
					slog.Info("approval expiry sweep", "expired", n)
				}
			}
		}
	}()
}
