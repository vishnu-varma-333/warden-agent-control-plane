// Package breaker implements a minimal circuit breaker: after enough
// consecutive failures against a backend, stop calling it for a cooldown
// window instead of retrying a backend that's clearly down on every request.
package breaker

import (
	"sync"
	"time"
)

type State int

const (
	Closed State = iota // normal operation, calls go through
	Open                // tripped: calls are rejected without trying
	HalfOpen            // cooldown elapsed: exactly one trial call is allowed
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Breaker is safe for concurrent use.
type Breaker struct {
	failureThreshold int
	cooldown         time.Duration

	mu                  sync.Mutex
	state               State
	consecutiveFailures int
	openedAt            time.Time
	halfOpenInFlight    bool
}

// New returns a Breaker that opens after failureThreshold consecutive
// failures and stays open for cooldown before allowing one trial call.
func New(failureThreshold int, cooldown time.Duration) *Breaker {
	return &Breaker{
		failureThreshold: failureThreshold,
		cooldown:         cooldown,
		state:            Closed,
	}
}

// Allow reports whether a call may proceed right now. When it returns true
// for a HalfOpen breaker, the caller has claimed the single trial slot and
// MUST report the outcome via RecordSuccess/RecordFailure.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Closed:
		return true
	case Open:
		if time.Since(b.openedAt) < b.cooldown {
			return false
		}
		// Cooldown elapsed: move to half-open and let exactly one call through.
		b.state = HalfOpen
		b.halfOpenInFlight = true
		return true
	case HalfOpen:
		// Another call already claimed the trial slot; reject until it resolves.
		return !b.halfOpenInFlight
	}
	return false
}

// RecordSuccess closes the breaker and resets the failure count.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = Closed
	b.consecutiveFailures = 0
	b.halfOpenInFlight = false
}

// RecordFailure counts a failure. In Closed, it opens the breaker once the
// threshold is hit. In HalfOpen, a single failed trial reopens it
// immediately (no point trying again until the cooldown passes once more).
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case HalfOpen:
		b.open()
	case Closed:
		b.consecutiveFailures++
		if b.consecutiveFailures >= b.failureThreshold {
			b.open()
		}
	}
}

func (b *Breaker) open() {
	b.state = Open
	b.openedAt = time.Now()
	b.halfOpenInFlight = false
	b.consecutiveFailures = 0
}

// State returns the current state, mainly for observability/tests.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
