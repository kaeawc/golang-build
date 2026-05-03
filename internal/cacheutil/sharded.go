package cacheutil

import "path/filepath"

// ShardedEntryPath returns "{root}/{hash[:2]}/{hash[2:]}{ext}".
// ext must include the leading dot (".json", ".gob").
// Hashes shorter than 3 chars fall back to "{root}/_/{hash}{ext}".
func ShardedEntryPath(root, hash, ext string) string {
	if len(hash) < 3 {
		return filepath.Join(root, "_", hash+ext)
	}
	return filepath.Join(root, hash[:2], hash[2:]+ext)
}
