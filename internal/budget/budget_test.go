package budget

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestClient(t *testing.T) *redis.Client {
	t.Helper()
	srv := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: srv.Addr()})
}

func TestChargesAccumulateUntilLimit(t *testing.T) {
	client := newTestClient(t)
	b := New(client, 10.0, time.Minute)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		allowed, total, err := b.Charge(ctx, "team-a", 2.0)
		if err != nil {
			t.Fatalf("charge %d: unexpected error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("charge %d: expected allowed (cumulative %v of limit 10)", i, (i+1)*2)
		}
		if total != float64((i+1)*2) {
			t.Fatalf("charge %d: expected total %v, got %v", i, (i+1)*2, total)
		}
	}
}

func TestRejectedChargeIsNotApplied(t *testing.T) {
	client := newTestClient(t)
	b := New(client, 10.0, time.Minute)
	ctx := context.Background()

	if allowed, total, _ := b.Charge(ctx, "team-a", 9.0); !allowed || total != 9.0 {
		t.Fatalf("expected first charge of 9 allowed with total 9, got allowed=%v total=%v", allowed, total)
	}

	// This charge of 5 would bring the total to 14, over the limit of 10 —
	// it must be rejected AND leave the stored total unchanged at 9, not
	// partially applied.
	allowed, total, err := b.Charge(ctx, "team-a", 5.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected this charge to be rejected for exceeding the limit")
	}
	if total != 9.0 {
		t.Fatalf("expected rejected charge to leave total at 9 (unchanged), got %v", total)
	}

	// Prove it really wasn't applied: a charge of 1 (9+1=10) should still fit.
	allowed, total, err = b.Charge(ctx, "team-a", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed || total != 10.0 {
		t.Fatalf("expected the rejected charge to have left room for +1 (total 10), got allowed=%v total=%v", allowed, total)
	}
}
