package policyrelease

import (
	"context"
	"fmt"

	"github.com/cerbos/cerbos-sdk-go/cerbos"
)

// CerbosAdminReloader reloads a replica's local Cerbos store through the
// pod-local Admin API, using the cerbos-sdk-go admin client
// (GRPCAdminClient.ReloadStore) that already backs the wider PDP tooling in
// this repository (libs/cerbosclient uses the same SDK for the runtime
// path). It is never reached through ingress (§14.4): endpoint.Address is
// always a pod-local address, wired in by the caller's compose or Kubernetes
// configuration, not one this controller resolves on its own.
type CerbosAdminReloader struct{}

// Reload implements Reloader.
func (CerbosAdminReloader) Reload(ctx context.Context, endpoint AdminEndpoint) error {
	opts := []cerbos.Opt{}
	if endpoint.PlaintextTLS {
		opts = append(opts, cerbos.WithPlaintext())
	}

	client, err := cerbos.NewAdminClientWithCredentials(endpoint.Address, endpoint.Username, endpoint.Password, opts...)
	if err != nil {
		return fmt.Errorf("policyrelease: connecting to admin API at %s: %w", endpoint.Address, err)
	}

	// wait=true blocks until the reload completes (or fails, e.g. because the
	// installed tree no longer compiles), so a nil error is the confirmation
	// that this replica now serves the target revision.
	if err := client.ReloadStore(ctx, true); err != nil {
		return fmt.Errorf("policyrelease: reloading store at %s: %w", endpoint.Address, err)
	}
	return nil
}
