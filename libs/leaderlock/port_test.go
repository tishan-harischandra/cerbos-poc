package leaderlock_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
)

// An election is identified by a name the port understands, never by a raw
// lock key a caller invented: a key belongs to one backend's key space, and a
// caller that passes one has already chosen the backend.
func TestAnUnnamedElectionIsRefused(t *testing.T) {
	err := leaderlock.Name("").Validate()
	if !errors.Is(err, leaderlock.ErrInvalidElection) {
		t.Errorf("Validate() = %v, want ErrInvalidElection", err)
	}
}

// A name has to survive being turned into a Kubernetes object name, a Redis
// key and a database value alike. Validating against the intersection here
// means a name cannot work under one LEADER_ELECTION_TYPE and fail under
// another - which would be discovered only after switching backends.
func TestANameIsRefusedUnlessEveryBackendCouldUseItAsAKey(t *testing.T) {
	refused := []leaderlock.Name{
		"Outbox-Publisher",
		"outbox publisher",
		"outbox_publisher",
		"outbox/publisher",
		"-outbox",
		"outbox-",
		leaderlock.Name(strings.Repeat("a", leaderlock.MaxNameLength+1)),
	}
	for _, name := range refused {
		if err := name.Validate(); !errors.Is(err, leaderlock.ErrInvalidElection) {
			t.Errorf("Validate(%q) = %v, want ErrInvalidElection", name, err)
		}
	}

	// The elections this platform actually runs must pass their own rule.
	for _, name := range []leaderlock.Name{leaderlock.ElectionOutboxPublisher, leaderlock.ElectionPolicyController} {
		if err := name.Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want the platform's own elections to be valid", name, err)
		}
	}
}

// The advisory-lock adapter needs an int64, and every replica has to derive
// the same one from the same name without anyone writing a magic number down.
func TestANameMapsToAStableNonNegativeNumericKey(t *testing.T) {
	first := leaderlock.ElectionOutboxPublisher.NumericKey()
	if first != leaderlock.ElectionOutboxPublisher.NumericKey() {
		t.Error("NumericKey is not stable across calls")
	}
	if first < 0 {
		// A negative key is legal for pg_try_advisory_lock but reads as
		// a bug in a lock listing, and invites sign confusion.
		t.Errorf("NumericKey() = %d, want a non-negative key", first)
	}
	if second := leaderlock.ElectionPolicyController.NumericKey(); first == second {
		t.Errorf("both elections hash to %d, so they would share one lock", first)
	}
}
