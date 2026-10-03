// Package budget enforces a spending cap per scope (agent or team) over a
// period, in Redis so it's correct across gateway replicas. Unlike a rate
// limiter, a rejected charge must NOT be partially applied — "charge if it
// fits, else don't charge at all" has to be one atomic decision, which is
// exactly what the Lua script below does.
package budget

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const chargeScript = `
local current = tonumber(redis.call("GET", KEYS[1]) or "0")
local amount = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local newTotal = current + amount
if newTotal > limit then
  -- Explicit tostring(): Lua numbers returned through redis.call/EVAL get
  -- converted to RESP integers (truncating any fraction), which would
  -- silently corrupt fractional costs. Returning a string keeps full
  -- precision all the way back to the Go client.
  return {0, tostring(current)}
end
redis.call("SET", KEYS[1], newTotal, "EX", ttl)
return {1, tostring(newTotal)}
`

type Budget struct {
	client *redis.Client
	limit  float64
	period time.Duration
	script *redis.Script
}

func New(client *redis.Client, limit float64, period time.Duration) *Budget {
	return &Budget{client: client, limit: limit, period: period, script: redis.NewScript(chargeScript)}
}

// Charge attempts to add amount to scope's spend for the current period.
// It returns whether the charge was applied and the resulting total (the
// pre-charge total if rejected). A Redis error fails open — see
// ratelimit.Allow for the same tradeoff and its rationale.
func (b *Budget) Charge(ctx context.Context, scope string, amount float64) (allowed bool, total float64, err error) {
	redisKey := fmt.Sprintf("warden:budget:%s", scope)
	res, err := b.script.Run(ctx, b.client, []string{redisKey}, amount, b.limit, int(b.period.Seconds())).Slice()
	if err != nil {
		return true, 0, fmt.Errorf("budget: redis: %w", err)
	}
	ok, _ := res[0].(int64)
	totalRaw, _ := res[1].(string) // Redis returns bulk strings for GET-derived values even through EVAL
	fmt.Sscanf(totalRaw, "%f", &total)
	return ok == 1, total, nil
}
