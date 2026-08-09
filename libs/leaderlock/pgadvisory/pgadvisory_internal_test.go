package pgadvisory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestTheElectorReconnectsAfterItsConnectionDies guards the finding issue
// #26's chaos suite made against the policy controller's own elector, which
// this adapter was promoted from: a PostgreSQL restart underneath a live
// elector left its dedicated connection half-open, so every later acquisition
// failed the same way and the controller silently stopped processing
// releases. The fix has to survive the move to the port.
func TestTheElectorReconnectsAfterItsConnectionDies(t *testing.T) {
	dsn := os.Getenv("ASSIGNMENTSTORE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ASSIGNMENTSTORE_POSTGRES_DSN is not set")
	}
	ctx := context.Background()

	elector, err := New(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	elector.key = 384756123
	defer elector.close()

	acquired, err := elector.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire (before): %v", err)
	}
	if !acquired {
		t.Fatal("Acquire (before) = false, want true on an uncontended lock")
	}

	killer, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting the killer: %v", err)
	}
	defer killer.Close(ctx)

	// Simulate the connection dying underneath the elector - a
	// PostgreSQL restart being the case that matters - by terminating
	// its backend from a second connection, without ever closing it
	// ourselves.
	pid := elector.conn.PgConn().PID()
	if _, err := killer.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatalf("pg_terminate_backend: %v", err)
	}
	// Give PostgreSQL a moment to actually tear down the backend.
	time.Sleep(200 * time.Millisecond)

	// The dead session took the lock with it, so the honest answer to
	// "do I still lead?" is no - and it must be a verdict, not an error,
	// or the loop retries as though nothing had happened.
	held, err := elector.Renew(ctx)
	if err != nil {
		t.Fatalf("Renew after connection loss: %v", err)
	}
	if held {
		t.Error("Renew = true after the session died, want the elector to stand down")
	}

	acquiredAfter, err := elector.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire (after connection loss): %v", err)
	}
	if !acquiredAfter {
		t.Fatal("Acquire (after connection loss) = false, want the elector to reconnect and retake the now-unheld lock")
	}
}
