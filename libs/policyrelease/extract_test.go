package policyrelease_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func buildTestTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatalf("writing header for %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("writing content for %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}
	return buf.Bytes()
}

func TestExtractTarball_WritesFilesUnderDestination(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"root-policy-bbb/resources/patient_record.yaml": "kind: ResourcePolicy",
		"root-policy-bbb/schemas/patient_record.json":   "{}",
	})

	dest := t.TempDir()
	if err := policyrelease.ExtractTarball(tarball, dest); err != nil {
		t.Fatalf("ExtractTarball: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "root-policy-bbb/resources/patient_record.yaml"))
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(got) != "kind: ResourcePolicy" {
		t.Fatalf("extracted content = %q", got)
	}
}

func TestExtractTarball_RejectsPathTraversal(t *testing.T) {
	tarball := buildTestTarball(t, map[string]string{
		"../escape.yaml": "kind: ResourcePolicy",
	})

	dest := t.TempDir()
	if err := policyrelease.ExtractTarball(tarball, dest); err == nil {
		t.Fatal("ExtractTarball: want error for path traversal entry, got nil")
	}
}
