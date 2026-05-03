package cacheutil

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kaeawc/golang-build/internal/fsutil"
)

// VersionedDir is a cache directory whose contents are invalidated when any of
// the declared schema tokens change.
type VersionedDir struct {
	Root       string        // absolute path to the cache root
	EntriesDir string        // subdir under Root whose contents are nuked on mismatch (default: "entries")
	ExtraDirs  []string      // additional subdirs under Root whose contents are nuked on mismatch
	ExtraFiles []string      // additional files under Root whose contents are nuked on mismatch
	Tokens     []SchemaToken // written to {Root}/{Name} sidecar files
}

// SchemaToken is one named-version dimension.
type SchemaToken struct {
	Name  string // filename under Root, e.g. "version" or "grammar-version"
	Value string
}

// Open ensures the directory tree exists, checks every token against its
// sidecar, and removes-and-recreates EntriesDir if any mismatch is found.
// Missing sidecars on first run are written without nuking (fresh repo).
// Returns the absolute path to EntriesDir.
func (v VersionedDir) Open() (entriesDir string, err error) {
	entriesSub := v.EntriesDir
	if entriesSub == "" {
		entriesSub = "entries"
	}
	entriesPath := filepath.Join(v.Root, entriesSub)

	if err := os.MkdirAll(entriesPath, 0o750); err != nil {
		return "", fmt.Errorf("cacheutil: mkdir entries: %w", err)
	}

	nuke, err := v.tokensMismatched()
	if err != nil {
		return "", err
	}
	if nuke {
		if err := v.nukeEntries(entriesPath); err != nil {
			return "", err
		}
	}

	// Write sidecars after the nuke+mkdir so readers that see the new sidecar
	// can trust the entries subtree matches.
	for _, token := range v.Tokens {
		sidecar := filepath.Join(v.Root, token.Name)
		if err := fsutil.WriteFileAtomic(sidecar, []byte(token.Value), 0o644); err != nil {
			return "", fmt.Errorf("cacheutil: write sidecar %s: %w", token.Name, err)
		}
	}

	return entriesPath, nil
}

// tokensMismatched returns true if any present sidecar differs from its token.
// Missing sidecars (first run) do not count as a mismatch.
func (v VersionedDir) tokensMismatched() (bool, error) {
	for _, token := range v.Tokens {
		sidecar := filepath.Join(v.Root, token.Name)
		data, err := os.ReadFile(sidecar)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, fmt.Errorf("cacheutil: read sidecar %s: %w", token.Name, err)
		}
		if string(data) != token.Value {
			return true, nil
		}
	}
	return false, nil
}

func (v VersionedDir) nukeEntries(entriesPath string) error {
	if err := os.RemoveAll(entriesPath); err != nil {
		return fmt.Errorf("cacheutil: remove entries: %w", err)
	}
	if err := os.MkdirAll(entriesPath, 0o750); err != nil {
		return fmt.Errorf("cacheutil: mkdir entries after nuke: %w", err)
	}
	for _, extra := range v.ExtraDirs {
		if extra == "" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(v.Root, extra)); err != nil {
			return fmt.Errorf("cacheutil: remove extra dir %s: %w", extra, err)
		}
	}
	for _, extra := range v.ExtraFiles {
		if extra == "" {
			continue
		}
		if err := os.Remove(filepath.Join(v.Root, extra)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cacheutil: remove extra file %s: %w", extra, err)
		}
	}
	return nil
}

// Clear removes the entire cache root. Safe to call when the dir is missing.
func (v VersionedDir) Clear() error {
	if err := os.RemoveAll(v.Root); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cacheutil: clear %s: %w", v.Root, err)
	}
	return nil
}
