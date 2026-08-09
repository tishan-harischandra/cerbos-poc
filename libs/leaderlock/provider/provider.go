// Package provider is the composition root for leader election adapters.
//
// It is the only package permitted to import a concrete adapter: consumers
// take a leaderlock.Elector from here and never learn which mechanism is
// behind it, so an operator changes the coordination backend with an
// environment variable. An architecture test fails the build if any other
// package reaches past this one (ADR-009).
package provider

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/pgadvisory"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/single"
)

// Type names a coordination mechanism.
type Type string

const (
	// TypePGAdvisory holds a PostgreSQL session-scoped advisory lock. The
	// only type with no split-brain window, and the only one that cannot
	// run against Oracle.
	TypePGAdvisory Type = "PG_ADVISORY"
	// TypeSingle elects every caller and coordinates with nothing. For a
	// deployment scaled to one replica, and for tests.
	TypeSingle Type = "SINGLE"
)

// Environment variables, all prefixed LEADER_ELECTION_ so one glance at a
// compose file or a manifest shows the whole coordination configuration.
const (
	// EnvType selects the adapter. It has no default: see FromEnv.
	EnvType = "LEADER_ELECTION_TYPE"
	// EnvIdentity names this instance in the election, so an operator
	// reading a lease can tell which replica holds it. It defaults to the
	// hostname, which is the pod name in Kubernetes.
	EnvIdentity = "LEADER_ELECTION_IDENTITY"
	// EnvTTL is how long a lease survives without renewal.
	EnvTTL = "LEADER_ELECTION_TTL"
	// EnvRenewInterval is how often a leader renews. It defaults to a
	// third of the TTL, so two consecutive renewals can fail before
	// leadership is at risk.
	EnvRenewInterval = "LEADER_ELECTION_RENEW_INTERVAL"
	// EnvRetryInterval is how often a follower re-contends.
	EnvRetryInterval = "LEADER_ELECTION_RETRY_INTERVAL"
	// EnvDSN is the database the election runs on, for the types that use
	// one. It falls back to the authorization database's own DSN, because
	// an election on a second database would be a second thing to keep
	// available.
	EnvDSN = "LEADER_ELECTION_DSN"
)

// The authorization database DSNs the election falls back to, so a compose
// file or a manifest configures one database rather than two.
const (
	envPostgresDSN = "ASSIGNMENTSTORE_POSTGRES_DSN"
	envOracleDSN   = "ASSIGNMENTSTORE_ORACLE_DSN"
)

// DefaultTTL is how long a lease lives without renewal. It is long enough to
// ride out a garbage collection pause or a brief network stall, and short
// enough that a dead leader is replaced well inside a human's patience.
const DefaultTTL = 15 * time.Second

// DefaultRetryInterval is how often a follower asks again.
const DefaultRetryInterval = 2 * time.Second

// Config is the resolved coordination configuration.
type Config struct {
	Type Type
	// Identity names this instance in the election.
	Identity string
	// TTL is how long a lease survives unrenewed. Ignored by adapters
	// with no lease.
	TTL time.Duration
	// RenewInterval is how often a leader renews its lease.
	RenewInterval time.Duration
	// RetryInterval is how often a follower re-contends.
	RetryInterval time.Duration

	// DSN is the database the election runs on, for the database-backed
	// types.
	DSN string
}

// LookupFunc mirrors os.LookupEnv so configuration stays testable.
type LookupFunc func(key string) (string, bool)

// FromEnv resolves the installation's coordination configuration.
//
// It refuses to produce a configuration when EnvType is unset. Every other
// setting in this repository has a sensible default; this one cannot. A
// missing type would have to mean either "coordinate with nothing", which
// silently elects every replica, or a guess at a backend the installation may
// not run. Both are worse than refusing to start.
func FromEnv(lookup LookupFunc) (Config, error) {
	declared, ok := lookup(EnvType)
	if !ok || declared == "" {
		return Config{}, fmt.Errorf("%w: set %s to one of %s - there is deliberately no default, because an unset type would elect every replica at once",
			leaderlock.ErrNotConfigured, EnvType, supportedTypes())
	}

	cfg := Config{
		Type:          Type(declared),
		Identity:      valueOr(lookup, EnvIdentity, defaultIdentity()),
		TTL:           durationOr(lookup, EnvTTL, DefaultTTL),
		RenewInterval: durationOr(lookup, EnvRenewInterval, 0),
		RetryInterval: durationOr(lookup, EnvRetryInterval, DefaultRetryInterval),
		DSN:           valueOr(lookup, EnvDSN, valueOr(lookup, envPostgresDSN, valueOr(lookup, envOracleDSN, ""))),
	}
	if !known(cfg.Type) {
		return Config{}, fmt.Errorf("%w: %s=%q; supported types are %s",
			leaderlock.ErrUnknownType, EnvType, declared, supportedTypes())
	}
	if cfg.RenewInterval <= 0 {
		// A third of the TTL lets two consecutive renewals fail before
		// the lease is in danger, which is what makes a lease survive a
		// brief stall rather than merely detect one.
		cfg.RenewInterval = cfg.TTL / 3
	}
	if cfg.RenewInterval >= cfg.TTL {
		return Config{}, fmt.Errorf("%s (%s) must be shorter than %s (%s), or a leader loses its lease before it renews",
			EnvRenewInterval, cfg.RenewInterval, EnvTTL, cfg.TTL)
	}
	return cfg, nil
}

// New builds the one elector this installation uses.
func New(cfg Config) (leaderlock.Elector, error) {
	switch cfg.Type {
	case TypePGAdvisory:
		return pgadvisory.New(pgadvisory.Config{
			DSN:           cfg.DSN,
			CheckInterval: cfg.RenewInterval,
			RetryInterval: cfg.RetryInterval,
		})
	case TypeSingle:
		return single.New(), nil
	default:
		return nil, fmt.Errorf("%w: %s=%q; supported types are %s",
			leaderlock.ErrUnknownType, EnvType, cfg.Type, supportedTypes())
	}
}

func known(t Type) bool {
	for _, supported := range Types {
		if t == supported {
			return true
		}
	}
	return false
}

// Types are every mechanism this build supports, in the order they are
// reported to an operator who got the value wrong.
var Types = []Type{TypePGAdvisory, TypeSingle}

func supportedTypes() string {
	names := make([]string, 0, len(Types))
	for _, t := range Types {
		names = append(names, string(t))
	}
	return joinWithOr(names)
}

func joinWithOr(names []string) string {
	switch len(names) {
	case 0:
		return "(none)"
	case 1:
		return names[0]
	}
	joined := ""
	for i, name := range names[:len(names)-1] {
		if i > 0 {
			joined += ", "
		}
		joined += name
	}
	return joined + " or " + names[len(names)-1]
}

// defaultIdentity is the hostname, which is the pod name under Kubernetes and
// the container id under compose - in both cases the name an operator would
// use to find the holder.
func defaultIdentity() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown-" + strconv.Itoa(os.Getpid())
	}
	return host
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func durationOr(lookup LookupFunc, key string, fallback time.Duration) time.Duration {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
