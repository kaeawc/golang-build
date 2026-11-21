package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// testUserStore is the contract every UserStore must satisfy. It runs
// against MemoryStore always and against PgxStore when a database is
// available (see store_pgx_test.go), keeping the fake faithful.
func testUserStore(t *testing.T, s UserStore) {
	t.Helper()
	ctx := context.Background()

	var ids []int64
	for _, n := range []string{"a", "b", "c", "d"} {
		u, err := s.Create(ctx, n)
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		ids = append(ids, u.ID)
	}
	if _, err := s.Create(ctx, "  "); !errors.Is(err, ErrInvalidName) {
		t.Errorf("blank name: err = %v", err)
	}
	if _, err := s.Create(ctx, strings.Repeat("x", MaxNameLength+1)); !errors.Is(err, ErrInvalidName) {
		t.Errorf("long name: err = %v", err)
	}

	page, err := s.List(ctx, ids[0], 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != ids[1] || page[1].ID != ids[2] {
		t.Errorf("List(after first, 2) = %+v", page)
	}

	u, err := s.Get(ctx, ids[3])
	if err != nil || u.Name != "d" {
		t.Errorf("Get = %+v, %v", u, err)
	}
	if err := s.Delete(ctx, ids[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ids[3]); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get deleted: err = %v", err)
	}
	if err := s.Delete(ctx, ids[3]); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: err = %v", err)
	}
}

func TestMemoryStoreContract(t *testing.T) {
	t.Parallel()
	testUserStore(t, NewMemoryStore())
}

func TestMemoryStoreHonoursContext(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore("a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.List(ctx, 0, 10); !errors.Is(err, context.Canceled) {
		t.Errorf("List on cancelled ctx: %v", err)
	}
	if _, err := s.Get(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Get on cancelled ctx: %v", err)
	}
}
