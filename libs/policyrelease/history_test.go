package policyrelease_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func TestStore_HistoryReportsRecordedAttemptsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	store := policyrelease.NewStore(dir)

	if err := store.RecordAttempt(policyrelease.HistoryEntry{
		Revision: "root-v1.3.0", Commit: "aaa", Activated: true,
	}); err != nil {
		t.Fatalf("RecordAttempt(first): %v", err)
	}
	if err := store.RecordAttempt(policyrelease.HistoryEntry{
		Revision: "root-v1.4.0", Commit: "bbb", Activated: false, Error: "replica cerbos-b failed to reload",
	}); err != nil {
		t.Fatalf("RecordAttempt(second): %v", err)
	}

	history, err := store.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(history))
	}
	if history[0].Revision != "root-v1.3.0" || !history[0].Activated {
		t.Errorf("history[0] = %+v, want the first successful attempt", history[0])
	}
	if history[1].Revision != "root-v1.4.0" || history[1].Activated {
		t.Errorf("history[1] = %+v, want the second failed attempt", history[1])
	}
	if history[1].Error == "" {
		t.Errorf("history[1].Error is empty, want the failure reason recorded")
	}
	if history[0].RecordedAt.IsZero() {
		t.Errorf("history[0].RecordedAt is zero, want a timestamp")
	}
}

func TestStore_HistoryOnAStoreWithNoAttemptsIsEmpty(t *testing.T) {
	dir := t.TempDir()
	store := policyrelease.NewStore(dir)

	history, err := store.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("len(history) = %d, want 0", len(history))
	}
}
