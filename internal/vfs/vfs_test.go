package vfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// runFSContract runs the same set of behavioral assertions against
// any FS implementation. Both OS and Mem must satisfy them so tests
// substituting Mem for OS don't get a different filesystem.
func runFSContract(t *testing.T, name string, factory func(t *testing.T) (FS, string)) {
	t.Helper()

	t.Run(name+"/WriteFile then ReadFile roundtrips", func(t *testing.T) {
		fsys, root := factory(t)
		path := filepath.Join(root, "hello.txt")
		want := []byte("hello world")
		if err := fsys.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, err := fsys.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run(name+"/ReadFile missing returns ErrNotExist", func(t *testing.T) {
		fsys, root := factory(t)
		_, err := fsys.ReadFile(filepath.Join(root, "missing.txt"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run(name+"/Stat missing returns ErrNotExist", func(t *testing.T) {
		fsys, root := factory(t)
		_, err := fsys.Stat(filepath.Join(root, "missing"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run(name+"/MkdirAll creates intermediate directories", func(t *testing.T) {
		fsys, root := factory(t)
		dir := filepath.Join(root, "a", "b", "c")
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		info, err := fsys.Stat(dir)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("IsDir = false, want true")
		}
	})

	t.Run(name+"/MkdirAll on existing directory is idempotent", func(t *testing.T) {
		fsys, root := factory(t)
		dir := filepath.Join(root, "x")
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("second MkdirAll: %v", err)
		}
	})

	t.Run(name+"/WriteFile requires parent dir to exist", func(t *testing.T) {
		fsys, root := factory(t)
		path := filepath.Join(root, "nested", "deep", "f.txt")
		err := fsys.WriteFile(path, []byte("ok"), 0o644)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("WriteFile without parent err = %v, want fs.ErrNotExist", err)
		}
		// After MkdirAll the same write succeeds.
		if err := fsys.MkdirAll(filepath.Join(root, "nested", "deep"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := fsys.WriteFile(path, []byte("ok"), 0o644); err != nil {
			t.Fatalf("WriteFile after MkdirAll: %v", err)
		}
	})

	t.Run(name+"/WriteFile overwrites existing file", func(t *testing.T) {
		fsys, root := factory(t)
		path := filepath.Join(root, "f.txt")
		if err := fsys.WriteFile(path, []byte("first"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := fsys.WriteFile(path, []byte("second"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _ := fsys.ReadFile(path)
		if string(got) != "second" {
			t.Errorf("got %q, want second", got)
		}
	})

	t.Run(name+"/Remove deletes a file", func(t *testing.T) {
		fsys, root := factory(t)
		path := filepath.Join(root, "f.txt")
		_ = fsys.WriteFile(path, []byte("x"), 0o644)
		if err := fsys.Remove(path); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := fsys.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after Remove, Stat err = %v, want ErrNotExist", err)
		}
	})

	t.Run(name+"/Remove missing returns ErrNotExist", func(t *testing.T) {
		fsys, root := factory(t)
		err := fsys.Remove(filepath.Join(root, "ghost"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run(name+"/ReadDir lists immediate children sorted", func(t *testing.T) {
		fsys, root := factory(t)
		_ = fsys.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644)
		_ = fsys.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
		_ = fsys.MkdirAll(filepath.Join(root, "sub", "deeper"), 0o755)
		entries, err := fsys.ReadDir(root)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		want := []string{"a.txt", "b.txt", "sub"}
		if len(names) != len(want) {
			t.Fatalf("ReadDir names = %v, want %v", names, want)
		}
		for i, w := range want {
			if names[i] != w {
				t.Errorf("ReadDir[%d] = %q, want %q (full %v)", i, names[i], w, names)
			}
		}
		for _, e := range entries {
			if e.Name() == "sub" && !e.IsDir() {
				t.Error("sub IsDir = false, want true")
			}
			if e.Name() == "a.txt" && e.IsDir() {
				t.Error("a.txt IsDir = true, want false")
			}
		}
	})
}

func TestOS_SatisfiesFSContract(t *testing.T) {
	runFSContract(t, "OS", func(t *testing.T) (FS, string) {
		return OS{}, t.TempDir()
	})
}

func TestMem_SatisfiesFSContract(t *testing.T) {
	runFSContract(t, "Mem", func(t *testing.T) (FS, string) {
		m := NewMem()
		if err := m.MkdirAll("/root", 0o755); err != nil {
			t.Fatal(err)
		}
		return m, "/root"
	})
}

func TestDefault_IsOS(t *testing.T) {
	t.Parallel()
	if _, ok := Default.(OS); !ok {
		t.Fatalf("Default = %T, want OS", Default)
	}
}

func TestMem_StatReturnsModeAndSize(t *testing.T) {
	t.Parallel()
	m := NewMem()
	if err := m.MkdirAll("/a", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/a/b.txt", []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := m.Stat("/a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 5 {
		t.Errorf("Size = %d, want 5", info.Size())
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("Perm = %v, want 0o600", info.Mode().Perm())
	}
	if info.IsDir() {
		t.Error("IsDir = true, want false")
	}
	if info.Name() != "b.txt" {
		t.Errorf("Name = %q, want b.txt", info.Name())
	}
}

func TestMem_ReadDirOnNonexistent(t *testing.T) {
	t.Parallel()
	m := NewMem()
	_, err := m.ReadDir("/nope")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func TestMem_ReadFileOnDirectoryFails(t *testing.T) {
	t.Parallel()
	m := NewMem()
	if err := m.MkdirAll("/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadFile("/dir"); err == nil {
		t.Fatal("expected error reading a directory, got nil")
	}
}

func TestMem_WriteFileOverDirectoryFails(t *testing.T) {
	t.Parallel()
	m := NewMem()
	if err := m.MkdirAll("/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/dir", []byte("x"), 0o644); err == nil {
		t.Fatal("expected error writing a file at a directory path, got nil")
	}
}

func TestMem_RemoveNonEmptyDirectoryFails(t *testing.T) {
	t.Parallel()
	m := NewMem()
	if err := m.MkdirAll("/a", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/a/b.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("/a"); err == nil {
		t.Fatal("expected error removing non-empty directory, got nil")
	}
}

func TestMem_DeterministicClock(t *testing.T) {
	t.Parallel()
	m := NewMem()
	fixed := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return fixed })

	if err := m.WriteFile("/f.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := m.Stat("/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(fixed) {
		t.Errorf("ModTime = %v, want %v", info.ModTime(), fixed)
	}
}

func TestMem_ConcurrentReadsAndWrites(t *testing.T) {
	t.Parallel()
	m := NewMem()
	if err := m.MkdirAll("/data", 0o755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	const writers = 8
	const iterations = 100
	wg.Add(writers * 2)
	for i := 0; i < writers; i++ {
		idx := i
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				path := filepath.Join("/data", "f"+itoa(idx)+".txt")
				_ = m.WriteFile(path, []byte("x"), 0o644)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _ = m.ReadDir("/data")
			}
		}()
	}
	wg.Wait()
}

func TestMem_ReadFileReturnsCopy(t *testing.T) {
	t.Parallel()
	m := NewMem()
	if err := m.WriteFile("/f.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	got1, _ := m.ReadFile("/f.txt")
	got1[0] = 'X'

	got2, _ := m.ReadFile("/f.txt")
	if string(got2) != "hello" {
		t.Fatalf("ReadFile returned shared buffer; mutation leaked: got %q", got2)
	}
}

func TestOS_ReadDirOnFilePath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (OS{}).ReadDir(path); err == nil {
		t.Fatal("expected error reading a file as a directory, got nil")
	}
}

// Compile-time assertions.
var (
	_ FS = OS{}
	_ FS = (*Mem)(nil)
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
