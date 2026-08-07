package policyrelease

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractTarball extracts a gzip-compressed tarball, as returned by
// GiteaClient.FetchArchive, into dest. Every entry must resolve inside dest;
// an entry that would escape it (e.g. via "..") is rejected rather than
// silently written outside the destination the validation gate is about to
// trust.
func ExtractTarball(tarball []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return fmt.Errorf("policyrelease: opening gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("policyrelease: reading tar entry: %w", err)
		}

		target := filepath.Join(dest, header.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("policyrelease: tar entry %q escapes destination", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("policyrelease: creating directory %q: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("policyrelease: creating parent of %q: %w", target, err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("policyrelease: creating file %q: %w", target, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("policyrelease: writing file %q: %w", target, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("policyrelease: closing file %q: %w", target, err)
			}
		}
	}
}
