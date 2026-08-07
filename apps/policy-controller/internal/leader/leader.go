// Package leader implements leader election for the policy controller
// through a PostgreSQL advisory lock, as issue #21 requires: compose has no
// leader election of its own, so this has to be real and portable to
// multiple controller replicas running against the same database, in compose
// or in Kubernetes alike.
package leader

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Elector holds one dedicated PostgreSQL connection and uses it to contend
// for a session-scoped advisory lock. The lock is tied to the connection, not
// to a row or a lease that needs renewing: closing or losing the connection
// releases it automatically, so a crashed leader cannot hold the fleet
// hostage.
type Elector struct {
	conn    *pgx.Conn
	lockKey int64
}

// New opens a dedicated connection to dsn for contending on lockKey. Every
// Elector instance sharing the same lockKey across the fleet contends for the
// same lock; exactly one of them ever holds it at a time.
func New(ctx context.Context, dsn string, lockKey int64) (*Elector, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("leader: connecting: %w", err)
	}
	return &Elector{conn: conn, lockKey: lockKey}, nil
}

// TryAcquire attempts to become leader without blocking. It returns false,
// with no error, when another instance already holds the lock.
func (e *Elector) TryAcquire(ctx context.Context) (bool, error) {
	var acquired bool
	if err := e.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", e.lockKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("leader: pg_try_advisory_lock: %w", err)
	}
	return acquired, nil
}

// Release gives up leadership without closing the connection, so the same
// Elector can contend again later.
func (e *Elector) Release(ctx context.Context) error {
	var released bool
	if err := e.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", e.lockKey).Scan(&released); err != nil {
		return fmt.Errorf("leader: pg_advisory_unlock: %w", err)
	}
	return nil
}

// Close releases the underlying connection. Any lock this Elector held is
// released as a side effect, since PostgreSQL ties a session-scoped advisory
// lock to the session that acquired it.
func (e *Elector) Close(ctx context.Context) error {
	return e.conn.Close(ctx)
}
