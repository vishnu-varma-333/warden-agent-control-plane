package breaker

import (
	"testing"
	"time"
)

func TestClosedAllowsUntilThreshold(t *testing.T) {
	b := New(3, time.Minute)

	if !b.Allow() {
		t.Fatal("expected closed breaker to allow")
	}
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != Closed {
		t.Fatalf("expected still closed after 2/3 failures, got %s", b.State())
	}
	b.RecordFailure()
	if b.State() != Open {
		t.Fatalf("expected open after 3/3 failures, got %s", b.State())
	}
	if b.Allow() {
		t.Fatal("expected open breaker to reject calls before cooldown")
	}
}

func TestHalfOpenTrialSuccessCloses(t *testing.T) {
	b := New(1, 10*time.Millisecond)
	b.RecordFailure() // trips open immediately (threshold 1)

	if b.Allow() {
		t.Fatal("expected reject while within cooldown")
	}
	time.Sleep(15 * time.Millisecond)

	if !b.Allow() {
		t.Fatal("expected one trial call allowed after cooldown (half-open)")
	}
	if b.Allow() {
		t.Fatal("expected a second concurrent call to be rejected while a trial is in flight")
	}

	b.RecordSuccess()
	if b.State() != Closed {
		t.Fatalf("expected closed after successful trial, got %s", b.State())
	}
	if !b.Allow() {
		t.Fatal("expected closed breaker to allow again")
	}
}

func TestHalfOpenTrialFailureReopens(t *testing.T) {
	b := New(1, 10*time.Millisecond)
	b.RecordFailure()
	time.Sleep(15 * time.Millisecond)

	if !b.Allow() {
		t.Fatal("expected trial call allowed after cooldown")
	}
	b.RecordFailure()
	if b.State() != Open {
		t.Fatalf("expected reopened after failed trial, got %s", b.State())
	}
	if b.Allow() {
		t.Fatal("expected reject immediately after reopening")
	}
}
