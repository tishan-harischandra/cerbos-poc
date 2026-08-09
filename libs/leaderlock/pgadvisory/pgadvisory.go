// Package pgadvisory is the leader election adapter backed by a PostgreSQL
// session-scoped advisory lock.
//
// It is the only adapter that offers genuine mutual exclusion. The lock is
// tied to a session rather than to a row or a lease: there is no ttl to
// expire, no renewal to miss, and therefore no window in which two instances
// both believe they lead. A crashed leader's session ends with it and
// PostgreSQL releases the lock, so a dead leader cannot hold the fleet
// hostage either.
//
// The cost is a dedicated connection per elector and a hard dependency on
// PostgreSQL, which is why an installation running the oracle profile picks
// DATABASE instead and accepts the lease semantics that come with it.
//
// Nothing outside the provider factory may import this package.
package pgadvisory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/lease"
)

// reconnectPingTimeout bounds the liveness ping ensureConn gives the
// dedicated connection before deciding it is dead and redialing.
//
// A PostgreSQL restart - issue #26's chaos suite restarts the database ahead
// of a policy-rollout scenario - leaves this connection's socket half-open. It
// looks fine until something actually uses it, and a raw pgx.Conn never
// notices or reconnects on its own, so before this check every acquisition
// after a restart failed the same way and the policy controller silently
// stopped processing releases.
const reconnectPingTimeout = 2 * time.Second

// livenessTimeout bounds the check that decides whether this instance still
// holds the lock.
const livenessTimeout = 2 * time.Second

// DefaultCheckInterval is how often a leader confirms its session is still
// alive. There is no lease to renew; this only decides how quickly a leader
// whose connection died learns that it is no longer leading.
const DefaultCheckInterval = 5 * time.Second

// Config describes the PostgreSQL instance the election runs on.
type Config struct {
	// DSN is a libpq connection string or URL. The elector opens its own
	// connection rather than borrowing from a pool, because the lock
	// lives on the session and a pooled connection is handed to somebody
	// else the moment it is returned.
	DSN string

	// CheckInterval is how often a leader confirms its session survives.
	// Zero means DefaultCheckInterval.
	CheckInterval time.Duration
	// RetryInterval is how often a follower re-contends.
	RetryInterval time.Duration

	// PauseRenewal reproduces a paused or killed leader for the contract
	// suite. Production leaves it nil.
	PauseRenewal <-chan struct{}
	// OnError, if set, receives backend failures.
	OnError func(error)
}

// Elector contends for one advisory lock on a dedicated connection.
type Elector struct {
	cfg Config

	mu   sync.Mutex
	conn *pgx.Conn
	// reconnected records that the dedicated connection was replaced.
	// Any lock the old session held died with it, so a leader that
	// reconnects has lost the election even though the new connection
	// works perfectly.
	reconnected bool
	key         int64
}

// New returns the adapter.
func New(cfg Config) (*Elector, error) {
	if cfg.DSN == "" {
		return nil, errors.New("pgadvisory: a DSN is required")
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = DefaultCheckInterval
	}
	return &Elector{cfg: cfg}, nil
}

// Run contends for the advisory lock the election name maps to.
func (e *Elector) Run(ctx context.Context, election leaderlock.Name, onElected func(context.Context)) error {
	if err := election.Validate(); err != nil {
		return err
	}
	e.key = election.NumericKey()
	defer e.close()

	return lease.Run(ctx, lease.Config{
		RenewInterval: e.cfg.CheckInterval,
		RetryInterval: e.cfg.RetryInterval,
		PauseRenewal:  e.cfg.PauseRenewal,
		OnError:       e.cfg.OnError,
	}, e, onElected)
}

// Acquire takes the lock without blocking, reconnecting first if the
// dedicated connection has died underneath us.
func (e *Elector) Acquire(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureConn(ctx); err != nil {
		return false, err
	}
	var acquired bool
	if err := e.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", e.key).Scan(&acquired); err != nil {
		return false, fmt.Errorf("%w: pg_try_advisory_lock: %w", leaderlock.ErrBackendUnavailable, err)
	}
	if acquired {
		e.reconnected = false
	}
	return acquired, nil
}

// Renew confirms this instance still holds the lock.
//
// A session-scoped advisory lock cannot be taken away while the session that
// holds it lives, so a live connection is the whole proof. What must not
// happen here is ensureConn's reconnect: a fresh connection holds no lock, and
// silently redialing would report continued leadership to a caller that has
// in fact lost it.
func (e *Elector) Renew(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.conn == nil || e.conn.IsClosed() || e.reconnected {
		return false, nil
	}
	pingCtx, cancel := context.WithTimeout(ctx, livenessTimeout)
	defer cancel()
	if err := e.conn.Ping(pingCtx); err != nil {
		// The session is gone, and with it the lock. This is a verdict,
		// not an outage: report the loss rather than an error, so the
		// loop stands down instead of retrying as though it still led.
		return false, nil
	}
	return true, nil
}

// Release gives up the lock without closing the connection, so the same
// elector can contend again.
func (e *Elector) Release(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.conn == nil || e.conn.IsClosed() {
		return nil
	}
	var released bool
	if err := e.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", e.key).Scan(&released); err != nil {
		return fmt.Errorf("pgadvisory: pg_advisory_unlock: %w", err)
	}
	return nil
}

// ensureConn verifies the dedicated connection is alive and transparently
// redials it otherwise, so a PostgreSQL restart between two acquisitions does
// not wedge leader election forever. It records the redial, because the new
// session holds nothing the old one held.
func (e *Elector) ensureConn(ctx context.Context) error {
	if e.conn != nil && !e.conn.IsClosed() {
		pingCtx, cancel := context.WithTimeout(ctx, reconnectPingTimeout)
		alive := e.conn.Ping(pingCtx) == nil
		cancel()
		if alive {
			return nil
		}
	}
	if e.conn != nil {
		_ = e.conn.Close(ctx)
		e.reconnected = true
	}
	conn, err := pgx.Connect(ctx, e.cfg.DSN)
	if err != nil {
		return fmt.Errorf("%w: pgadvisory: reconnecting: %w", leaderlock.ErrBackendUnavailable, err)
	}
	e.conn = conn
	return nil
}

func (e *Elector) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn != nil {
		_ = e.conn.Close(context.Background())
		e.conn = nil
	}
}
