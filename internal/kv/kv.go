// Package kv provides an in-memory key-value Store with optional TTL.
//
// The interface is small on purpose: Get / Set / SetWithTTL / Delete / Len.
// The default Memory implementation is sharded by key hash to reduce mutex
// contention under load, expires entries lazily on Get and proactively via
// an optional janitor goroutine, and reads "now" from clock.Clock so its
// behavior is deterministic when wired with clock.Fake.
//
// This is the in-memory complement to internal/cacheutil (on-disk caches).
// For distributed caching, reach for Redis or Memcached directly.
package kv

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// Store is the key-value interface.
type Store[V any] interface {
	// Get returns the value and true if present and not expired.
	Get(key string) (V, bool)
	// Set stores key=value with no expiration.
	Set(key string, value V)
	// SetWithTTL stores key=value with a relative expiration. ttl <= 0
	// means no expiration (same as Set).
	SetWithTTL(key string, value V, ttl time.Duration)
	// Delete removes a key. No-op if absent.
	Delete(key string)
	// Len returns the number of entries currently in the store. Expired
	// entries that haven't been swept may be counted.
	Len() int
}

// Config controls Memory behavior. Zero value is usable.
type Config struct {
	// Shards is the number of internal map shards. Defaults to 16.
	// Higher reduces lock contention; lower reduces memory.
	Shards int
	// JanitorInterval, if > 0, starts a background goroutine that sweeps
	// expired entries on this cadence. Zero disables the janitor (entries
	// still expire lazily on Get).
	JanitorInterval time.Duration
	// Clock supplies "now". Defaults to clock.Default.
	Clock clock.Clock
}

// Memory is an in-memory Store with optional TTL. Safe for concurrent use.
type Memory[V any] struct {
	clk    clock.Clock
	shards []*shard[V]
	mask   uint32

	stopOnce sync.Once
	stop     chan struct{}
	stopped  chan struct{}
}

type shard[V any] struct {
	mu      sync.RWMutex
	entries map[string]entry[V]
}

type entry[V any] struct {
	value     V
	expiresAt time.Time // zero == no expiration
}

// NewMemory returns a Memory configured per cfg. Shards is rounded up to the
// next power of 2 (default 16). If cfg.JanitorInterval > 0, the janitor
// goroutine is started; call Close to stop it.
func NewMemory[V any](cfg Config) *Memory[V] {
	shards := cfg.Shards
	if shards <= 0 {
		shards = 16
	}
	shards = nextPow2(shards)

	clk := cfg.Clock
	if clk == nil {
		clk = clock.Default
	}

	m := &Memory[V]{
		clk:     clk,
		shards:  make([]*shard[V], shards),
		mask:    uint32(shards - 1), // #nosec G115 -- shards is rounded to a small pow2, fits in uint32
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	for i := range m.shards {
		m.shards[i] = &shard[V]{entries: map[string]entry[V]{}}
	}
	if cfg.JanitorInterval > 0 {
		go m.runJanitor(cfg.JanitorInterval)
	} else {
		close(m.stopped)
	}
	return m
}

// Close stops the janitor goroutine if one is running. Safe to call
// multiple times. After Close, Get/Set/Delete still work.
func (m *Memory[V]) Close() {
	m.stopOnce.Do(func() {
		close(m.stop)
	})
	<-m.stopped
}

// Get returns the value and true if present and unexpired.
func (m *Memory[V]) Get(key string) (V, bool) {
	s := m.shardFor(key)
	s.mu.RLock()
	e, ok := s.entries[key]
	s.mu.RUnlock()
	var zero V
	if !ok {
		return zero, false
	}
	if !e.expiresAt.IsZero() && !m.clk.Now().Before(e.expiresAt) {
		// Expired: drop it lazily so Len trends correct without the janitor.
		s.mu.Lock()
		// Re-check to avoid racing with a concurrent Set.
		if cur, ok := s.entries[key]; ok && cur.expiresAt.Equal(e.expiresAt) {
			delete(s.entries, key)
		}
		s.mu.Unlock()
		return zero, false
	}
	return e.value, true
}

// Set stores key=value with no expiration.
func (m *Memory[V]) Set(key string, value V) {
	s := m.shardFor(key)
	s.mu.Lock()
	s.entries[key] = entry[V]{value: value}
	s.mu.Unlock()
}

// SetWithTTL stores key=value with a relative expiration.
func (m *Memory[V]) SetWithTTL(key string, value V, ttl time.Duration) {
	if ttl <= 0 {
		m.Set(key, value)
		return
	}
	s := m.shardFor(key)
	s.mu.Lock()
	s.entries[key] = entry[V]{value: value, expiresAt: m.clk.Now().Add(ttl)}
	s.mu.Unlock()
}

// Delete removes a key.
func (m *Memory[V]) Delete(key string) {
	s := m.shardFor(key)
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
}

// Len returns the number of entries (including possibly-expired ones).
func (m *Memory[V]) Len() int {
	n := 0
	for _, s := range m.shards {
		s.mu.RLock()
		n += len(s.entries)
		s.mu.RUnlock()
	}
	return n
}

// Sweep removes every expired entry across all shards. Safe to call
// concurrently with Get/Set/Delete; useful in tests to advance state
// without waiting for the janitor.
func (m *Memory[V]) Sweep() {
	now := m.clk.Now()
	for _, s := range m.shards {
		s.mu.Lock()
		for k, e := range s.entries {
			if !e.expiresAt.IsZero() && !now.Before(e.expiresAt) {
				delete(s.entries, k)
			}
		}
		s.mu.Unlock()
	}
}

// GetOrSet returns the existing value for key if present, else stores and
// returns the result of compute. The compute callback runs without holding
// any shard lock, so other operations on different keys remain unblocked.
// Note: compute may run concurrently in different goroutines for the same
// key; the last write wins.
func (m *Memory[V]) GetOrSet(_ context.Context, key string, ttl time.Duration, compute func() (V, error)) (V, error) {
	if v, ok := m.Get(key); ok {
		return v, nil
	}
	v, err := compute()
	if err != nil {
		var zero V
		return zero, err
	}
	if ttl > 0 {
		m.SetWithTTL(key, v, ttl)
	} else {
		m.Set(key, v)
	}
	return v, nil
}

func (m *Memory[V]) shardFor(key string) *shard[V] {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return m.shards[h.Sum32()&m.mask]
}

func (m *Memory[V]) runJanitor(interval time.Duration) {
	defer close(m.stopped)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.Sweep()
		case <-m.stop:
			return
		}
	}
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
