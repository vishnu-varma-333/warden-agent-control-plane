// Package guard is Warden's second, independent line of defense against
// malicious tool content: a trained classifier (internal/guard/guardpb,
// served from Python over gRPC) scans tool descriptions and tool outputs
// for prompt-injection attempts. This is deliberately separate from the
// registry's hash-based tool-poisoning detection (milestone 4) — that
// catches a definition silently CHANGING; this catches a definition or
// output containing an injection attempt even the FIRST time it's seen,
// which a hash pin can never do.
package guard

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/guard/guardpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Result struct {
	IsInjection bool
	Confidence  float32
	LatencyMS   float64
}

type Client struct {
	conn    *grpc.ClientConn
	rpc     guardpb.GuardClient
	timeout time.Duration
}

// New dials the classifier service. timeout is the strict per-call latency
// budget the spec calls for: a classifier call that doesn't return in time
// is treated as unavailable (see Scan's fail-open behavior), not awaited
// indefinitely — a hot-path safety scan must never become the slowest part
// of a request.
func New(addr string, timeout time.Duration) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("guard: dial %s: %w", addr, err)
	}
	return &Client{conn: conn, rpc: guardpb.NewGuardClient(conn), timeout: timeout}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Scan classifies text and reports whether it looks like a prompt-injection
// attempt. On any failure (timeout, connection error, classifier down), it
// fails OPEN — returns ok=false and no error is surfaced to the caller's
// request — logging loudly instead of blocking the call. This mirrors the
// rate-limit/budget fail-open reasoning (DECISIONS.md), not the policy
// engine's fail-closed one: losing this detection layer is a risk the
// *policy engine* (the real authorization boundary) is still enforcing
// independently, so refusing all traffic because an ML sidecar is down
// would trade a detection gap for a total availability outage — a bad
// trade for a defense-in-depth layer, not the primary one.
func (c *Client) Scan(ctx context.Context, text string) (result Result, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.rpc.Classify(ctx, &guardpb.ClassifyRequest{Text: text})
	if err != nil {
		slog.Error("guard: classify call failed, failing open", "error", err)
		return Result{}, false
	}
	return Result{
		IsInjection: resp.GetIsInjection(),
		Confidence:  resp.GetConfidence(),
		LatencyMS:   resp.GetLatencyMs(),
	}, true
}
