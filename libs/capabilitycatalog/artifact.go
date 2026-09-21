package capabilitycatalog

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ArtifactFileName is the pre-decoded form of one module's capabilities,
// written beside the YAML it was derived from.
//
// ADR-013 measured why it exists: 100,000 capabilities cost 4.0 s and
// 838 MiB peak RSS to parse from YAML, against 203 ms and 90 MiB to decode
// from this. It is a build or sync output and is never committed - the YAML
// remains the authored source of truth, and this is only ever an
// optimisation over it.
const ArtifactFileName = "module.gob"

// BuildModuleArtifacts writes a pre-decoded artifact for every module
// directory under dir, derived from that module's YAML.
//
// It reads YAML directly rather than going through LoadDefinitionsForModule,
// so an artifact is never derived from an earlier artifact, and it captures
// the module's whole contents - generated and hand-authored alike - because
// that is what a reader of the artifact will be served instead of the
// directory.
func BuildModuleArtifacts(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		defs, err := loadModuleYAML(filepath.Join(dir, module))
		if err != nil {
			return err
		}
		if err := WriteModuleArtifact(dir, module, defs); err != nil {
			return err
		}
	}
	return nil
}

// moduleArtifact is the on-disk artifact: the decoded definitions plus a
// fingerprint of the YAML they came from.
type moduleArtifact struct {
	Fingerprint string
	Definitions []UiCapabilityDefinition
}

// WriteModuleArtifact writes defs as dir/module's pre-decoded artifact,
// stamped with a fingerprint of the module's current YAML.
func WriteModuleArtifact(dir, module string, defs []UiCapabilityDefinition) error {
	moduleDir := filepath.Join(dir, module)
	fingerprint, err := fingerprintModule(moduleDir)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(moduleArtifact{
		Fingerprint: fingerprint,
		Definitions: defs,
	}); err != nil {
		return fmt.Errorf("encoding the artifact for module %s: %w", module, err)
	}

	path := filepath.Join(moduleDir, ArtifactFileName)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// loadModuleArtifact returns the module's pre-decoded definitions, and
// whether they may be used.
//
// Every failure - absent, unreadable, corrupt, or derived from YAML that has
// since changed - returns false rather than an error, because the artifact
// is an optimisation and the YAML beside it is the source of truth. The one
// outcome that must never happen is serving content the adopter did not
// author, so a fingerprint mismatch is treated exactly like a missing file.
func loadModuleArtifact(moduleDir string) ([]UiCapabilityDefinition, bool) {
	raw, err := os.ReadFile(filepath.Join(moduleDir, ArtifactFileName))
	if err != nil {
		return nil, false
	}

	var artifact moduleArtifact
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&artifact); err != nil {
		return nil, false
	}

	current, err := fingerprintModule(moduleDir)
	if err != nil || current != artifact.Fingerprint {
		return nil, false
	}

	return artifact.Definitions, true
}

// fingerprintModule summarises the module's YAML files by name, size and
// modification time.
//
// Stat rather than content hash: hashing would have to read every byte of
// the YAML the artifact exists to avoid reading, which gives back a large
// part of what was bought. Size and modification time together catch the
// edits that actually occur here - a syncer writing new content, or a
// developer editing a file - and any miss degrades to serving the artifact,
// which is why the syncer writes the artifact after the YAML rather than
// relying on this alone.
func fingerprintModule(moduleDir string) (string, error) {
	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", moduleDir, err)
	}

	var parts []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", filepath.Join(moduleDir, entry.Name()), err)
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d",
			entry.Name(), info.Size(), info.ModTime().UnixNano()))
	}

	sort.Strings(parts)
	return strings.Join(parts, "|"), nil
}
