package policyrelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Fetcher is the subset of GiteaClient the release pipeline needs: list the
// candidate tags, then fetch the exact commit a selected tag points at.
type Fetcher interface {
	ListTags(ctx context.Context) ([]Tag, error)
	FetchArchive(ctx context.Context, commit string) ([]byte, error)
}

// ReleaseConfig wires together one pass of the root policy release pipeline
// (Figure 5, §13).
type ReleaseConfig struct {
	Fetcher   Fetcher
	TagPrefix string
	Validate  ValidateOptions
	Replicas  []Replica
	Reloader  Reloader
	Store     *Store
	// RetainCount is how many built archives to keep after a successful
	// activation. Zero means keep every archive.
	RetainCount int
	// WorkDir is scratch space the fetched commit is extracted into. It is
	// safe to reuse across calls; RunOnce always extracts into a fresh
	// subdirectory of it.
	WorkDir string
}

// RunOnce runs one poll-validate-package-install-activate pass. Dynamic role
// and user assignment changes never reach this function: it has no
// dependency on assignmentstore, and its only writes are the extracted
// scratch tree, the archive store, and each replica's policy directory
// (§13.1).
func RunOnce(ctx context.Context, cfg ReleaseConfig) (ActivationResult, error) {
	tags, err := cfg.Fetcher.ListTags(ctx)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: listing tags: %w", err)
	}
	tag, err := SelectTag(tags, cfg.TagPrefix)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: selecting tag: %w", err)
	}

	if active, err := cfg.Store.Active(); err == nil && active.Revision == tag.Name {
		return ActivationResult{Revision: tag.Name, Activated: true}, nil
	}

	tarball, err := cfg.Fetcher.FetchArchive(ctx, tag.Commit)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: fetching commit %s: %w", tag.Commit, err)
	}

	extractDir := filepath.Join(cfg.WorkDir, tag.Commit)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: preparing extraction directory: %w", err)
	}
	if err := ExtractTarball(tarball, extractDir); err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: extracting commit %s: %w", tag.Commit, err)
	}

	if err := Validate(ctx, extractDir, cfg.Validate); err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: tag %s failed validation: %w", tag.Name, err)
	}

	archive, err := BuildArchive(ctx, ArchiveInput{
		SourceDir: filepath.Join(extractDir, "policies"),
		Revision:  tag.Name,
		Commit:    tag.Commit,
		OutputDir: cfg.Store.dir,
	})
	if err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: packaging tag %s: %w", tag.Name, err)
	}

	result, err := InstallAndActivate(ctx, archive, cfg.Replicas, cfg.Reloader)
	if err != nil {
		return result, err
	}

	if err := cfg.Store.MarkActive(archive); err != nil {
		return result, err
	}
	if cfg.RetainCount > 0 {
		if err := cfg.Store.Prune(cfg.RetainCount); err != nil {
			return result, err
		}
	}
	return result, nil
}
