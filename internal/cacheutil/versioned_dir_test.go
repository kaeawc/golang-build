package cacheutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kaeawc/golang-build/internal/cacheutil"
)

func TestVersionedDir_FirstRun(t *testing.T) {
	root := t.TempDir()
	vd := cacheutil.VersionedDir{
		Root: root,
		Tokens: []cacheutil.SchemaToken{
			{Name: "version", Value: "1"},
			{Name: "grammar-version", Value: "abc"},
		},
	}

	entriesDir, err := vd.Open()
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}

	// Entries dir must exist
	if fi, err := os.Stat(entriesDir); err != nil || !fi.IsDir() {
		t.Fatalf("entries dir not created: %v", err)
	}

	// Sidecars must have been written with correct content
	for _, tok := range vd.Tokens {
		data, err := os.ReadFile(filepath.Join(root, tok.Name))
		if err != nil {
			t.Fatalf("sidecar %s missing: %v", tok.Name, err)
		}
		if string(data) != tok.Value {
			t.Fatalf("sidecar %s = %q, want %q", tok.Name, data, tok.Value)
		}
	}
}

func TestVersionedDir_SameTokensPreservesEntries(t *testing.T) {
	root := t.TempDir()
	vd := cacheutil.VersionedDir{
		Root: root,
		Tokens: []cacheutil.SchemaToken{
			{Name: "version", Value: "1"},
		},
	}

	// First run to create sidecars
	entriesDir, err := vd.Open()
	if err != nil {
		t.Fatalf("first Open(): %v", err)
	}

	// Place a sentinel file in entries
	sentinel := filepath.Join(entriesDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep-me"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Second run with same tokens
	_, err = vd.Open()
	if err != nil {
		t.Fatalf("second Open(): %v", err)
	}

	// Sentinel must still be present
	if _, err := os.Stat(sentinel); os.IsNotExist(err) {
		t.Fatal("entries dir was nuked on same-token run")
	}
}

func TestVersionedDir_TokenChangeCausesNuke(t *testing.T) {
	root := t.TempDir()
	vd := cacheutil.VersionedDir{
		Root: root,
		Tokens: []cacheutil.SchemaToken{
			{Name: "version", Value: "1"},
		},
	}

	entriesDir, err := vd.Open()
	if err != nil {
		t.Fatalf("first Open(): %v", err)
	}

	// Place a sentinel file in entries
	sentinel := filepath.Join(entriesDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("should-be-nuked"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Second run with different token value
	vd2 := cacheutil.VersionedDir{
		Root: root,
		Tokens: []cacheutil.SchemaToken{
			{Name: "version", Value: "2"},
		},
	}
	_, err = vd2.Open()
	if err != nil {
		t.Fatalf("second Open(): %v", err)
	}

	// Sentinel must be gone
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("sentinel still exists after token change (entries not nuked)")
	}

	// Sidecar must have new value
	data, err := os.ReadFile(filepath.Join(root, "version"))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if string(data) != "2" {
		t.Fatalf("sidecar = %q, want %q", data, "2")
	}
}

func TestVersionedDir_TwoTokensChangeOne(t *testing.T) {
	root := t.TempDir()
	vd := cacheutil.VersionedDir{
		Root: root,
		Tokens: []cacheutil.SchemaToken{
			{Name: "version", Value: "1"},
			{Name: "grammar-version", Value: "old"},
		},
	}

	entriesDir, err := vd.Open()
	if err != nil {
		t.Fatalf("first Open(): %v", err)
	}

	sentinel := filepath.Join(entriesDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("bye"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Change only the second token
	vd2 := cacheutil.VersionedDir{
		Root: root,
		Tokens: []cacheutil.SchemaToken{
			{Name: "version", Value: "1"},
			{Name: "grammar-version", Value: "new"},
		},
	}
	_, err = vd2.Open()
	if err != nil {
		t.Fatalf("second Open(): %v", err)
	}

	// Nuke must have happened
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("sentinel still exists; nuke did not fire")
	}

	// Both sidecars should reflect current values
	for _, tok := range vd2.Tokens {
		data, err := os.ReadFile(filepath.Join(root, tok.Name))
		if err != nil {
			t.Fatalf("sidecar %s missing: %v", tok.Name, err)
		}
		if string(data) != tok.Value {
			t.Fatalf("sidecar %s = %q, want %q", tok.Name, data, tok.Value)
		}
	}
}

func TestVersionedDir_Clear(t *testing.T) {
	root := t.TempDir()
	vd := cacheutil.VersionedDir{
		Root: root,
		Tokens: []cacheutil.SchemaToken{
			{Name: "version", Value: "1"},
		},
	}

	if _, err := vd.Open(); err != nil {
		t.Fatalf("Open(): %v", err)
	}

	// Clear must remove root
	if err := vd.Clear(); err != nil {
		t.Fatalf("Clear(): %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("root still exists after Clear()")
	}

	// Second Clear must be idempotent (no error on missing dir)
	if err := vd.Clear(); err != nil {
		t.Fatalf("second Clear(): %v", err)
	}
}
