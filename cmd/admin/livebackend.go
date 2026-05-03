package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kaeawc/golang-build/internal/cache"
	"github.com/kaeawc/golang-build/internal/db"
)

// LiveBackend talks to a real Postgres pool and Valkey cache. Built
// only when DATABASE_URL is set; falls back to FakeBackend otherwise.
type LiveBackend struct {
	pool  *pgxpool.Pool
	cache cache.Cache
}

// NewLiveBackend builds a LiveBackend from env vars. Returns nil
// (without error) if DATABASE_URL is not set, signalling the caller to
// fall back to the fake.
func NewLiveBackend(ctx context.Context) (*LiveBackend, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	lb := &LiveBackend{pool: pool}
	if addr := os.Getenv("VALKEY_ADDR"); addr != "" {
		c, err := cache.New(addr)
		if err != nil {
			pool.Close()
			return nil, err
		}
		lb.cache = c
	}
	return lb, nil
}

// Close releases the pool + cache.
func (l *LiveBackend) Close() {
	if l.pool != nil {
		l.pool.Close()
	}
	if l.cache != nil {
		l.cache.Close()
	}
}

func (l *LiveBackend) ListUsers(ctx context.Context) ([]User, error) {
	q := db.New(l.pool)
	rows, err := q.GetUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = User{ID: r.ID, Name: r.Name}
	}
	return out, nil
}

func (l *LiveBackend) RunHealthchecks(ctx context.Context) ([]Probe, error) {
	probes := []Probe{l.probeDB(ctx)}
	if l.cache != nil {
		probes = append(probes, l.probeCache(ctx))
	}
	return probes, nil
}

func (l *LiveBackend) probeDB(ctx context.Context) Probe {
	start := time.Now()
	err := l.pool.Ping(ctx)
	dur := time.Since(start)
	if err != nil {
		return Probe{Name: "db", Healthy: false, Latency: dur, Err: err.Error()}
	}
	return Probe{Name: "db", Healthy: true, Latency: dur}
}

func (l *LiveBackend) probeCache(ctx context.Context) Probe {
	// valkey-go returns "valkey nil" for missing keys; we treat that as
	// a successful round-trip. Any other error means the cache is sick.
	start := time.Now()
	_, err := l.cache.Get(ctx, "_admin_probe")
	dur := time.Since(start)
	if err != nil && !isMissingKey(err) {
		return Probe{Name: "valkey", Healthy: false, Latency: dur, Err: err.Error()}
	}
	return Probe{Name: "valkey", Healthy: true, Latency: dur}
}

func isMissingKey(err error) bool {
	if err == nil {
		return false
	}
	// valkey-go's "nil reply" is conveyed via an error implementing
	// IsValkeyNil(); fall back to string match for portability.
	type nilErr interface{ IsValkeyNil() bool }
	var ne nilErr
	if errors.As(err, &ne) && ne.IsValkeyNil() {
		return true
	}
	return strings.Contains(err.Error(), "valkey nil")
}
