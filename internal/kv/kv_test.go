package kv

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

func TestSetGetDelete(t *testing.T) {
	m := NewMemory[int](Config{})
	defer m.Close()

	m.Set("a", 1)
	m.Set("b", 2)

	if v, ok := m.Get("a"); !ok || v != 1 {
		t.Errorf("Get(a) = (%d, %v), want (1, true)", v, ok)
	}
	if v, ok := m.Get("b"); !ok || v != 2 {
		t.Errorf("Get(b) = (%d, %v), want (2, true)", v, ok)
	}
	if _, ok := m.Get("missing"); ok {
		t.Errorf("Get(missing) = ok, want missing")
	}
	if got := m.Len(); got != 2 {
		t.Errorf("Len = %d, want 2", got)
	}
	m.Delete("a")
	if _, ok := m.Get("a"); ok {
		t.Errorf("Get after Delete: still present")
	}
	if got := m.Len(); got != 1 {
		t.Errorf("Len after Delete = %d, want 1", got)
	}
}

func TestTTLExpiration(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	m := NewMemory[string](Config{Clock: clk})
	defer m.Close()

	m.SetWithTTL("k", "v", 100*time.Millisecond)
	if v, ok := m.Get("k"); !ok || v != "v" {
		t.Errorf("Get before expiration = (%q, %v)", v, ok)
	}

	clk.Advance(99 * time.Millisecond)
	if _, ok := m.Get("k"); !ok {
		t.Error("Get just before TTL expiry should still hit")
	}

	clk.Advance(2 * time.Millisecond)
	if _, ok := m.Get("k"); ok {
		t.Error("Get after TTL expiry should miss")
	}
}

func TestSetWithTTLZeroIsNoExpire(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	m := NewMemory[int](Config{Clock: clk})
	defer m.Close()

	m.SetWithTTL("k", 1, 0)
	clk.Advance(time.Hour)
	if v, ok := m.Get("k"); !ok || v != 1 {
		t.Errorf("Get after large advance with zero TTL = (%d, %v)", v, ok)
	}
}

func TestLazyExpiryRemovesEntry(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	m := NewMemory[int](Config{Clock: clk})
	defer m.Close()

	m.SetWithTTL("k", 1, 10*time.Millisecond)
	clk.Advance(50 * time.Millisecond)

	// Trigger lazy expiry.
	if _, ok := m.Get("k"); ok {
		t.Fatal("Get on expired key returned true")
	}
	if got := m.Len(); got != 0 {
		t.Errorf("Len after lazy expiry = %d, want 0", got)
	}
}

func TestSweepRemovesExpired(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	m := NewMemory[int](Config{Clock: clk})
	defer m.Close()

	m.SetWithTTL("a", 1, 10*time.Millisecond)
	m.SetWithTTL("b", 2, 10*time.Second) // not expired
	m.Set("c", 3)                        // never expires

	clk.Advance(50 * time.Millisecond)
	m.Sweep()

	if _, ok := m.Get("a"); ok {
		t.Error("a should be swept")
	}
	if _, ok := m.Get("b"); !ok {
		t.Error("b should remain")
	}
	if _, ok := m.Get("c"); !ok {
		t.Error("c should remain")
	}
	if got := m.Len(); got != 2 {
		t.Errorf("Len after Sweep = %d, want 2", got)
	}
}

func TestGetOrSetCachesResult(t *testing.T) {
	m := NewMemory[int](Config{})
	defer m.Close()

	var calls atomic.Int32
	compute := func() (int, error) {
		calls.Add(1)
		return 42, nil
	}

	for i := 0; i < 5; i++ {
		v, err := m.GetOrSet(context.Background(), "k", 0, compute)
		if err != nil || v != 42 {
			t.Fatalf("GetOrSet = (%d, %v)", v, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("compute called %d times, want 1", got)
	}
}

func TestGetOrSetPropagatesError(t *testing.T) {
	m := NewMemory[int](Config{})
	defer m.Close()

	want := errors.New("boom")
	v, err := m.GetOrSet(context.Background(), "k", 0, func() (int, error) {
		return 0, want
	})
	if v != 0 || !errors.Is(err, want) {
		t.Errorf("GetOrSet = (%d, %v)", v, err)
	}
	if got := m.Len(); got != 0 {
		t.Errorf("Len after failed compute = %d, want 0 (must not cache)", got)
	}
}

func TestJanitorSweeps(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	m := NewMemory[int](Config{Clock: clk, JanitorInterval: 5 * time.Millisecond})
	defer m.Close()

	m.SetWithTTL("k", 1, 10*time.Millisecond)
	clk.Advance(50 * time.Millisecond)

	// Janitor uses real time.NewTicker; give it a real moment to fire.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.Len() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("janitor did not sweep within deadline; Len=%d", m.Len())
}

func TestCloseIsIdempotent(t *testing.T) {
	m := NewMemory[int](Config{JanitorInterval: 10 * time.Millisecond})
	m.Close()
	m.Close() // must not panic or hang
	// Operations after Close still work.
	m.Set("a", 1)
	if v, ok := m.Get("a"); !ok || v != 1 {
		t.Errorf("Get after Close = (%d, %v)", v, ok)
	}
}

func TestShardingHandlesManyKeys(t *testing.T) {
	m := NewMemory[int](Config{Shards: 8})
	defer m.Close()
	for i := 0; i < 1000; i++ {
		m.Set(string(rune('a'+(i%26))), i)
	}
	// Last write wins per key; expect 26 distinct keys.
	if got := m.Len(); got != 26 {
		t.Errorf("Len = %d, want 26", got)
	}
}

func TestShardCountRoundsToPow2(t *testing.T) {
	m := NewMemory[int](Config{Shards: 5})
	defer m.Close()
	if got := len(m.shards); got != 8 {
		t.Errorf("shards = %d, want 8 (next pow2 of 5)", got)
	}
}

func TestConcurrentSetGet(t *testing.T) {
	m := NewMemory[int](Config{})
	defer m.Close()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			m.Set(string(rune('a'+(i%26))), i)
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = m.Get(string(rune('a' + (i % 26))))
		}(i)
	}
	wg.Wait()
}

func TestSetReplacesExpiration(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	m := NewMemory[int](Config{Clock: clk})
	defer m.Close()

	m.SetWithTTL("k", 1, 10*time.Millisecond)
	m.Set("k", 2) // overwrite without TTL

	clk.Advance(time.Hour)
	if v, ok := m.Get("k"); !ok || v != 2 {
		t.Errorf("Get after Set overwrote TTL = (%d, %v)", v, ok)
	}
}
