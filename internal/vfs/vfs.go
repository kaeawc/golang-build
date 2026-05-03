// Package vfs provides a small filesystem abstraction with an OS
// implementation and an in-memory Mem fake. Production code that
// takes an FS instead of calling os.* directly is testable without
// reaching for t.TempDir() boilerplate, and unit tests run faster
// because they never touch the disk.
//
// FS is deliberately small — just the operations Krit actually uses
// from os: read/write a file, stat, mkdir-all, remove, list a
// directory. Streaming reads/writes and file-handle operations are
// out of scope; callers that need them should keep using os directly
// (and may want a richer abstraction later).
package vfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FS is the minimal filesystem interface Krit code can take a
// dependency on. Implementations must be safe for concurrent use.
type FS interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Remove(path string) error
	ReadDir(path string) ([]os.DirEntry, error)
}

// OS is an FS backed by the real os package.
type OS struct{}

// ReadFile delegates to os.ReadFile.
func (OS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// WriteFile delegates to os.WriteFile.
func (OS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// Stat delegates to os.Stat.
func (OS) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

// MkdirAll delegates to os.MkdirAll.
func (OS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }

// Remove delegates to os.Remove.
func (OS) Remove(path string) error { return os.Remove(path) }

// ReadDir delegates to os.ReadDir.
func (OS) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }

// Default is a process-wide OS FS for callers that cannot reach a
// composition root. Prefer injecting an FS explicitly.
var Default FS = OS{}

// Mem is an in-memory FS suitable for tests. Paths are normalized
// with filepath.Clean. Directories are auto-created when WriteFile
// or MkdirAll touches them; leading separator behavior matches
// what os does on the host (paths are stored as-given after Clean).
//
// Mem is safe for concurrent use.
type Mem struct {
	mu    sync.RWMutex
	files map[string]*memEntry
	now   func() time.Time
}

type memEntry struct {
	data    []byte
	perm    os.FileMode
	dir     bool
	modTime time.Time
}

// NewMem returns an empty Mem.
func NewMem() *Mem {
	return &Mem{
		files: map[string]*memEntry{
			".": {dir: true, perm: 0o755, modTime: time.Time{}},
		},
		now: time.Now,
	}
}

// SetClock overrides the clock used for new entries' mod times.
// Useful for deterministic tests that assert on Stat().ModTime().
func (m *Mem) SetClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = now
}

func (m *Mem) clock() time.Time {
	if m.now == nil {
		return time.Time{}
	}
	return m.now()
}

// ReadFile returns the contents of path.
func (m *Mem) ReadFile(path string) ([]byte, error) {
	clean := filepath.Clean(path)
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.files[clean]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	if e.dir {
		return nil, &fs.PathError{Op: "read", Path: path, Err: errIsDirectory}
	}
	out := make([]byte, len(e.data))
	copy(out, e.data)
	return out, nil
}

// WriteFile stores data at path. The parent directory must already
// exist; this matches os.WriteFile so callers can swap implementations
// without behavioral surprises.
func (m *Mem) WriteFile(path string, data []byte, perm os.FileMode) error {
	clean := filepath.Clean(path)
	if clean == "." || clean == "/" {
		return &fs.PathError{Op: "write", Path: path, Err: errIsDirectory}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.files[clean]; ok && e.dir {
		return &fs.PathError{Op: "write", Path: path, Err: errIsDirectory}
	}
	parent := filepath.Dir(clean)
	if parent != "." && parent != "/" {
		if e, ok := m.files[parent]; !ok || !e.dir {
			return &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		}
	}
	stored := make([]byte, len(data))
	copy(stored, data)
	m.files[clean] = &memEntry{data: stored, perm: perm, modTime: m.clock()}
	return nil
}

// Stat returns metadata for path.
func (m *Mem) Stat(path string) (os.FileInfo, error) {
	clean := filepath.Clean(path)
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.files[clean]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	return memInfo{name: filepath.Base(clean), entry: e}, nil
}

// MkdirAll creates path and any missing parents.
func (m *Mem) MkdirAll(path string, perm os.FileMode) error {
	clean := filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mkdirAllLocked(clean, perm)
}

func (m *Mem) mkdirAllLocked(clean string, perm os.FileMode) error {
	if clean == "." || clean == "/" || clean == "" {
		return nil
	}
	if e, ok := m.files[clean]; ok {
		if e.dir {
			return nil
		}
		return &fs.PathError{Op: "mkdir", Path: clean, Err: errNotDirectory}
	}
	parent := filepath.Dir(clean)
	if err := m.mkdirAllLocked(parent, perm); err != nil {
		return err
	}
	m.files[clean] = &memEntry{dir: true, perm: perm, modTime: m.clock()}
	return nil
}

// Remove deletes the entry at path. Removing a non-empty directory
// returns an error matching the os package's behavior.
func (m *Mem) Remove(path string) error {
	clean := filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.files[clean]
	if !ok {
		return &fs.PathError{Op: "remove", Path: path, Err: fs.ErrNotExist}
	}
	if e.dir {
		// Refuse if any child entry exists.
		prefix := clean + string(filepath.Separator)
		for p := range m.files {
			if p != clean && strings.HasPrefix(p, prefix) {
				return &fs.PathError{Op: "remove", Path: path, Err: errDirNotEmpty}
			}
		}
	}
	delete(m.files, clean)
	return nil
}

// ReadDir lists the immediate children of path. Entries are sorted
// by name to match os.ReadDir's documented behavior.
func (m *Mem) ReadDir(path string) ([]os.DirEntry, error) {
	clean := filepath.Clean(path)
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.assertReadDirTarget(path, clean); err != nil {
		return nil, err
	}

	prefix := clean + string(filepath.Separator)
	if clean == "." {
		prefix = ""
	}
	seen := map[string]bool{}
	var entries []os.DirEntry
	for p, e := range m.files {
		if p == clean {
			continue
		}
		rel, ok := relativeChild(p, prefix)
		if !ok || seen[rel] {
			continue
		}
		seen[rel] = true
		entries = append(entries, memDirEntry{name: rel, entry: e})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// assertReadDirTarget checks that clean exists and is a directory, unless
// clean is "." (the implicit root).
func (m *Mem) assertReadDirTarget(path, clean string) error {
	if clean == "." {
		return nil
	}
	e, ok := m.files[clean]
	if !ok {
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	if !e.dir {
		return &fs.PathError{Op: "fdopendir", Path: path, Err: errNotDirectory}
	}
	return nil
}

// relativeChild returns the immediate-child name of p under prefix, or
// ("", false) if p is not under prefix.
func relativeChild(p, prefix string) (string, bool) {
	rel := p
	if prefix != "" {
		if !strings.HasPrefix(p, prefix) {
			return "", false
		}
		rel = p[len(prefix):]
	}
	if idx := strings.Index(rel, string(filepath.Separator)); idx >= 0 {
		rel = rel[:idx]
	}
	if rel == "" {
		return "", false
	}
	return rel, true
}

// memInfo implements os.FileInfo for Mem entries.
type memInfo struct {
	name  string
	entry *memEntry
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return int64(len(i.entry.data)) }
func (i memInfo) Mode() os.FileMode  { return i.entry.perm }
func (i memInfo) ModTime() time.Time { return i.entry.modTime }
func (i memInfo) IsDir() bool        { return i.entry.dir }
func (i memInfo) Sys() any           { return nil }

// memDirEntry implements os.DirEntry.
type memDirEntry struct {
	name  string
	entry *memEntry
}

func (d memDirEntry) Name() string { return d.name }
func (d memDirEntry) IsDir() bool  { return d.entry.dir }
func (d memDirEntry) Type() os.FileMode {
	if d.entry.dir {
		return os.ModeDir
	}
	return 0
}
func (d memDirEntry) Info() (os.FileInfo, error) {
	return memInfo(d), nil
}

// Sentinel errors for situations the os package surfaces with
// platform-specific syscall errors. We use simple sentinels so tests
// can match with errors.Is independently of platform.
var (
	errIsDirectory  = errors.New("is a directory")
	errNotDirectory = errors.New("not a directory")
	errDirNotEmpty  = errors.New("directory not empty")
)
