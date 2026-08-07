package policyrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store retains built archives and their manifests on disk and tracks which
// one is currently active. "Several previous archives and manifests
// retained" (issue #21) so a rollback can activate the one before the
// current release without re-fetching or re-validating anything.
type Store struct {
	dir string
}

// NewStore opens a Store rooted at dir, the same OutputDir archives are
// built into.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

const activeMarkerFile = "active.json"

// Archives lists every retained archive, oldest first, by reading the
// manifest files BuildArchive writes next to each tarball.
func (s *Store) Archives() ([]Archive, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("policyrelease: reading store directory: %w", err)
	}

	var archives []Archive
	var manifests []Manifest
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".manifest.json") {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("policyrelease: reading manifest %s: %w", e.Name(), err)
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("policyrelease: parsing manifest %s: %w", e.Name(), err)
		}
		base := strings.TrimSuffix(e.Name(), ".manifest.json")
		archives = append(archives, Archive{
			Revision:     m.Revision,
			Commit:       m.Commit,
			TarballPath:  filepath.Join(s.dir, base+".tar.gz"),
			ManifestPath: path,
			SHA256:       m.SHA256,
		})
		manifests = append(manifests, m)
	}

	sort.SliceStable(archives, func(i, j int) bool {
		return manifests[i].CreatedAt.Before(manifests[j].CreatedAt)
	})
	return archives, nil
}

// Prune deletes every retained archive and manifest except the keep newest,
// leaving the currently active one untouched even if it would otherwise be
// pruned.
func (s *Store) Prune(keep int) error {
	archives, err := s.Archives()
	if err != nil {
		return err
	}
	if len(archives) <= keep {
		return nil
	}
	for _, archive := range archives[:len(archives)-keep] {
		if err := os.Remove(archive.TarballPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("policyrelease: pruning %s: %w", archive.TarballPath, err)
		}
		if err := os.Remove(archive.ManifestPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("policyrelease: pruning %s: %w", archive.ManifestPath, err)
		}
	}
	return nil
}

type activeMarker struct {
	Revision string `json:"revision"`
}

// MarkActive records archive as the currently active revision. It is called
// only after InstallAndActivate reports every replica confirmed.
func (s *Store) MarkActive(archive Archive) error {
	data, err := json.Marshal(activeMarker{Revision: archive.Revision})
	if err != nil {
		return fmt.Errorf("policyrelease: encoding active marker: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, activeMarkerFile), data, 0o644); err != nil {
		return fmt.Errorf("policyrelease: writing active marker: %w", err)
	}
	return nil
}

// Active returns the archive currently recorded as active.
func (s *Store) Active() (Archive, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, activeMarkerFile))
	if err != nil {
		return Archive{}, fmt.Errorf("policyrelease: no active revision recorded: %w", err)
	}
	var marker activeMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return Archive{}, fmt.Errorf("policyrelease: parsing active marker: %w", err)
	}
	return s.findByRevision(marker.Revision)
}

// Previous returns the archive retained immediately before the currently
// active one, by build order. It is the archive a rollback activates
// (§13.3).
func (s *Store) Previous() (Archive, error) {
	active, err := s.Active()
	if err != nil {
		return Archive{}, err
	}
	archives, err := s.Archives()
	if err != nil {
		return Archive{}, err
	}
	for i, a := range archives {
		if a.Revision == active.Revision {
			if i == 0 {
				return Archive{}, errors.New("policyrelease: no archive retained before the active revision")
			}
			return archives[i-1], nil
		}
	}
	return Archive{}, fmt.Errorf("policyrelease: active revision %s is no longer retained", active.Revision)
}

func (s *Store) findByRevision(revision string) (Archive, error) {
	archives, err := s.Archives()
	if err != nil {
		return Archive{}, err
	}
	for _, a := range archives {
		if a.Revision == revision {
			return a, nil
		}
	}
	return Archive{}, fmt.Errorf("policyrelease: revision %s is not retained", revision)
}

// Rollback activates the archive retained immediately before the store's
// current active revision (§13.3). It never touches assignment data: this
// package has no dependency on assignmentstore or the Postgres DSN, and
// nothing here writes anything but policy files and its own manifests.
func Rollback(ctx context.Context, store *Store, replicas []Replica, reloader Reloader) (ActivationResult, error) {
	previous, err := store.Previous()
	if err != nil {
		return ActivationResult{}, fmt.Errorf("policyrelease: rollback: %w", err)
	}

	result, err := InstallAndActivate(ctx, previous, replicas, reloader)
	if err != nil {
		return result, fmt.Errorf("policyrelease: rollback: %w", err)
	}

	if err := store.MarkActive(previous); err != nil {
		return result, fmt.Errorf("policyrelease: rollback: %w", err)
	}
	return result, nil
}
