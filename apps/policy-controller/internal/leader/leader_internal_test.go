package leader

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestElector_ReconnectsAfterConnectionLoss guards against issue #26's chaos
// suite finding: a PostgreSQL restart underneath a live Elector left its
// dedicated connection half-open, so every later TryAcquire failed the same
// way and the policy controller silently stopped processing releases.
func TestElector_ReconnectsAfterConnectionLoss(t *testing.T) {
	dsn := os.Getenv("ASSIGNMENTSTORE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ASSIGNMENTSTORE_POSTGRES_DSN is not set")
	}
	ctx := context.Background()

	e, err := New(ctx, dsn, 384756123)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close(ctx)

	acquired, err := e.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("TryAcquire (before): %v", err)
	}
	if !acquired {
		t.Fatal("TryAcquire (before) = false, want true on an uncontended lock")
	}

	killer, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting the killer: %v", err)
	}
	defer killer.Close(ctx)

	// Simulate the connection dying underneath the Elector - a PostgreSQL
	// restart being the case that matters - by terminating its backend
	// from a second connection, without ever closing e.conn ourselves.
	pid := e.conn.PgConn().PID()
	if _, err := killer.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatalf("pg_terminate_backend: %v", err)
	}
	// Give PostgreSQL a moment to actually tear down the backend before
	// asserting the Elector notices and recovers.
	time.Sleep(200 * time.Millisecond)

	acquiredAfter, err := e.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("TryAcquire (after connection loss): %v", err)
	}
	if !acquiredAfter {
		t.Fatal("TryAcquire (after connection loss) = false, want true: the Elector should reconnect and reacquire the now-unheld lock")
	}
}
