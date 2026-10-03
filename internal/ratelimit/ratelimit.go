// Package ratelimit implements a distributed fixed-window counter in
// Redis. "Distributed" is the entire point: an in-memory counter would be
// per gateway replica, so an agent could get 3x the real limit just by
// landing on 3 different pods behind a load balancer.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// incrAndExpire is one atomic unit from Redis's point of view: Lua scripts
// run to completion without another client's command interleaving. Without
// this, INCR followed by a separate EXPIRE call would race — two
// concurrent first-requests could both see count==1 and both set the
// expiry, which is harmless here, but a crash between the two calls would
// leave a key with no expiry at all, leaking memory forever.
const incrAndExpireScript = `
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return count
`

type Limiter struct {
	client *redis.Client
	limit  int
	window time.Duration
	script *redis.Script
}

func New(client *redis.Client, limit int, window time.Duration) *Limiter {
	return &Limiter{
		client: client,
		limit:  limit,
		window: window,
		script: redis.NewScript(incrAndExpireScript),
	}
}

// Allow reports whether key (e.g. an agent ID) may make one more call in
// the current window. A Redis error fails open (allow) with an error
// returned — per DECISIONS.md, losing the rate limiter shouldn't take down
// the whole gateway, but callers can choose to log/alert on the error.
func (l *Limiter) Allow(ctx context.Context, key string) (bool, error) {
	redisKey := fmt.Sprintf("warden:ratelimit:%s", key)
	count, err := l.script.Run(ctx, l.client, []string{redisKey}, int(l.window.Seconds())).Int()
	if err != nil {
		return true, fmt.Errorf("ratelimit: redis: %w", err)
	}
	return count <= l.limit, nil
}
