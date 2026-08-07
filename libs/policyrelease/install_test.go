package policyrelease_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func buildTestArchive(t *testing.T) policyrelease.Archive {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "resources/patient_record.yaml"), "kind: ResourcePolicy")

	archive, err := policyrelease.BuildArchive(context.Background(), policyrelease.ArchiveInput{
		SourceDir: src,
		Revision:  "root-v1.4.0",
		Commit:    "bbb",
		OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	return archive
}

type fakeReloader struct {
	failFor map[string]error
	calls   []string
}

func (f *fakeReloader) Reload(ctx context.Context, endpoint policyrelease.AdminEndpoint) error {
	f.calls = append(f.calls, endpoint.Name)
	if err, ok := f.failFor[endpoint.Name]; ok {
		return err
	}
	return nil
}

func TestInstallAndActivate_ActivatesWhenEveryReplicaReloadsCleanly(t *testing.T) {
	archive := buildTestArchive(t)
	replicaA := t.TempDir()
	replicaB := t.TempDir()
	reloader := &fakeReloader{}

	result, err := policyrelease.InstallAndActivate(context.Background(), archive, []policyrelease.Replica{
		{PolicyDir: replicaA, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}},
		{PolicyDir: replicaB, Admin: policyrelease.AdminEndpoint{Name: "cerbos-b"}},
	}, reloader)
	if err != nil {
		t.Fatalf("InstallAndActivate: %v", err)
	}
	if !result.Activated {
		t.Fatalf("result.Activated = false, want true: %+v", result)
	}

	for _, dir := range []string{replicaA, replicaB} {
		got, err := os.ReadFile(filepath.Join(dir, "resources/patient_record.yaml"))
		if err != nil {
			t.Fatalf("reading installed policy in %s: %v", dir, err)
		}
		if string(got) != "kind: ResourcePolicy" {
			t.Fatalf("installed content in %s = %q", dir, got)
		}
	}
}

func TestInstallAndActivate_RefusesActivationWhenAReplicaFailsToReload(t *testing.T) {
	archive := buildTestArchive(t)
	replicaA := t.TempDir()
	replicaB := t.TempDir()
	reloader := &fakeReloader{failFor: map[string]error{"cerbos-b": context.DeadlineExceeded}}

	result, err := policyrelease.InstallAndActivate(context.Background(), archive, []policyrelease.Replica{
		{PolicyDir: replicaA, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}},
		{PolicyDir: replicaB, Admin: policyrelease.AdminEndpoint{Name: "cerbos-b"}},
	}, reloader)
	if err == nil {
		t.Fatal("InstallAndActivate: want error when a replica fails to reload, got nil")
	}
	if result.Activated {
		t.Fatalf("result.Activated = true, want false: %+v", result)
	}
	if len(result.Failed) != 1 || result.Failed["cerbos-b"] == nil {
		t.Fatalf("result.Failed = %+v, want an entry for cerbos-b", result.Failed)
	}
}

func TestInstallAndActivate_NeverServesAPartiallyWrittenPolicySet(t *testing.T) {
	archive := buildTestArchive(t)
	replicaDir := t.TempDir()
	// Simulate a pre-existing served revision so a failed install cannot
	// leave the directory half old, half new.
	writeFile(t, filepath.Join(replicaDir, "resources", "existing.yaml"), "kind: ResourcePolicy")

	reloader := &fakeReloader{}
	_, err := policyrelease.InstallAndActivate(context.Background(), archive, []policyrelease.Replica{
		{PolicyDir: replicaDir, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}},
	}, reloader)
	if err != nil {
		t.Fatalf("InstallAndActivate: %v", err)
	}

	if _, err := os.Stat(filepath.Join(replicaDir, "resources", "existing.yaml")); !os.IsNotExist(err) {
		t.Fatal("stale file from the previous revision survived the atomic install")
	}
	if _, err := os.Stat(filepath.Join(replicaDir, "resources", "patient_record.yaml")); err != nil {
		t.Fatalf("new revision file missing after install: %v", err)
	}
}
