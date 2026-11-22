package eventbus

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type userCreated struct{ ID string }

func TestSyncDeliversInOrder(t *testing.T) {
	bus := NewSync[userCreated]()
	var got []string
	off := bus.Subscribe(func(e userCreated) { got = append(got, e.ID) })
	defer off()

	bus.Publish(userCreated{ID: "a"})
	bus.Publish(userCreated{ID: "b"})
	bus.Publish(userCreated{ID: "c"})

	want := []string{"a", "b", "c"}
	if len(got) != 3 {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("got[%d] = %q, want %q", i, got[i], v)
		}
	}
}

func TestSyncMultipleSubscribers(t *testing.T) {
	bus := NewSync[int]()
	var a, b int
	bus.Subscribe(func(e int) { a += e })
	bus.Subscribe(func(e int) { b += e })

	bus.Publish(1)
	bus.Publish(2)

	if a != 3 || b != 3 {
		t.Errorf("a=%d b=%d, want 3 each", a, b)
	}
	if got := bus.Subscribers(); got != 2 {
		t.Errorf("Subscribers = %d, want 2", got)
	}
}

func TestSyncUnsubscribe(t *testing.T) {
	bus := NewSync[int]()
	var got int
	off := bus.Subscribe(func(e int) { got += e })

	bus.Publish(5)
	off()
	bus.Publish(100)

	if got != 5 {
		t.Errorf("got = %d, want 5 (second publish must not deliver)", got)
	}
	if bus.Subscribers() != 0 {
		t.Errorf("Subscribers after off = %d, want 0", bus.Subscribers())
	}
}

func TestSyncUnsubscribeIsIdempotent(t *testing.T) {
	bus := NewSync[int]()
	off := bus.Subscribe(func(int) {})
	off()
	off() // must not panic
	if bus.Subscribers() != 0 {
		t.Errorf("Subscribers = %d, want 0", bus.Subscribers())
	}
}

func TestSyncNilHandler(t *testing.T) {
	bus := NewSync[int]()
	off := bus.Subscribe(nil)
	off()
	if bus.Subscribers() != 0 {
		t.Errorf("nil handler should not register")
	}
	bus.Publish(1) // must not panic
}

func TestSyncDeliveryOrderMatchesSubscriptionOrder(t *testing.T) {
	bus := NewSync[int]()
	var seq []string
	bus.Subscribe(func(int) { seq = append(seq, "first") })
	bus.Subscribe(func(int) { seq = append(seq, "second") })
	bus.Subscribe(func(int) { seq = append(seq, "third") })

	bus.Publish(1)

	want := []string{"first", "second", "third"}
	for i, w := range want {
		if seq[i] != w {
			t.Errorf("seq[%d] = %q, want %q", i, seq[i], w)
		}
	}
}

func TestAsyncDeliversEventually(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewAsync[int](AsyncConfig{})
		defer bus.Close()

		var got atomic.Int32
		bus.Subscribe(func(int) { got.Add(1) })

		for i := range 10 {
			bus.Publish(i)
		}

		synctest.Wait()
		if got.Load() != 10 {
			t.Errorf("Async delivery: got %d, want 10", got.Load())
		}
	})
}

func TestAsyncDropWhenFull(t *testing.T) {
	bus := NewAsync[int](AsyncConfig{BufferSize: 2, DropWhenFull: true})
	defer bus.Close()

	hold := make(chan struct{})
	bus.Subscribe(func(int) { <-hold })

	for i := range 100 {
		bus.Publish(i)
	}

	if bus.Stats().Dropped == 0 {
		t.Error("expected drops with full buffer + DropWhenFull")
	}
	close(hold)
}

func TestAsyncSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewAsync[int](AsyncConfig{BufferSize: 64, DropWhenFull: true})
		defer bus.Close()

		hold := make(chan struct{})
		bus.Subscribe(func(int) { <-hold })

		var fastGot atomic.Int32
		bus.Subscribe(func(int) { fastGot.Add(1) })

		for i := range 50 {
			bus.Publish(i)
		}

		// The slow subscriber stays parked on hold while the fast one drains.
		synctest.Wait()
		if fastGot.Load() != 50 {
			t.Errorf("fast subscriber got %d, want 50", fastGot.Load())
		}
		close(hold)
	})
}

func TestAsyncCloseDrainsQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewAsync[int](AsyncConfig{BufferSize: 16})

		var got atomic.Int32
		bus.Subscribe(func(int) {
			// Virtual time: the handler is slow, so events are still
			// queued when Close is called, but the test takes no wall time.
			time.Sleep(5 * time.Millisecond)
			got.Add(1)
		})

		for i := range 10 {
			bus.Publish(i)
		}
		bus.Close()

		if got.Load() != 10 {
			t.Errorf("after Close: got = %d, want 10 (Close should drain)", got.Load())
		}
	})
}

func TestAsyncPublishAfterCloseIsNoop(t *testing.T) {
	bus := NewAsync[int](AsyncConfig{})
	var got atomic.Int32
	bus.Subscribe(func(int) { got.Add(1) })
	bus.Close()

	bus.Publish(1) // must not panic, must not deliver
	if got.Load() != 0 {
		t.Errorf("got = %d after Publish to closed bus", got.Load())
	}
}

func TestAsyncUnsubscribeStopsDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewAsync[int](AsyncConfig{})
		defer bus.Close()

		var got atomic.Int32
		off := bus.Subscribe(func(int) { got.Add(1) })

		bus.Publish(1)
		synctest.Wait()
		off()
		bus.Publish(2)
		synctest.Wait()

		if got.Load() != 1 {
			t.Errorf("got = %d, want 1", got.Load())
		}
	})
}

func TestAsyncConcurrentPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := NewAsync[int](AsyncConfig{BufferSize: 1024})
		defer bus.Close()

		var got atomic.Int32
		bus.Subscribe(func(int) { got.Add(1) })

		var wg sync.WaitGroup
		for i := range 100 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				bus.Publish(i)
			}(i)
		}
		wg.Wait()

		synctest.Wait()
		if got.Load() != 100 {
			t.Errorf("concurrent Publish: got %d, want 100", got.Load())
		}
	})
}
