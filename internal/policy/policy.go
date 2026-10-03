// Package policy is Warden's actual authorization decision-maker: given a
// principal, action and resource, it answers allow or deny against the
// currently active Cedar policy, and logs every decision with its reason.
//
// Unlike rate limiting and budgets (which fail open — see DECISIONS.md),
// this package fails CLOSED: if no policy has ever loaded successfully,
// every request is denied. An authorization gateway that quietly stops
// enforcing anything during an outage isn't a gateway, it's a bypass.
package policy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
	"github.com/redis/go-redis/v9"
)

// DefaultSeedPolicy is installed only if the policies table is empty, so a
// fresh environment has something to evaluate against instead of denying
// everything forever. It also doubles as a live demonstration of Cedar's
// permit/forbid/default-deny semantics: mock-model and the echo tool are
// permitted in general, echo is explicitly forbidden when acting as
// user-2 (forbid overrides permit), and anything not named here is denied
// by Cedar's own default — no explicit rule required.
const DefaultSeedPolicy = `permit(
    principal,
    action == Warden::Action::"CallModel",
    resource == Warden::Model::"mock-model"
);

permit(
    principal,
    action == Warden::Action::"CallTool",
    resource == Warden::Tool::"echo"
);

permit(
    principal,
    action == Warden::Action::"CallTool",
    resource == Warden::Tool::"delete_data"
);

forbid(
    principal,
    action == Warden::Action::"CallTool",
    resource == Warden::Tool::"echo"
) when {
    context.actingAs == "user-2"
};
`

type Decision struct {
	Allow         bool
	PolicyVersion int
	Reasons       []string
	CacheHit      bool
}

type Engine struct {
	db    *sql.DB
	cache *redis.Client

	mu        sync.RWMutex
	compiled  *cedar.PolicySet
	version   int
	everReady bool
}

func New(db *sql.DB, cache *redis.Client) *Engine {
	return &Engine{db: db, cache: cache}
}

// SeedIfEmpty installs DefaultSeedPolicy as version 1 only if no policy
// rows exist yet. Safe to call on every startup.
func (e *Engine) SeedIfEmpty(ctx context.Context) error {
	var count int
	if err := e.db.QueryRowContext(ctx, `SELECT count(*) FROM policies`).Scan(&count); err != nil {
		return fmt.Errorf("policy: count existing: %w", err)
	}
	if count > 0 {
		return nil
	}
	_, err := e.db.ExecContext(ctx,
		`INSERT INTO policies (version, cedar_source, active) VALUES (1, $1, true)`,
		DefaultSeedPolicy,
	)
	if err != nil {
		return fmt.Errorf("policy: seed: %w", err)
	}
	slog.Info("seeded default policy", "version", 1)
	return nil
}

// Refresh loads the currently active policy from Postgres and compiles it,
// replacing the in-memory copy atomically. Call it at startup and on an
// interval (StartPeriodicRefresh) so a newly-activated version takes effect
// without a gateway restart.
func (e *Engine) Refresh(ctx context.Context) error {
	var version int
	var source string
	err := e.db.QueryRowContext(ctx,
		`SELECT version, cedar_source FROM policies WHERE active = true`,
	).Scan(&version, &source)
	if err != nil {
		return fmt.Errorf("policy: load active: %w", err)
	}

	e.mu.RLock()
	unchanged := e.everReady && version == e.version
	e.mu.RUnlock()
	if unchanged {
		return nil
	}

	ps, err := cedar.NewPolicySetFromBytes(fmt.Sprintf("policy-v%d.cedar", version), []byte(source))
	if err != nil {
		return fmt.Errorf("policy: compile version %d: %w", version, err)
	}

	e.mu.Lock()
	e.compiled = ps
	e.version = version
	e.everReady = true
	e.mu.Unlock()

	slog.Info("policy engine loaded active version", "version", version)
	return nil
}

func (e *Engine) StartPeriodicRefresh(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := e.Refresh(ctx); err != nil {
					slog.Error("policy periodic refresh failed", "error", err)
				}
			}
		}
	}()
}

// Authorize decides whether agentID (acting as actingAsUser) may perform
// action on a resource of the given type. action/resourceType/resourceID
// map directly onto Cedar entity types: e.g. ("CallModel", "Model",
// "mock-model") or ("CallTool", "Tool", "echo").
func (e *Engine) Authorize(ctx context.Context, agentID, actingAsUser, action, resourceType, resourceID string) (Decision, error) {
	e.mu.RLock()
	compiled, version, ready := e.compiled, e.version, e.everReady
	e.mu.RUnlock()

	if !ready {
		slog.Error("policy decision: DENY (fail-closed, no policy ever loaded)",
			"agent", agentID, "action", action, "resource", resourceType+"::"+resourceID)
		return Decision{Allow: false, Reasons: []string{"no policy loaded"}}, nil
	}

	cacheKey := decisionCacheKey(version, agentID, actingAsUser, action, resourceType, resourceID)
	if cached, hit := e.getCached(ctx, cacheKey); hit {
		cached.CacheHit = true
		logDecision(cached, agentID, actingAsUser, action, resourceType, resourceID)
		return cached, nil
	}

	req := cedar.Request{
		Principal: types.NewEntityUID(types.EntityType("Warden::Agent"), types.String(agentID)),
		Action:    types.NewEntityUID(types.EntityType("Warden::Action"), types.String(action)),
		Resource:  types.NewEntityUID(types.EntityType("Warden::"+resourceType), types.String(resourceID)),
		Context: types.NewRecord(types.RecordMap{
			"actingAs": types.String(actingAsUser),
		}),
	}

	cedarDecision, diagnostic := compiled.IsAuthorized(types.EntityMap{}, req)

	var reasons []string
	for _, r := range diagnostic.Reasons {
		reasons = append(reasons, string(r.PolicyID))
	}
	for _, errReason := range diagnostic.Errors {
		reasons = append(reasons, errReason.String())
	}

	decision := Decision{
		Allow:         cedarDecision == cedar.Allow,
		PolicyVersion: version,
		Reasons:       reasons,
	}

	e.setCached(ctx, cacheKey, decision)
	logDecision(decision, agentID, actingAsUser, action, resourceType, resourceID)
	return decision, nil
}

func logDecision(d Decision, agentID, actingAsUser, action, resourceType, resourceID string) {
	level := slog.LevelInfo
	if !d.Allow {
		level = slog.LevelWarn
	}
	slog.Log(context.Background(), level, "policy decision",
		"allow", d.Allow,
		"agent", agentID,
		"actingAs", actingAsUser,
		"action", action,
		"resource", resourceType+"::"+resourceID,
		"policyVersion", d.PolicyVersion,
		"reasons", d.Reasons,
		"cacheHit", d.CacheHit,
	)
}

// decisionCacheKey includes the policy version, so activating a new policy
// version invalidates every cached decision everywhere for free — no
// explicit cache-busting pass needed, since old-version keys simply stop
// being looked up.
func decisionCacheKey(version int, agentID, actingAsUser, action, resourceType, resourceID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s|%s|%s", version, agentID, actingAsUser, action, resourceType, resourceID)))
	return "warden:policydecision:" + hex.EncodeToString(sum[:])
}

func (e *Engine) getCached(ctx context.Context, key string) (Decision, bool) {
	raw, err := e.cache.Get(ctx, key).Bytes()
	if err != nil {
		return Decision{}, false
	}
	var d Decision
	if err := json.Unmarshal(raw, &d); err != nil {
		return Decision{}, false
	}
	return d, true
}

func (e *Engine) setCached(ctx context.Context, key string, d Decision) {
	raw, err := json.Marshal(d)
	if err != nil {
		return
	}
	_ = e.cache.Set(ctx, key, raw, 30*time.Second).Err()
}
