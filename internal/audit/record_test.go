package audit

import (
	"testing"
	"time"
)

func sampleRecord() Record {
	return Record{
		Seq: 1, PrevHash: "", EventID: "evt-1", Decision: "allow", Reason: "policy0",
		AgentID: "agent-demo", ActingAs: "user-1", Action: "CallModel",
		ResourceType: "Model", ResourceID: "mock-model",
		OccurredAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
	}
}

func TestComputeHashIsDeterministic(t *testing.T) {
	r := sampleRecord()
	if ComputeHash(r) != ComputeHash(r) {
		t.Fatal("expected identical records to hash identically")
	}
}

func TestComputeHashChangesWithAnyField(t *testing.T) {
	base := ComputeHash(sampleRecord())

	changed := sampleRecord()
	changed.Reason = "a different reason"
	if ComputeHash(changed) == base {
		t.Fatal("expected changing Reason to change the hash")
	}

	changed = sampleRecord()
	changed.Decision = "deny"
	if ComputeHash(changed) == base {
		t.Fatal("expected changing Decision to change the hash")
	}

	changed = sampleRecord()
	changed.PrevHash = "something-else"
	if ComputeHash(changed) == base {
		t.Fatal("expected changing PrevHash to change the hash (this is what makes it a CHAIN)")
	}
}
