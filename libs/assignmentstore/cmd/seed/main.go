// Command seed writes the demo role matrix into the authorization database.
//
// It runs as a container step of `make up` rather than as part of the ADS: the
// service reads the matrix and has no business writing it, and a service that
// seeded itself on boot would quietly re-create rows an administrator had
// deliberately removed.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/demoseed"
	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore/postgresstore"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("seed: the demo role matrix is in place")
}

func run() error {
	dsn := os.Getenv("ASSIGNMENTSTORE_POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("ASSIGNMENTSTORE_POSTGRES_DSN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store, err := postgresstore.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	if err := store.Ping(ctx); err != nil {
		return fmt.Errorf("the authorization database is not reachable: %w", err)
	}

	return demoseed.Apply(ctx, store, time.Now().UTC())
}
