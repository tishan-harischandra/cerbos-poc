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

	// Gitea's own /archive/{ref}.tar.gz endpoint wraps every entry in one
	// top-level directory (named after the ref, e.g. "root-policy-bbb1234"),
	// so the release tree is not always extractDir itself.
	releaseRoot, err := findReleaseRoot(extractDir)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: commit %s: %w", tag.Commit, err)
	}

	if err := Validate(ctx, releaseRoot, cfg.Validate); err != nil {
		validationErr := fmt.Errorf("policyrelease: tag %s failed validation: %w", tag.Name, err)
		if recordErr := recordOutcome(cfg.Store, ActivationResult{Revision: tag.Name}, validationErr); recordErr != nil {
			return ActivationResult{}, recordErr
		}
		return ActivationResult{}, validationErr
	}

	archive, err := BuildArchive(ctx, ArchiveInput{
		SourceDir: filepath.Join(releaseRoot, "policies"),
		Revision:  tag.Name,
		Commit:    tag.Commit,
		OutputDir: cfg.Store.dir,
	})
	if err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: packaging tag %s: %w", tag.Name, err)
	}

	result, err := InstallAndActivate(ctx, archive, cfg.Replicas, cfg.Reloader)
	if recordErr := recordOutcome(cfg.Store, result, err); recordErr != nil {
		return result, recordErr
	}
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

// recordOutcome records one RunOnce attempt in the store's history,
// regardless of whether it activated (issue #22's "a release that failed to
// activate is visibly distinguished from one that succeeded").
func recordOutcome(store *Store, result ActivationResult, runErr error) error {
	entry := HistoryEntry{
		Revision:  result.Revision,
		Activated: result.Activated,
	}
	if runErr != nil {
		entry.Error = runErr.Error()
	}
	return store.RecordAttempt(entry)
}

// findReleaseRoot returns the directory under extractDir that directly
// contains a "policies" subdirectory: extractDir itself, or - when the
// archive wrapped everything in one top-level directory, as Gitea's does -
// that single subdirectory.
func findReleaseRoot(extractDir string) (string, error) {
	if isDir(filepath.Join(extractDir, "policies")) {
		return extractDir, nil
	}

	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", fmt.Errorf("reading extracted tree: %w", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		candidate := filepath.Join(extractDir, dirs[0])
		if isDir(filepath.Join(candidate, "policies")) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no \"policies\" directory found under %s", extractDir)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
