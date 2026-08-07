package policyrelease_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func TestBuildArchive_ProducesTarballAndManifestWithVerifiableChecksum(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "policies/resources/patient_record.yaml"), "kind: ResourcePolicy")

	outDir := t.TempDir()
	archive, err := policyrelease.BuildArchive(context.Background(), policyrelease.ArchiveInput{
		SourceDir: filepath.Join(src, "policies"),
		Revision:  "root-v1.4.0",
		Commit:    "bbb",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	if archive.Revision != "root-v1.4.0" || archive.Commit != "bbb" {
		t.Fatalf("archive = %+v", archive)
	}

	tarball, err := os.ReadFile(archive.TarballPath)
	if err != nil {
		t.Fatalf("reading tarball: %v", err)
	}
	if len(tarball) == 0 {
		t.Fatal("tarball is empty")
	}

	if err := policyrelease.VerifyChecksum(archive); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}

	// Tampering with the archive after the fact must be detectable.
	if err := os.WriteFile(archive.TarballPath, append(tarball, 0xFF), 0o644); err != nil {
		t.Fatalf("tampering with tarball: %v", err)
	}
	if err := policyrelease.VerifyChecksum(archive); err == nil {
		t.Fatal("VerifyChecksum: want error after tampering with the tarball, got nil")
	}
}

func TestBuildArchive_NamesArchiveWithRevisionAndCommit(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "policies/resources/patient_record.yaml"), "kind: ResourcePolicy")
	outDir := t.TempDir()

	archive, err := policyrelease.BuildArchive(context.Background(), policyrelease.ArchiveInput{
		SourceDir: filepath.Join(src, "policies"),
		Revision:  "root-v1.4.0",
		Commit:    "bbb1234",
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	base := filepath.Base(archive.TarballPath)
	if base != "root-v1.4.0-bbb1234.tar.gz" {
		t.Fatalf("tarball name = %q, want root-v1.4.0-bbb1234.tar.gz", base)
	}
	manifestBase := filepath.Base(archive.ManifestPath)
	if manifestBase != "root-v1.4.0-bbb1234.manifest.json" {
		t.Fatalf("manifest name = %q, want root-v1.4.0-bbb1234.manifest.json", manifestBase)
	}
}
