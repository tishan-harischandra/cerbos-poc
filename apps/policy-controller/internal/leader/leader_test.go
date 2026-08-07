package leader_test

import (
	"context"
	"os"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/apps/policy-controller/internal/leader"
)

const postgresDSNEnv = "ASSIGNMENTSTORE_POSTGRES_DSN"

func dsnOrSkip(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(postgresDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", postgresDSNEnv)
	}
	return dsn
}

func TestElector_OnlyOneInstanceAcquiresTheLockAtATime(t *testing.T) {
	dsn := dsnOrSkip(t)
	ctx := context.Background()

	first, err := leader.New(ctx, dsn, 918273645)
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	defer first.Close(ctx)

	second, err := leader.New(ctx, dsn, 918273645)
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	defer second.Close(ctx)

	acquired, err := first.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("first.TryAcquire: %v", err)
	}
	if !acquired {
		t.Fatal("first.TryAcquire = false, want true on an uncontended lock")
	}

	stillPassive, err := second.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("second.TryAcquire: %v", err)
	}
	if stillPassive {
		t.Fatal("second.TryAcquire = true, want false while first holds the lock")
	}

	if err := first.Release(ctx); err != nil {
		t.Fatalf("first.Release: %v", err)
	}

	nowAcquired, err := second.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("second.TryAcquire after release: %v", err)
	}
	if !nowAcquired {
		t.Fatal("second.TryAcquire = false after first released, want true")
	}
}
