package cacheutil_test

import (
	"path/filepath"
	"testing"

	"github.com/kaeawc/golang-build/internal/cacheutil"
)

func TestShardedEntryPath_GoldValue(t *testing.T) {
	root := "/cache"
	hash := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	ext := ".json"
	want := filepath.Join(root, "ba", "7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad.json")
	got := cacheutil.ShardedEntryPath(root, hash, ext)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShardedEntryPath_ShortHash(t *testing.T) {
	root := "/cache"
	hash := "ab" // len 2 < 3
	ext := ".json"
	want := filepath.Join(root, "_", "ab.json")
	got := cacheutil.ShardedEntryPath(root, hash, ext)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShardedEntryPath_ExactlyThreeChars(t *testing.T) {
	root := "/cache"
	hash := "abc" // len == 3: shard "ab", rest "c"
	ext := ".ext"
	want := filepath.Join(root, "ab", "c.ext")
	got := cacheutil.ShardedEntryPath(root, hash, ext)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShardedEntryPath_EmptyHash(t *testing.T) {
	root := "/cache"
	hash := ""
	ext := ".ext"
	want := filepath.Join(root, "_", ".ext")
	got := cacheutil.ShardedEntryPath(root, hash, ext)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestShardedEntryPath_ParityWithOracle verifies that ShardedEntryPath produces
// the same result as the oracle package's internal entryPath function.
// oracle.entryPath(cacheDir, hash) returns:
//
//	filepath.Join(cacheDir, "entries", "_", hash+".json")      if len(hash) < 3
//	filepath.Join(cacheDir, "entries", hash[:2], hash[2:]+".json") otherwise
//
// ShardedEntryPath(filepath.Join(cacheDir, "entries"), hash, ".json") must match.
func TestShardedEntryPath_ParityWithOracle(t *testing.T) {
	cacheDir := "/repo/.krit/types-cache"
	hash := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

	// Manually reproduce oracle.entryPath logic
	var oraclePath string
	if len(hash) < 3 {
		oraclePath = filepath.Join(cacheDir, "entries", "_", hash+".json")
	} else {
		oraclePath = filepath.Join(cacheDir, "entries", hash[:2], hash[2:]+".json")
	}

	got := cacheutil.ShardedEntryPath(filepath.Join(cacheDir, "entries"), hash, ".json")
	if got != oraclePath {
		t.Errorf("parity fail: ShardedEntryPath=%q, oracle=%q", got, oraclePath)
	}

	// Also check short-hash parity
	shortHash := "a"
	if len(shortHash) < 3 {
		oraclePath = filepath.Join(cacheDir, "entries", "_", shortHash+".json")
	} else {
		oraclePath = filepath.Join(cacheDir, "entries", shortHash[:2], shortHash[2:]+".json")
	}
	got = cacheutil.ShardedEntryPath(filepath.Join(cacheDir, "entries"), shortHash, ".json")
	if got != oraclePath {
		t.Errorf("short-hash parity fail: ShardedEntryPath=%q, oracle=%q", got, oraclePath)
	}
}
