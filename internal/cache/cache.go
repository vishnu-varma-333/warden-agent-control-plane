// Package cache implements exact-match response caching in Redis. The
// cache is keyed on a hash of the model and the full message list, so it
// only ever matches a byte-for-byte repeat of a prior request — it's
// correctness-preserving by construction, not a heuristic.
//
// Redis, not an in-memory map: the gateway runs as multiple replicas behind
// a load balancer, and an in-memory cache would mean each replica has its
// own partial view — the same request could hit a cold cache on every
// replica in turn, defeating the point.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/provider"
)

type Cache struct {
	client *redis.Client
	ttl    time.Duration
}

func New(client *redis.Client, ttl time.Duration) *Cache {
	return &Cache{client: client, ttl: ttl}
}

func key(req provider.ChatRequest) (string, error) {
	// Only model + messages affect the answer; anything else in the request
	// must not be part of the cache key or we'd get false hits.
	canonical := struct {
		Model    string                  `json:"model"`
		Messages []provider.ChatMessage  `json:"messages"`
	}{Model: req.Model, Messages: req.Messages}

	b, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("cache: marshal key input: %w", err)
	}
	sum := sha256.Sum256(b)
	return "warden:chatcache:" + hex.EncodeToString(sum[:]), nil
}

// Get returns the cached response and true on a hit, or a zero value and
// false on a miss (including any Redis error, which is treated as a miss
// so a cache outage degrades to "always call the provider," not a failure).
func (c *Cache) Get(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, bool) {
	k, err := key(req)
	if err != nil {
		return provider.ChatResponse{}, false
	}
	raw, err := c.client.Get(ctx, k).Bytes()
	if err != nil {
		return provider.ChatResponse{}, false
	}
	var resp provider.ChatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return provider.ChatResponse{}, false
	}
	return resp, true
}

func (c *Cache) Set(ctx context.Context, req provider.ChatRequest, resp provider.ChatResponse) {
	k, err := key(req)
	if err != nil {
		return
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	// Best-effort: a failed cache write shouldn't fail the request that
	// already has a perfectly good response to return.
	_ = c.client.Set(ctx, k, raw, c.ttl).Err()
}
