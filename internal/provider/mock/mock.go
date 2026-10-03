// Package mock is a fake model backend used for local development and the
// gateway benchmark: it responds instantly with no network call, so timing
// it measures the gateway's own added overhead, not a real provider's
// latency. It can also be forced "unhealthy" to prove fallback and the
// circuit breaker actually work, without needing a real outage.
package mock

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider"
)

type Provider struct {
	name      string
	unhealthy atomic.Bool
	latency   time.Duration // artificial per-call delay, for simulating a slow provider
}

func New(name string) *Provider {
	return &Provider{name: name}
}

func (p *Provider) Name() string { return p.name }

// SetUnhealthy flips whether every call fails immediately, simulating a
// provider outage.
func (p *Provider) SetUnhealthy(v bool) { p.unhealthy.Store(v) }

// SetLatency adds an artificial delay before responding, for testing what a
// slow (not down) provider looks like to the fallback logic.
func (p *Provider) SetLatency(d time.Duration) { p.latency = d }

func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if p.latency > 0 {
		select {
		case <-time.After(p.latency):
		case <-ctx.Done():
			return provider.ChatResponse{}, ctx.Err()
		}
	}
	if p.unhealthy.Load() {
		return provider.ChatResponse{}, fmt.Errorf("mock provider %q: simulated outage", p.name)
	}
	return provider.ChatResponse{
		Content:      p.echo(req),
		FinishReason: "stop",
	}, nil
}

func (p *Provider) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	if p.unhealthy.Load() {
		return nil, fmt.Errorf("mock provider %q: simulated outage", p.name)
	}

	out := make(chan provider.ChatChunk)
	go func() {
		defer close(out)
		words := strings.Fields(p.echo(req))
		for i, w := range words {
			chunk := provider.ChatChunk{Delta: w + " "}
			if i == len(words)-1 {
				chunk.FinishReason = "stop"
				chunk.Done = true
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
			if p.latency > 0 {
				time.Sleep(p.latency)
			}
		}
	}()
	return out, nil
}

// echo is deliberately trivial (no real model behind this): it just proves
// the request reached a specific named provider, which is what fallback
// tests need to assert on.
func (p *Provider) echo(req provider.ChatRequest) string {
	var last string
	if n := len(req.Messages); n > 0 {
		last = req.Messages[n-1].Content
	}
	return fmt.Sprintf("[%s] you said: %s", p.name, last)
}
