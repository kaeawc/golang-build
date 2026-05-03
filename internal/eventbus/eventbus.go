// Package eventbus provides a generic in-process publish/subscribe primitive.
//
// Use it to decouple emit-side code from listener-side code without dragging
// in a broker. Publishers don't know who listens; listeners don't know who
// publishes. Sync delivers synchronously in the publisher's goroutine; Async
// fans events out to per-subscriber buffered channels processed by worker
// goroutines so a slow listener doesn't block the publisher.
//
//	type UserCreated struct{ ID string }
//	bus := eventbus.NewSync[UserCreated]()
//	off := bus.Subscribe(func(e UserCreated) { /* react */ })
//	defer off()
//	bus.Publish(UserCreated{ID: "u_1"})
//
// For tests: Sync is already deterministic. To assert listener invocation,
// just use a closure capture.
package eventbus

import (
	"sync"
	"sync/atomic"
)

// Bus is the publish/subscribe interface.
type Bus[E any] interface {
	// Subscribe registers a handler. The returned Unsubscribe removes it.
	// Subscribing during Publish is safe but the new handler will not see
	// the in-flight event.
	Subscribe(handler func(E)) Unsubscribe
	// Publish delivers e to every current subscriber.
	Publish(e E)
	// Subscribers reports the current handler count.
	Subscribers() int
}

// Unsubscribe removes a previously-registered handler. Idempotent.
type Unsubscribe func()

// Sync delivers events synchronously in the publisher's goroutine. Handler
// errors and panics propagate; recover or wrap in the handler if needed.
type Sync[E any] struct {
	mu       sync.RWMutex
	nextID   uint64
	handlers map[uint64]func(E)
}

// NewSync returns a Sync bus.
func NewSync[E any]() *Sync[E] {
	return &Sync[E]{handlers: map[uint64]func(E){}}
}

// Subscribe registers handler.
func (b *Sync[E]) Subscribe(handler func(E)) Unsubscribe {
	if handler == nil {
		return func() {}
	}
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.handlers[id] = handler
	b.mu.Unlock()
	var off sync.Once
	return func() {
		off.Do(func() {
			b.mu.Lock()
			delete(b.handlers, id)
			b.mu.Unlock()
		})
	}
}

// Publish delivers e to every current subscriber, in subscription order.
// Handlers run with the read lock held to keep the snapshot stable; do not
// call Subscribe/Publish from inside a handler (that would deadlock).
func (b *Sync[E]) Publish(e E) {
	b.mu.RLock()
	// Copy to a slice so we don't hold the lock during handler execution.
	handlers := make([]func(E), 0, len(b.handlers))
	// Iterate in id order for deterministic delivery.
	ids := make([]uint64, 0, len(b.handlers))
	for id := range b.handlers {
		ids = append(ids, id)
	}
	b.mu.RUnlock()

	sortIDs(ids)
	b.mu.RLock()
	for _, id := range ids {
		if h, ok := b.handlers[id]; ok {
			handlers = append(handlers, h)
		}
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		h(e)
	}
}

// Subscribers returns the current handler count.
func (b *Sync[E]) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.handlers)
}

// AsyncConfig controls Async behavior.
type AsyncConfig struct {
	// BufferSize is the capacity of each subscriber's queue. Defaults to
	// 16. When the queue is full, Publish behavior is governed by
	// DropWhenFull.
	BufferSize int
	// DropWhenFull, if true, drops events for any subscriber whose queue
	// is full instead of blocking the publisher. Drops are counted in
	// Stats. If false (default), Publish blocks until queue space is
	// available — slow subscribers can throttle the publisher.
	DropWhenFull bool
}

// Async delivers events to per-subscriber buffered channels processed by
// worker goroutines. A slow handler delays only its own queue, not other
// subscribers or the publisher.
type Async[E any] struct {
	cfg AsyncConfig

	mu       sync.Mutex
	nextID   uint64
	subs     map[uint64]*asyncSub[E]
	dropped  atomic.Int64
	closed   atomic.Bool
	closedWg sync.WaitGroup
}

type asyncSub[E any] struct {
	id      uint64
	handler func(E)
	ch      chan E
	stop    chan struct{}
}

// NewAsync returns an Async bus.
func NewAsync[E any](cfg AsyncConfig) *Async[E] {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 16
	}
	return &Async[E]{cfg: cfg, subs: map[uint64]*asyncSub[E]{}}
}

// Subscribe registers handler. Each subscriber owns a buffered channel; the
// handler runs in a dedicated goroutine.
func (b *Async[E]) Subscribe(handler func(E)) Unsubscribe {
	if handler == nil || b.closed.Load() {
		return func() {}
	}
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	sub := &asyncSub[E]{
		id:      id,
		handler: handler,
		ch:      make(chan E, b.cfg.BufferSize),
		stop:    make(chan struct{}),
	}
	b.subs[id] = sub
	b.mu.Unlock()

	b.closedWg.Add(1)
	go b.runSub(sub)

	var off sync.Once
	return func() {
		off.Do(func() {
			b.mu.Lock()
			if cur, ok := b.subs[id]; ok && cur == sub {
				delete(b.subs, id)
				close(sub.stop)
			}
			b.mu.Unlock()
		})
	}
}

// Publish delivers e to every subscriber's queue. With DropWhenFull, full
// queues drop the event and increment a counter.
func (b *Async[E]) Publish(e E) {
	if b.closed.Load() {
		return
	}
	b.mu.Lock()
	subs := make([]*asyncSub[E], 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	for _, s := range subs {
		if b.cfg.DropWhenFull {
			select {
			case s.ch <- e:
			default:
				b.dropped.Add(1)
			}
		} else {
			select {
			case s.ch <- e:
			case <-s.stop:
				// Subscriber is gone; drop silently.
			}
		}
	}
}

// Subscribers returns the current count.
func (b *Async[E]) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// AsyncStats reports cumulative bus activity.
type AsyncStats struct {
	Dropped int64 `json:"dropped"`
}

// Stats returns cumulative counters.
func (b *Async[E]) Stats() AsyncStats {
	return AsyncStats{Dropped: b.dropped.Load()}
}

// Close stops accepting new Publish calls, signals all subscriber goroutines
// to drain their queues, and waits for them to exit. Safe to call once.
func (b *Async[E]) Close() {
	if !b.closed.CompareAndSwap(false, true) {
		return
	}
	b.mu.Lock()
	for id, s := range b.subs {
		close(s.stop)
		delete(b.subs, id)
	}
	b.mu.Unlock()
	b.closedWg.Wait()
}

func (b *Async[E]) runSub(s *asyncSub[E]) {
	defer b.closedWg.Done()
	for {
		select {
		case e := <-s.ch:
			s.handler(e)
		case <-s.stop:
			// Drain anything already queued so handler ordering is
			// preserved relative to Publish; new sends are blocked
			// behind close because s.stop is closed.
			for {
				select {
				case e := <-s.ch:
					s.handler(e)
				default:
					return
				}
			}
		}
	}
}

// sortIDs sorts a slice of uint64 in ascending order using a simple
// insertion sort (handler counts in eventbus are typically small).
func sortIDs(ids []uint64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
