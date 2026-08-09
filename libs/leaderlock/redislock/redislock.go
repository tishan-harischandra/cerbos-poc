// Package redislock is the leader election adapter backed by a Redis key.
//
// It is for an installation that already runs Redis and would rather not put
// election traffic on its database. Redis is optional here in the way the
// oracle profile is optional: nothing else in this platform needs it, so it
// is a compose profile and a kustomize component rather than part of the
// stack.
//
// The guarantee is a lease, with the split-brain window the port documents,
// and on a Redis failover it is weaker still: a replica promoted before it
// replicated the key has no record of the lock, and will happily grant it to
// somebody else. Redlock exists to address that and is deliberately not
// implemented here - it needs several independent Redis instances, which is
// more infrastructure than an optional convenience should ask for. An
// installation that needs a stronger guarantee should choose PG_ADVISORY.
//
// Nothing outside the provider factory may import this package.
package redislock

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/lease"
)

// commandTimeout bounds one exchange with Redis.
const commandTimeout = 5 * time.Second

// Config describes the Redis instance the election runs on.
type Config struct {
	// Address is host:port.
	Address string
	// Username and Password authenticate, when Redis requires it.
	Username string
	Password string
	// KeyPrefix namespaces the election keys, so two installations
	// sharing one Redis do not share one election.
	KeyPrefix string

	// Identity is the value written under the key, so a `GET` names the
	// replica that leads.
	Identity string

	TTL           time.Duration
	RenewInterval time.Duration
	RetryInterval time.Duration

	// PauseRenewal reproduces a stalled leader for the contract suite.
	// Production leaves it nil.
	PauseRenewal <-chan struct{}
	// OnError, if set, receives backend failures.
	OnError func(error)
}

// DefaultKeyPrefix namespaces this platform's elections.
const DefaultKeyPrefix = "cerbos-poc/leader-election/"

// Elector contends for one Redis key.
type Elector struct {
	cfg Config

	mu   sync.Mutex
	conn *conn
	key  string
}

// New returns the adapter.
func New(cfg Config) (*Elector, error) {
	switch {
	case cfg.Address == "":
		return nil, errors.New("redislock: an address is required")
	case cfg.Identity == "":
		return nil, errors.New("redislock: an identity is required, or the key names nobody")
	case cfg.TTL <= 0:
		return nil, errors.New("redislock: a ttl is required: this lease expires on time")
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = DefaultKeyPrefix
	}
	return &Elector{cfg: cfg}, nil
}

// Close releases the connection.
func (e *Elector) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn == nil {
		return nil
	}
	err := e.conn.close()
	e.conn = nil
	return err
}

// Run contends for the key the election name maps to.
func (e *Elector) Run(ctx context.Context, election leaderlock.Name, onElected func(context.Context)) error {
	if err := election.Validate(); err != nil {
		return err
	}
	e.key = e.cfg.KeyPrefix + string(election)
	defer func() { _ = e.Close() }()

	return lease.Run(ctx, lease.Config{
		TTL:           e.cfg.TTL,
		RenewInterval: e.cfg.RenewInterval,
		RetryInterval: e.cfg.RetryInterval,
		PauseRenewal:  e.cfg.PauseRenewal,
		OnError:       e.cfg.OnError,
	}, e, onElected)
}

// Acquire takes the key if nobody holds it.
//
// SET NX PX is the whole of it: the key is created only if absent, and it
// carries its own expiry, so a leader that dies is forgotten by Redis without
// anybody having to notice.
func (e *Elector) Acquire(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	e.mu.Lock()
	defer e.mu.Unlock()
	connection, err := e.connect(ctx)
	if err != nil {
		return false, err
	}

	_, err = connection.do(ctx, "SET", e.key, e.cfg.Identity, "NX", "PX", e.ttlMillis())
	switch {
	case errors.Is(err, ErrNil):
		// Somebody else holds it. Not a failure.
		return false, nil
	case err != nil:
		e.discard()
		return false, fmt.Errorf("%w: redislock: acquiring: %w", leaderlock.ErrBackendUnavailable, err)
	}
	return true, nil
}

// Renew extends the lease only while the key still names us.
//
// The compare and the extend have to be one step: reading the key, deciding
// it is ours and then extending it would extend a rival's lease whenever the
// key turned over in between. WATCH makes the extend conditional on the key
// not having changed since the read, and EXEC reports the abort.
func (e *Elector) Renew(ctx context.Context) (bool, error) {
	return e.compareAndSwap(ctx, func(connection *conn, ctx context.Context) error {
		_, err := connection.do(ctx, "PEXPIRE", e.key, e.ttlMillis())
		return err
	})
}

// Release hands the election over by deleting the key, but only while it is
// still ours: a slow shutdown must not delete a lease a rival already holds.
func (e *Elector) Release(ctx context.Context) error {
	_, err := e.compareAndSwap(ctx, func(connection *conn, ctx context.Context) error {
		_, err := connection.do(ctx, "DEL", e.key)
		return err
	})
	return err
}

// compareAndSwap runs queue against the key inside a transaction that is
// abandoned if anybody else touches the key first, and reports whether it
// committed.
func (e *Elector) compareAndSwap(ctx context.Context, queue func(*conn, context.Context) error) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	e.mu.Lock()
	defer e.mu.Unlock()
	connection, err := e.connect(ctx)
	if err != nil {
		return false, err
	}

	if _, err := connection.do(ctx, "WATCH", e.key); err != nil {
		e.discard()
		return false, fmt.Errorf("%w: redislock: watching the key: %w", leaderlock.ErrBackendUnavailable, err)
	}

	holder, err := connection.do(ctx, "GET", e.key)
	if errors.Is(err, ErrNil) || (err == nil && holder != e.cfg.Identity) {
		// The lease expired, or a rival took it. Stop watching, or the
		// connection carries the watch into the next command.
		_, _ = connection.do(ctx, "UNWATCH")
		return false, nil
	}
	if err != nil {
		e.discard()
		return false, fmt.Errorf("%w: redislock: reading the key: %w", leaderlock.ErrBackendUnavailable, err)
	}

	if _, err := connection.do(ctx, "MULTI"); err != nil {
		e.discard()
		return false, fmt.Errorf("%w: redislock: opening a transaction: %w", leaderlock.ErrBackendUnavailable, err)
	}
	if err := queue(connection, ctx); err != nil {
		_, _ = connection.do(ctx, "DISCARD")
		return false, fmt.Errorf("%w: redislock: queueing the change: %w", leaderlock.ErrBackendUnavailable, err)
	}

	_, err = connection.do(ctx, "EXEC")
	switch {
	case errors.Is(err, ErrNil):
		// The key changed underneath the transaction, so it was
		// abandoned: somebody else is the leader now.
		return false, nil
	case err != nil:
		e.discard()
		return false, fmt.Errorf("%w: redislock: committing: %w", leaderlock.ErrBackendUnavailable, err)
	}
	return true, nil
}

// connect returns the adapter's single connection, dialling it if there is
// none. The caller holds the mutex.
func (e *Elector) connect(ctx context.Context) (*conn, error) {
	if e.conn != nil {
		return e.conn, nil
	}
	connection, err := dial(ctx, e.cfg.Address, e.cfg.Username, e.cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", leaderlock.ErrBackendUnavailable, err)
	}
	e.conn = connection
	return connection, nil
}

// discard drops a connection an error left in an unknown state - mid
// transaction, or with a half-read reply still buffered. Reusing it would
// pair one command's request with another's reply. The caller holds the
// mutex.
func (e *Elector) discard() {
	if e.conn != nil {
		_ = e.conn.close()
		e.conn = nil
	}
}

// ttlMillis is the lease length as PX wants it. A sub-millisecond lease
// rounds up to one, never to zero.
func (e *Elector) ttlMillis() string {
	millis := e.cfg.TTL.Milliseconds()
	if millis < 1 {
		millis = 1
	}
	return strconv.FormatInt(millis, 10)
}
