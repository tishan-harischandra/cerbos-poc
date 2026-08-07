package policyrelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// AdminEndpoint identifies one Cerbos replica's pod-local Admin API. It is
// reachable only pod-locally (§14.4): the policy-agent talking to it never
// goes through ingress, and the address is never one this controller's own
// caller could reach from outside the pod.
type AdminEndpoint struct {
	Name         string
	Address      string
	Username     string
	Password     string
	PlaintextTLS bool
}

// Replica is one Cerbos replica's local policy directory and Admin API.
type Replica struct {
	PolicyDir string
	Admin     AdminEndpoint
}

// Reloader triggers a local Cerbos store reload after an atomic install, as
// the real GRPCAdminClient.ReloadStore does.
type Reloader interface {
	Reload(ctx context.Context, endpoint AdminEndpoint) error
}

// ActivationResult reports which replicas confirmed the target revision.
type ActivationResult struct {
	Revision  string
	Activated bool
	Confirmed []string
	Failed    map[string]error
}

// InstallAndActivate atomically installs archive on every replica and asks
// each one to reload its local store (§13.2 steps 20-21). The release is
// marked active only when every replica confirms; a replica that fails to
// reload is named in the result and prevents activation, but the replicas
// that did succeed keep the atomic install they already completed - "no
// replica ever serves a partially written policy set" is a per-replica
// guarantee, not a promise that the fleet moves together.
func InstallAndActivate(ctx context.Context, archive Archive, replicas []Replica, reloader Reloader) (ActivationResult, error) {
	result := ActivationResult{
		Revision: archive.Revision,
		Failed:   make(map[string]error),
	}

	for _, replica := range replicas {
		if err := atomicInstall(archive.TarballPath, replica.PolicyDir); err != nil {
			result.Failed[replica.Admin.Name] = fmt.Errorf("installing: %w", err)
			continue
		}
		if err := reloader.Reload(ctx, replica.Admin); err != nil {
			result.Failed[replica.Admin.Name] = fmt.Errorf("reloading store: %w", err)
			continue
		}
		result.Confirmed = append(result.Confirmed, replica.Admin.Name)
	}

	if len(result.Failed) > 0 {
		return result, fmt.Errorf("policyrelease: revision %s not activated, %d of %d replicas failed: %v",
			archive.Revision, len(result.Failed), len(replicas), result.Failed)
	}

	result.Activated = true
	return result, nil
}

// atomicInstall extracts tarballPath into a fresh sibling directory of dest,
// then swaps it into place with a rename. Directory renames on the same
// filesystem are atomic, so a reader of dest never observes a mix of the
// previous and new revision - at worst, if dest does not exist yet, a
// concurrent reader sees a transient "not found" between the two renames
// below rather than a partially written tree.
func atomicInstall(tarballPath, dest string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("policyrelease: creating %s: %w", parent, err)
	}

	staging, err := os.MkdirTemp(parent, ".policyrelease-install-*")
	if err != nil {
		return fmt.Errorf("policyrelease: creating staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	tarball, err := os.ReadFile(tarballPath)
	if err != nil {
		return fmt.Errorf("policyrelease: reading tarball: %w", err)
	}
	if err := ExtractTarball(tarball, staging); err != nil {
		return fmt.Errorf("policyrelease: extracting tarball: %w", err)
	}

	// os.Rename cannot replace a non-empty directory directly, so the
	// previous revision is moved aside first and removed only once the new
	// one is already in place.
	previous := dest + ".previous"
	os.RemoveAll(previous)
	hadPrevious := false
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, previous); err != nil {
			return fmt.Errorf("policyrelease: moving previous revision aside at %s: %w", dest, err)
		}
		hadPrevious = true
	}

	if err := os.Rename(staging, dest); err != nil {
		if hadPrevious {
			os.Rename(previous, dest)
		}
		return fmt.Errorf("policyrelease: activating staged revision at %s: %w", dest, err)
	}

	if hadPrevious {
		os.RemoveAll(previous)
	}
	return nil
}
