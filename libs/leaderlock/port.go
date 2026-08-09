// Package leaderlock is the provider-neutral leader election port.
//
// Two workloads in this platform need singleton behaviour across replicas:
// the policy release pipeline and the outbox publisher. Which mechanism
// coordinates them is an operational choice - an installation running on
// Kubernetes already has Leases, one running plain containers against
// PostgreSQL already has advisory locks - so consumers depend on this package
// only. The concrete adapters live below it and are reachable solely through
// the provider factory, which selects one from LEADER_ELECTION_TYPE. An
// architecture test enforces that boundary (ADR-009).
//
// # What the port promises
//
// It promises that a caller's onElected work runs while this instance is
// believed to be the leader, and that leaderCtx is cancelled as soon as that
// belief ends.
//
// It deliberately does not promise mutual exclusion. Four of the five
// adapters are lease-based: a leader whose process is paused, or whose
// network is partitioned, can still be inside onElected at the moment its
// lease expires and a rival claims it. That overlap is a genuine split-brain
// window, and it is not a defect in those adapters - it is what a lease is.
//
//	PG_ADVISORY  session-scoped lock: no TTL, no renewal, no split-brain
//	DATABASE     lease on the database clock: split-brain window
//	K8S_LEASE    coordination.k8s.io Lease:   split-brain window
//	REDIS        SET NX PX with renewal:      split-brain window
//	SINGLE       always leader, no coordination at all
//
// Callers must therefore be safe under brief overlap. Both current consumers
// are: outbox delivery is already at-least-once with an idempotent
// invalidation consumer, and the release pipeline installs atomically. A
// future consumer that needs true exclusion cannot get it from this port by
// choosing a different adapter, because the port only ever promises its
// weakest member. There are no fencing tokens; ADR-009 records why an epoch
// nothing downstream can validate is worse than this warning.
package leaderlock

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
)

// The failures every adapter reports the same way, so a consumer can react to
// them without knowing which mechanism is installed.
var (
	// ErrInvalidElection means the election name is not one the port can
	// map into a backend's key space.
	ErrInvalidElection = errors.New("leaderlock: the election name is not valid")
	// ErrBackendUnavailable means the coordination backend could not be
	// reached. It is not a statement about who leads, only that this
	// instance cannot find out.
	ErrBackendUnavailable = errors.New("leaderlock: the coordination backend is unavailable")
	// ErrLeadershipLost is the cause leaderCtx is cancelled with when the
	// election was lost rather than the caller shutting down. Recover it
	// with context.Cause.
	ErrLeadershipLost = errors.New("leaderlock: leadership was lost")
	// ErrNotConfigured means no leader election type was selected. There
	// is no default: an unset type would silently elect every replica.
	ErrNotConfigured = errors.New("leaderlock: no leader election type is configured")
	// ErrUnknownType means the configured type names no adapter.
	ErrUnknownType = errors.New("leaderlock: the configured leader election type names no adapter")
)

// Name identifies one election. Callers name the election rather than passing
// a lock key, because a key is already a choice of backend: an int64 for an
// advisory lock, a Lease object name in Kubernetes, a string key in Redis, a
// primary key in a table. Each adapter maps a name into its own key space.
type Name string

// The elections this platform runs.
const (
	// ElectionOutboxPublisher decides which admin-service replica drains
	// the transactional outbox (§10.1).
	ElectionOutboxPublisher Name = "outbox-publisher"
	// ElectionPolicyController decides which policy-controller replica
	// runs the release pipeline (§13).
	ElectionPolicyController Name = "policy-controller"
)

// Validate reports whether the name can be mapped into every adapter's key
// space. The rules are the intersection of what a Kubernetes object name, a
// Redis key and a database column all accept, so a name that validates here
// works under every LEADER_ELECTION_TYPE rather than only the one the author
// happened to be running.
func (n Name) Validate() error {
	switch {
	case n == "":
		return fmt.Errorf("%w: an election must be named", ErrInvalidElection)
	case len(n) > MaxNameLength:
		return fmt.Errorf("%w: %q is longer than %d characters", ErrInvalidElection, string(n), MaxNameLength)
	}
	for _, r := range string(n) {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isDigit && r != '-' {
			return fmt.Errorf("%w: %q may hold only lower-case letters, digits and hyphens, so every backend can use it as a key",
				ErrInvalidElection, string(n))
		}
	}
	if strings.HasPrefix(string(n), "-") || strings.HasSuffix(string(n), "-") {
		return fmt.Errorf("%w: %q may not begin or end with a hyphen", ErrInvalidElection, string(n))
	}
	return nil
}

// MaxNameLength is the longest election name every backend accepts. A
// Kubernetes object name is the tightest of the four at 253 characters.
const MaxNameLength = 253

// String reports the name as written.
func (n Name) String() string { return string(n) }

// NumericKey maps the name into the int64 key space PostgreSQL advisory locks
// use. It is a stable FNV-1a hash folded into the non-negative range, so every
// replica of every release derives the same key from the same name without
// coordinating on a magic number.
func (n Name) NumericKey() int64 {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(n))
	return int64(digest.Sum64() &^ (1 << 63))
}

// Elector runs work under leadership of a named election.
type Elector interface {
	// Run contends for the election and calls onElected once this
	// instance becomes the leader, with a context that is cancelled the
	// moment leadership ends - whether it was lost, released or ctx itself
	// was cancelled. context.Cause reports ErrLeadershipLost when the
	// election was lost rather than the caller shutting down.
	//
	// Run blocks until ctx is done. An adapter that loses leadership goes
	// back to contending, so a caller writes no retry loop of its own; a
	// caller's onElected may therefore be invoked more than once, and must
	// return promptly when its leaderCtx is cancelled.
	//
	// Run returns nil on a clean shutdown, and an error only when the
	// election cannot be run at all.
	Run(ctx context.Context, election Name, onElected func(leaderCtx context.Context)) error
}
