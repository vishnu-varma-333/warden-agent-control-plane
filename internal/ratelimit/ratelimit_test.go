package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestClient(t *testing.T) *redis.Client {
	t.Helper()
	srv := miniredis.RunT(t) // starts an in-memory Redis, torn down automatically at test end
	return redis.NewClient(&redis.Options{Addr: srv.Addr()})
}

func TestAllowsUpToLimitThenRejects(t *testing.T) {
	client := newTestClient(t)
	l := New(client, 3, time.Minute)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		allowed, err := l.Allow(ctx, "agent-1")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("call %d: expected allowed within limit of 3", i)
		}
	}

	allowed, err := l.Allow(ctx, "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected the 4th call to be rejected")
	}
}

func TestLimitsArePerKey(t *testing.T) {
	client := newTestClient(t)
	l := New(client, 1, time.Minute)
	ctx := context.Background()

	if allowed, _ := l.Allow(ctx, "agent-a"); !allowed {
		t.Fatal("expected agent-a's first call to be allowed")
	}
	if allowed, _ := l.Allow(ctx, "agent-b"); !allowed {
		t.Fatal("expected agent-b's first call to be allowed independently of agent-a")
	}
	if allowed, _ := l.Allow(ctx, "agent-a"); allowed {
		t.Fatal("expected agent-a's second call to be rejected")
	}
}
