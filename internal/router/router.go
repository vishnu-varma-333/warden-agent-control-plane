// Package router maps a model alias (what the caller asks for) to an
// ordered list of real providers, and walks that list on failure. Each
// provider gets its own circuit breaker, so one broken provider stops being
// retried without affecting the others.
package router

import (
	"context"
	"errors"
	"fmt"

	"time"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/breaker"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider"
)

const breakerCooldown = 10 * time.Second

// Route is "when someone asks for this model alias, try these providers in
// this order" — the same shape as the Route entity in the data model.
type Route struct {
	ModelAlias string
	Providers  []string // provider names, in fallback order
}

type Router struct {
	providers map[string]provider.Provider
	breakers  map[string]*breaker.Breaker
	routes    map[string]Route
}

func New() *Router {
	return &Router{
		providers: make(map[string]provider.Provider),
		breakers:  make(map[string]*breaker.Breaker),
		routes:    make(map[string]Route),
	}
}

// RegisterProvider makes a provider available to routes, with its own
// breaker: 3 consecutive failures trips it, 10s cooldown before a retry.
func (r *Router) RegisterProvider(p provider.Provider) {
	r.providers[p.Name()] = p
	r.breakers[p.Name()] = breaker.New(3, breakerCooldown)
}

func (r *Router) AddRoute(route Route) {
	r.routes[route.ModelAlias] = route
}

// ErrNoProviderAvailable means every provider for this route either errored
// or has its breaker open.
var ErrNoProviderAvailable = errors.New("router: no provider available for route")

func (r *Router) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, string, error) {
	route, ok := r.routes[req.Model]
	if !ok {
		return provider.ChatResponse{}, "", fmt.Errorf("router: no route for model %q", req.Model)
	}

	var lastErr error
	for _, name := range route.Providers {
		p := r.providers[name]
		b := r.breakers[name]
		if p == nil || b == nil || !b.Allow() {
			continue
		}
		resp, err := p.Chat(ctx, req)
		if err != nil {
			b.RecordFailure()
			lastErr = err
			continue
		}
		b.RecordSuccess()
		return resp, name, nil
	}
	if lastErr != nil {
		return provider.ChatResponse{}, "", fmt.Errorf("%w: last error: %v", ErrNoProviderAvailable, lastErr)
	}
	return provider.ChatResponse{}, "", ErrNoProviderAvailable
}

// ChatStream commits to the first provider whose breaker allows it and whose
// initial call succeeds; it does not fail over once streaming has started,
// since bytes may already be on their way to the caller.
func (r *Router) ChatStream(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, string, error) {
	route, ok := r.routes[req.Model]
	if !ok {
		return nil, "", fmt.Errorf("router: no route for model %q", req.Model)
	}

	var lastErr error
	for _, name := range route.Providers {
		p := r.providers[name]
		b := r.breakers[name]
		if p == nil || b == nil || !b.Allow() {
			continue
		}
		stream, err := p.ChatStream(ctx, req)
		if err != nil {
			b.RecordFailure()
			lastErr = err
			continue
		}
		b.RecordSuccess()
		return stream, name, nil
	}
	if lastErr != nil {
		return nil, "", fmt.Errorf("%w: last error: %v", ErrNoProviderAvailable, lastErr)
	}
	return nil, "", ErrNoProviderAvailable
}
