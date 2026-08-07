package policyrelease_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

type fakeFetcher struct {
	tags    []policyrelease.Tag
	tarball []byte
}

func (f *fakeFetcher) ListTags(ctx context.Context) ([]policyrelease.Tag, error) {
	return f.tags, nil
}

func (f *fakeFetcher) FetchArchive(ctx context.Context, commit string) ([]byte, error) {
	return f.tarball, nil
}

// releaseTreeTarball wraps the release tree in a single top-level directory,
// the same shape Gitea's own /archive/{ref}.tar.gz endpoint produces (e.g.
// "root-policy-bbb1234/..."), so RunOnce is exercised against a real
// archive shape rather than one flattened for convenience.
func releaseTreeTarball(t *testing.T) []byte {
	t.Helper()
	return buildTestTarball(t, map[string]string{
		"root-policy-bbb/catalog/resources/patient_record.yaml":  "actions: [read, update]",
		"root-policy-bbb/policies/resources/patient_record.yaml": "kind: ResourcePolicy",
	})
}

func TestRunOnce_ValidatesPackagesInstallsAndActivatesTheSelectedTag(t *testing.T) {
	storeDir := t.TempDir()
	replicaDir := t.TempDir()
	workDir := t.TempDir()

	cfg := policyrelease.ReleaseConfig{
		Fetcher:   &fakeFetcher{tags: []policyrelease.Tag{{Name: "root-v1.4.0", Commit: "bbb", Protected: true}}, tarball: releaseTreeTarball(t)},
		TagPrefix: "root-v",
		Validate:  policyrelease.ValidateOptions{Compiler: &fakeCompiler{}},
		Replicas:  []policyrelease.Replica{{PolicyDir: replicaDir, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}}},
		Reloader:  &fakeReloader{},
		Store:     policyrelease.NewStore(storeDir),
		WorkDir:   workDir,
	}

	result, err := policyrelease.RunOnce(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !result.Activated || result.Revision != "root-v1.4.0" {
		t.Fatalf("result = %+v", result)
	}

	if _, err := os.Stat(filepath.Join(replicaDir, "resources", "patient_record.yaml")); err != nil {
		t.Fatalf("installed policy missing: %v", err)
	}

	active, err := cfg.Store.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active.Revision != "root-v1.4.0" {
		t.Fatalf("active.Revision = %q, want root-v1.4.0", active.Revision)
	}
}

func TestRunOnce_SkipsWhenSelectedTagIsAlreadyActive(t *testing.T) {
	storeDir := t.TempDir()
	replicaDir := t.TempDir()
	fetcher := &fakeFetcher{tags: []policyrelease.Tag{{Name: "root-v1.4.0", Commit: "bbb", Protected: true}}, tarball: releaseTreeTarball(t)}

	cfg := policyrelease.ReleaseConfig{
		Fetcher:   fetcher,
		TagPrefix: "root-v",
		Validate:  policyrelease.ValidateOptions{Compiler: &fakeCompiler{}},
		Replicas:  []policyrelease.Replica{{PolicyDir: replicaDir, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}}},
		Reloader:  &fakeReloader{},
		Store:     policyrelease.NewStore(storeDir),
		WorkDir:   t.TempDir(),
	}

	if _, err := policyrelease.RunOnce(context.Background(), cfg); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	reloader := &fakeReloader{}
	cfg.Reloader = reloader
	cfg.WorkDir = t.TempDir()
	result, err := policyrelease.RunOnce(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if len(reloader.calls) != 0 {
		t.Fatalf("second RunOnce reloaded replicas for an already-active tag: %v", reloader.calls)
	}
	if result.Revision != "root-v1.4.0" || !result.Activated {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunOnce_NeverActivatesATagThatFailsValidation(t *testing.T) {
	storeDir := t.TempDir()
	replicaDir := t.TempDir()

	cfg := policyrelease.ReleaseConfig{
		Fetcher:   &fakeFetcher{tags: []policyrelease.Tag{{Name: "root-v1.4.0", Commit: "bbb", Protected: true}}, tarball: releaseTreeTarball(t)},
		TagPrefix: "root-v",
		Validate:  policyrelease.ValidateOptions{Compiler: &fakeCompiler{err: context.DeadlineExceeded}},
		Replicas:  []policyrelease.Replica{{PolicyDir: replicaDir, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}}},
		Reloader:  &fakeReloader{},
		Store:     policyrelease.NewStore(storeDir),
		WorkDir:   t.TempDir(),
	}

	if _, err := policyrelease.RunOnce(context.Background(), cfg); err == nil {
		t.Fatal("RunOnce: want error when validation fails, got nil")
	}

	if _, err := os.Stat(filepath.Join(replicaDir, "resources")); !os.IsNotExist(err) {
		t.Fatal("a failing validation must never reach install")
	}
	if _, err := cfg.Store.Active(); err == nil {
		t.Fatal("Active: want error, no revision should be marked active")
	}

	history, err := cfg.Store.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].Revision != "root-v1.4.0" || history[0].Activated {
		t.Fatalf("history[0] = %+v, want a non-activated root-v1.4.0", history[0])
	}
	if history[0].Error == "" {
		t.Fatal("history[0].Error is empty, want the validation failure recorded")
	}
}

func TestRunOnce_RecordsASuccessfulActivationInHistory(t *testing.T) {
	storeDir := t.TempDir()
	replicaDir := t.TempDir()

	cfg := policyrelease.ReleaseConfig{
		Fetcher:   &fakeFetcher{tags: []policyrelease.Tag{{Name: "root-v1.4.0", Commit: "bbb", Protected: true}}, tarball: releaseTreeTarball(t)},
		TagPrefix: "root-v",
		Validate:  policyrelease.ValidateOptions{Compiler: &fakeCompiler{}},
		Replicas:  []policyrelease.Replica{{PolicyDir: replicaDir, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}}},
		Reloader:  &fakeReloader{},
		Store:     policyrelease.NewStore(storeDir),
		WorkDir:   t.TempDir(),
	}

	if _, err := policyrelease.RunOnce(context.Background(), cfg); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	history, err := cfg.Store.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].Revision != "root-v1.4.0" || !history[0].Activated || history[0].Error != "" {
		t.Fatalf("history[0] = %+v, want an activated root-v1.4.0 with no error", history[0])
	}
}

func TestRunOnce_RecordsAFailedInstallInHistory(t *testing.T) {
	storeDir := t.TempDir()

	cfg := policyrelease.ReleaseConfig{
		Fetcher:   &fakeFetcher{tags: []policyrelease.Tag{{Name: "root-v1.4.0", Commit: "bbb", Protected: true}}, tarball: releaseTreeTarball(t)},
		TagPrefix: "root-v",
		Validate:  policyrelease.ValidateOptions{Compiler: &fakeCompiler{}},
		Replicas:  []policyrelease.Replica{{PolicyDir: t.TempDir(), Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}}},
		Reloader:  &fakeReloader{failFor: map[string]error{"cerbos-a": context.DeadlineExceeded}},
		Store:     policyrelease.NewStore(storeDir),
		WorkDir:   t.TempDir(),
	}

	if _, err := policyrelease.RunOnce(context.Background(), cfg); err == nil {
		t.Fatal("RunOnce: want error when a replica fails to reload, got nil")
	}

	history, err := cfg.Store.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].Revision != "root-v1.4.0" || history[0].Activated {
		t.Fatalf("history[0] = %+v, want a non-activated root-v1.4.0", history[0])
	}
	if history[0].Error == "" {
		t.Fatal("history[0].Error is empty, want the reload failure recorded")
	}
}
