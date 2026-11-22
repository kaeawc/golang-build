package ratelimit

import (
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// BenchmarkBucketAllow covers the two outcomes a hot path sees: a bucket
// with headroom (refill + consume) and an exhausted one (refill + reject).
// Both run against the real clock, since clock.Now is part of the cost.
func BenchmarkBucketAllow(b *testing.B) {
	b.Run("allowed", func(b *testing.B) {
		bucket := New(clock.System{}, 1e12, 1<<30)
		b.ReportAllocs()
		for b.Loop() {
			if !bucket.Allow() {
				b.Fatal("bucket with effectively unlimited rate denied a token")
			}
		}
	})
	b.Run("denied", func(b *testing.B) {
		bucket := New(clock.System{}, 1e-9, 1)
		bucket.Allow()
		b.ReportAllocs()
		for b.Loop() {
			if bucket.Allow() {
				b.Fatal("exhausted bucket granted a token")
			}
		}
	})
	b.Run("fake-clock", func(b *testing.B) {
		// Isolates the bucket's own arithmetic and locking from time.Now.
		bucket := New(clock.NewFake(time.Unix(0, 0)), 1, 1<<30)
		b.ReportAllocs()
		for b.Loop() {
			bucket.Allow()
		}
	})
}

// BenchmarkBucketAllowN shows the cost is independent of the request size.
func BenchmarkBucketAllowN(b *testing.B) {
	for _, bc := range []struct {
		name string
		n    int
	}{{"n=1", 1}, {"n=16", 16}, {"n=1024", 1024}} {
		b.Run(bc.name, func(b *testing.B) {
			bucket := New(clock.System{}, 1e15, 1<<30)
			b.ReportAllocs()
			for b.Loop() {
				bucket.AllowN(bc.n)
			}
		})
	}
}

// BenchmarkBucketAllowParallel measures lock contention when many
// goroutines share one bucket. b.Loop has no parallel form, so this uses
// RunParallel, the idiom for contention benchmarks.
func BenchmarkBucketAllowParallel(b *testing.B) {
	bucket := New(clock.System{}, 1e12, 1<<30)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bucket.Allow()
		}
	})
}

// BenchmarkManualAllow is the test double's cost, for comparison: a bare
// mutex and integer compare.
func BenchmarkManualAllow(b *testing.B) {
	m := NewManual(1 << 62)
	b.ReportAllocs()
	for b.Loop() {
		m.Allow()
	}
}
