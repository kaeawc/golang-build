package eventbus

import (
	"strconv"
	"sync/atomic"
	"testing"
)

type benchEvent struct {
	ID   int64
	Kind string
}

var subscriberCounts = []int{1, 8, 64}

// BenchmarkSyncPublish measures synchronous fan-out: the publisher pays for
// every handler, so ns/op should grow roughly linearly with subscribers.
func BenchmarkSyncPublish(b *testing.B) {
	for _, n := range subscriberCounts {
		b.Run("subs="+strconv.Itoa(n), func(b *testing.B) {
			bus := NewSync[benchEvent]()
			var delivered atomic.Int64
			for range n {
				bus.Subscribe(func(benchEvent) { delivered.Add(1) })
			}
			e := benchEvent{ID: 1, Kind: "user.created"}
			b.ReportAllocs()
			for b.Loop() {
				bus.Publish(e)
			}
			if got, want := delivered.Load(), int64(b.N*n); got != want {
				b.Fatalf("delivered %d events, want %d", got, want)
			}
		})
	}
}

// BenchmarkAsyncPublish measures enqueue cost into per-subscriber queues.
// In blocking mode a full queue applies back-pressure, so this tracks
// end-to-end dispatch throughput; in drop mode it is the pure publish cost.
func BenchmarkAsyncPublish(b *testing.B) {
	for _, mode := range []struct {
		name string
		cfg  AsyncConfig
	}{
		{"blocking", AsyncConfig{BufferSize: 1024}},
		{"drop", AsyncConfig{BufferSize: 1024, DropWhenFull: true}},
	} {
		for _, n := range subscriberCounts {
			b.Run(mode.name+"/subs="+strconv.Itoa(n), func(b *testing.B) {
				bus := NewAsync[benchEvent](mode.cfg)
				defer bus.Close()
				for range n {
					bus.Subscribe(func(benchEvent) {})
				}
				e := benchEvent{ID: 1, Kind: "user.created"}
				b.ReportAllocs()
				for b.Loop() {
					bus.Publish(e)
				}
			})
		}
	}
}
