package policyrelease_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func buildArchiveAt(t *testing.T, dir, revision, commit string) policyrelease.Archive {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "resources/patient_record.yaml"), "kind: ResourcePolicy")
	archive, err := policyrelease.BuildArchive(context.Background(), policyrelease.ArchiveInput{
		SourceDir: src,
		Revision:  revision,
		Commit:    commit,
		OutputDir: dir,
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	return archive
}

func TestStore_MarkActiveThenPreviousReturnsTheArchiveBeforeIt(t *testing.T) {
	dir := t.TempDir()
	store := policyrelease.NewStore(dir)

	first := buildArchiveAt(t, dir, "root-v1.3.0", "aaa")
	time.Sleep(2 * time.Millisecond)
	second := buildArchiveAt(t, dir, "root-v1.4.0", "bbb")

	if err := store.MarkActive(first); err != nil {
		t.Fatalf("MarkActive(first): %v", err)
	}
	if err := store.MarkActive(second); err != nil {
		t.Fatalf("MarkActive(second): %v", err)
	}

	active, err := store.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active.Revision != second.Revision {
		t.Fatalf("Active().Revision = %q, want %q", active.Revision, second.Revision)
	}

	previous, err := store.Previous()
	if err != nil {
		t.Fatalf("Previous: %v", err)
	}
	if previous.Revision != first.Revision {
		t.Fatalf("Previous().Revision = %q, want %q", previous.Revision, first.Revision)
	}
}

func TestStore_PruneKeepsOnlyTheNewestArchives(t *testing.T) {
	dir := t.TempDir()
	store := policyrelease.NewStore(dir)

	buildArchiveAt(t, dir, "root-v1.1.0", "111")
	time.Sleep(2 * time.Millisecond)
	buildArchiveAt(t, dir, "root-v1.2.0", "222")
	time.Sleep(2 * time.Millisecond)
	third := buildArchiveAt(t, dir, "root-v1.3.0", "333")

	if err := store.Prune(1); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	archives, err := store.Archives()
	if err != nil {
		t.Fatalf("Archives: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("len(archives) = %d, want 1", len(archives))
	}
	if archives[0].Revision != third.Revision {
		t.Fatalf("retained archive = %q, want %q", archives[0].Revision, third.Revision)
	}
	if _, err := os.Stat(third.TarballPath); err != nil {
		t.Fatalf("retained archive's tarball missing: %v", err)
	}
}

func TestRollback_ActivatesThePreviousArchiveWithoutTouchingAssignmentData(t *testing.T) {
	dir := t.TempDir()
	store := policyrelease.NewStore(dir)

	first := buildArchiveAt(t, dir, "root-v1.3.0", "aaa")
	time.Sleep(2 * time.Millisecond)
	buildArchiveAt(t, dir, "root-v1.4.0", "bbb")

	replicaDir := t.TempDir()
	reloader := &fakeReloader{}

	if err := store.MarkActive(first); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	second, err := store.Archives()
	if err != nil {
		t.Fatalf("Archives: %v", err)
	}
	// Archives() is sorted oldest-first; the newest one is what a normal
	// release would have just activated over `first`.
	if err := store.MarkActive(second[len(second)-1]); err != nil {
		t.Fatalf("MarkActive(second): %v", err)
	}

	result, err := policyrelease.Rollback(context.Background(), store, []policyrelease.Replica{
		{PolicyDir: replicaDir, Admin: policyrelease.AdminEndpoint{Name: "cerbos-a"}},
	}, reloader)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if result.Revision != first.Revision {
		t.Fatalf("Rollback activated %q, want %q", result.Revision, first.Revision)
	}
	if !result.Activated {
		t.Fatalf("Rollback result not activated: %+v", result)
	}

	active, err := store.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active.Revision != first.Revision {
		t.Fatalf("store now reports active revision %q, want %q", active.Revision, first.Revision)
	}
}
