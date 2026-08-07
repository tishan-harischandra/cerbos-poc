package policyrelease

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ArchiveInput describes what to package into an immutable release archive.
type ArchiveInput struct {
	// SourceDir is the validated policy directory (the same one just passed
	// to Compiler.Compile).
	SourceDir string
	// Revision is the selected Git tag name, e.g. "root-v1.4.0".
	Revision string
	// Commit is the exact commit SHA the tag pointed at.
	Commit string
	// OutputDir is where the tarball and manifest are written.
	OutputDir string
}

// Manifest is the release manifest committed alongside the tarball: a
// separate, human-and-machine-readable record of what the archive contains
// and how to verify it (§13.2 step 19).
type Manifest struct {
	Revision  string    `json:"revision"`
	Commit    string    `json:"commit"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"createdAt"`
}

// Archive is a built, immutable release: a tar.gz of the validated policy
// tree plus its manifest. Once written, neither file is modified again;
// activating a different revision means building a new Archive, never
// editing this one.
type Archive struct {
	Revision     string
	Commit       string
	TarballPath  string
	ManifestPath string
	SHA256       string
}

// BuildArchive packages input.SourceDir into a tar.gz named after the
// revision and commit, and writes a separate manifest carrying its SHA-256
// checksum so the checksum can be verified independently of the archive
// itself.
func BuildArchive(ctx context.Context, input ArchiveInput) (Archive, error) {
	if err := os.MkdirAll(input.OutputDir, 0o755); err != nil {
		return Archive{}, fmt.Errorf("policyrelease: creating output directory: %w", err)
	}

	baseName := fmt.Sprintf("%s-%s", input.Revision, input.Commit)
	tarballPath := filepath.Join(input.OutputDir, baseName+".tar.gz")
	manifestPath := filepath.Join(input.OutputDir, baseName+".manifest.json")

	if err := writeTarball(ctx, input.SourceDir, tarballPath); err != nil {
		return Archive{}, err
	}

	sum, err := sha256File(tarballPath)
	if err != nil {
		return Archive{}, err
	}

	manifest := Manifest{
		Revision:  input.Revision,
		Commit:    input.Commit,
		SHA256:    sum,
		CreatedAt: time.Now().UTC(),
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Archive{}, fmt.Errorf("policyrelease: encoding manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return Archive{}, fmt.Errorf("policyrelease: writing manifest: %w", err)
	}

	return Archive{
		Revision:     input.Revision,
		Commit:       input.Commit,
		TarballPath:  tarballPath,
		ManifestPath: manifestPath,
		SHA256:       sum,
	}, nil
}

// VerifyChecksum recomputes the tarball's SHA-256 and compares it against the
// checksum recorded when the Archive was built.
func VerifyChecksum(archive Archive) error {
	sum, err := sha256File(archive.TarballPath)
	if err != nil {
		return err
	}
	if sum != archive.SHA256 {
		return fmt.Errorf("policyrelease: checksum mismatch for %s: got %s, want %s",
			archive.TarballPath, sum, archive.SHA256)
	}
	return nil
}

func writeTarball(ctx context.Context, sourceDir, tarballPath string) error {
	out, err := os.Create(tarballPath)
	if err != nil {
		return fmt.Errorf("policyrelease: creating tarball: %w", err)
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return fmt.Errorf("policyrelease: writing tarball: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("policyrelease: closing tar writer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("policyrelease: closing gzip writer: %w", err)
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("policyrelease: opening %s for checksum: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("policyrelease: hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
