// Package mcpgateway makes Warden itself an MCP server that agents connect
// to as their single endpoint, proxying tools/call through to whichever
// real upstream MCP server actually owns that tool — while pinning every
// tool's definition by hash and refusing to expose or call one whose
// definition has silently changed since it was last trusted.
package mcpgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/approval"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/identity"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/policy"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/registry"
)

type UpstreamConfig struct {
	Name string
	URL  string
}

type Gateway struct {
	registry *registry.Registry
	policy   *policy.Engine
	approval *approval.Manager // nil means no approval gating is configured
	verifier *identity.Verifier
	outward  *mcp.Server
	impl     *mcp.Implementation

	mu       sync.Mutex
	sessions map[string]*mcp.ClientSession
	configs  map[string]UpstreamConfig
}

func New(reg *registry.Registry, pol *policy.Engine, appr *approval.Manager, verifier *identity.Verifier, impl *mcp.Implementation) *Gateway {
	return &Gateway{
		registry: reg,
		policy:   pol,
		approval: appr,
		verifier: verifier,
		outward:  mcp.NewServer(impl, nil),
		impl:     impl,
		sessions: make(map[string]*mcp.ClientSession),
		configs:  make(map[string]UpstreamConfig),
	}
}

// Server returns the outward-facing MCP server agents connect to.
func (g *Gateway) Server() *mcp.Server { return g.outward }

// ConnectUpstream opens a client session to one upstream MCP server and
// does an initial sync. Call StartPeriodicSync separately to keep
// detecting changes after startup.
func (g *Gateway) ConnectUpstream(ctx context.Context, cfg UpstreamConfig) error {
	g.mu.Lock()
	g.configs[cfg.Name] = cfg
	g.mu.Unlock()

	if err := g.connect(ctx, cfg); err != nil {
		return err
	}
	return g.Sync(ctx, cfg.Name)
}

func (g *Gateway) connect(ctx context.Context, cfg UpstreamConfig) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "warden-gateway", Version: "v1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: cfg.URL}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcpgateway: connect to %s (%s): %w", cfg.Name, cfg.URL, err)
	}

	g.mu.Lock()
	g.sessions[cfg.Name] = session
	g.mu.Unlock()
	return nil
}

// StartPeriodicSync re-lists tools from every connected upstream on an
// interval, so a definition changed after startup still gets caught (not
// just ones changed before Warden ever saw them).
func (g *Gateway) StartPeriodicSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.mu.Lock()
				names := make([]string, 0, len(g.sessions))
				for name := range g.sessions {
					names = append(names, name)
				}
				g.mu.Unlock()
				for _, name := range names {
					if err := g.Sync(ctx, name); err != nil {
						slog.Error("mcp periodic sync failed", "upstream", name, "error", err)
					}
				}
			}
		}
	}()
}

// Sync lists tools from one upstream, reconciles each against the
// registry, and updates which tools are actually exposed/callable on the
// outward server: active tools are (re-)registered with a proxying
// handler, changed (blocked) tools are removed so agents can't see or call
// them until a human approves the new definition.
func (g *Gateway) Sync(ctx context.Context, upstreamName string) error {
	g.mu.Lock()
	session := g.sessions[upstreamName]
	cfg, hasCfg := g.configs[upstreamName]
	g.mu.Unlock()
	if session == nil {
		return fmt.Errorf("mcpgateway: no connected session for %q", upstreamName)
	}

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		// The upstream's session can die out from under us (e.g. the
		// upstream process restarted, invalidating its SSE session) between
		// syncs. One reconnect attempt before giving up turns "upstream
		// restarted" into a brief gap instead of a permanent outage that
		// needs a gateway restart to clear.
		if !hasCfg {
			return fmt.Errorf("mcpgateway: list tools on %q: %w", upstreamName, err)
		}
		slog.Warn("mcp upstream session appears dead, attempting reconnect", "upstream", upstreamName, "error", err)
		if connErr := g.connect(ctx, cfg); connErr != nil {
			return fmt.Errorf("mcpgateway: list tools on %q: %w (reconnect also failed: %v)", upstreamName, err, connErr)
		}
		g.mu.Lock()
		session = g.sessions[upstreamName]
		g.mu.Unlock()
		res, err = session.ListTools(ctx, nil)
		if err != nil {
			return fmt.Errorf("mcpgateway: list tools on %q after reconnect: %w", upstreamName, err)
		}
		slog.Info("mcp upstream reconnected", "upstream", upstreamName)
	}

	for _, t := range res.Tools {
		hash := hashTool(t)
		status, err := g.registry.Reconcile(ctx, upstreamName, t.Name, hash)
		if err != nil {
			slog.Error("mcp tool reconcile failed", "upstream", upstreamName, "tool", t.Name, "error", err)
			continue
		}

		switch status {
		case registry.StatusActive:
			g.outward.AddTool(&mcp.Tool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			}, g.proxyHandler(upstreamName))
		case registry.StatusChanged:
			g.outward.RemoveTools(t.Name)
			slog.Warn("mcp tool definition changed; blocked until re-approved",
				"upstream", upstreamName, "tool", t.Name)
		}
	}
	return nil
}

// proxyHandler forwards a tools/call to the real upstream, after an
// independent status check at call time — not just trusting that the tool
// was active when it was last listed. Listing and calling are separate
// requests; re-checking closes the gap between them.
func (g *Gateway) proxyHandler(upstreamName string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolName := req.Params.Name

		// Identity is resolved per call, not once per connection: a single
		// MCP session can carry many tools/call requests, and each one
		// brings its own Authorization/X-Acting-As headers (RequestExtra),
		// which may differ between calls.
		var header http.Header
		if extra := req.GetExtra(); extra != nil {
			header = extra.Header
		}
		principal, err := identity.ResolveFromHeader(g.verifier, header)
		if err != nil {
			return nil, fmt.Errorf("mcpgateway: %w", err)
		}

		decision, err := g.policy.Authorize(ctx, principal.AgentID, principal.ActingAs, "CallTool", "Tool", toolName)
		if err != nil {
			return nil, fmt.Errorf("mcpgateway: policy check failed: %w", err)
		}
		if !decision.Allow {
			return nil, fmt.Errorf("mcpgateway: denied by policy (version %d): %v", decision.PolicyVersion, decision.Reasons)
		}

		if g.approval != nil {
			needsApproval, err := g.approval.RequiresApproval(ctx, "CallTool", "Tool", toolName)
			if err != nil {
				return nil, fmt.Errorf("mcpgateway: approval rule check failed: %w", err)
			}
			if needsApproval {
				result, err := g.waitForApprovalThenProceed(ctx, header, principal, toolName)
				if err != nil || result != nil {
					return result, err
				}
				// result == nil, err == nil: approved, execution not yet
				// claimed by anyone else — fall through to the normal path
				// below, which performs the actual proxy call.
			}
		}

		status, ok, err := g.registry.Status(ctx, upstreamName, toolName)
		if err != nil {
			return nil, fmt.Errorf("mcpgateway: status check: %w", err)
		}
		if !ok || status != registry.StatusActive {
			return nil, fmt.Errorf("mcpgateway: tool %q is blocked pending re-approval (definition changed)", toolName)
		}

		g.mu.Lock()
		session := g.sessions[upstreamName]
		cfg, hasCfg := g.configs[upstreamName]
		g.mu.Unlock()
		if session == nil {
			return nil, fmt.Errorf("mcpgateway: upstream %q not connected", upstreamName)
		}

		var args any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return nil, fmt.Errorf("mcpgateway: decode arguments: %w", err)
			}
		}

		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: args})
		if err != nil && hasCfg {
			slog.Warn("mcp upstream session appears dead on call, attempting reconnect", "upstream", upstreamName, "error", err)
			if connErr := g.connect(ctx, cfg); connErr == nil {
				g.mu.Lock()
				session = g.sessions[upstreamName]
				g.mu.Unlock()
				result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: args})
			}
		}
		return result, err
	}
}

// waitForApprovalThenProceed requests (or resumes waiting on) a durable
// approval, blocking the caller until it's decided or expires. On return:
//   - (non-nil result, nil error): the call is fully handled already
//     (rejected, expired, or already executed by a concurrent/retried
//     request) — the caller must return this as-is, not proceed further.
//   - (nil, non-nil error): something failed outright.
//   - (nil, nil): approved AND this call won the execution claim — the
//     caller should fall through and perform the real proxy call.
func (g *Gateway) waitForApprovalThenProceed(ctx context.Context, header http.Header, principal identity.Principal, toolName string) (*mcp.CallToolResult, error) {
	idempotencyKey := header.Get("X-Idempotency-Key") // optional; see DECISIONS.md on why it's caller-supplied, not inferred

	a, err := g.approval.Request(ctx, idempotencyKey, approval.CallSnapshot{
		AgentID: principal.AgentID, ActingAs: principal.ActingAs,
		Action: "CallTool", ResourceType: "Tool", ResourceID: toolName,
	}, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("mcpgateway: request approval: %w", err)
	}

	state := a.State
	if state == approval.StatePending {
		slog.Info("mcp call paused for approval", "approvalID", a.ID, "tool", toolName)
		state, err = g.approval.WaitForDecision(ctx, a.ID, time.Second)
		if err != nil {
			return nil, fmt.Errorf("mcpgateway: wait for approval: %w", err)
		}
	}

	switch state {
	case approval.StateRejected:
		return nil, fmt.Errorf("mcpgateway: call to %q was rejected by approval %s", toolName, a.ID)
	case approval.StateExpired:
		return nil, fmt.Errorf("mcpgateway: approval %s for %q expired before a decision was made", a.ID, toolName)
	case approval.StateApproved:
		claimed, err := g.approval.ClaimExecution(ctx, a.ID)
		if err != nil {
			return nil, fmt.Errorf("mcpgateway: claim execution: %w", err)
		}
		if !claimed {
			slog.Info("approved call already executed by another request; not duplicating", "approvalID", a.ID, "tool", toolName)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "already executed (approval " + a.ID + "); not repeating the action"}},
			}, nil
		}
		return nil, nil // claimed: proceed to the real call
	default:
		return nil, fmt.Errorf("mcpgateway: approval %s in unexpected state %q", a.ID, state)
	}
}

// hashTool fingerprints the parts of a tool definition that matter for
// trust: what it's called, what it claims to do, and what it accepts.
// json.Marshal sorts map keys, so this is stable across runs regardless of
// map iteration order in InputSchema.
func hashTool(t *mcp.Tool) string {
	canonical := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema any    `json:"inputSchema"`
	}{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}

	b, _ := json.Marshal(canonical) // Marshal of this fixed shape cannot fail
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
