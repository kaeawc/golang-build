package cacheutil_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kaeawc/golang-build/internal/cacheutil"
)

func TestAsyncWriter_CloseFlushesQueuedJobs(t *testing.T) {
	w := cacheutil.NewAsyncWriter(2, 8)
	var ran atomic.Int64
	for range 16 {
		if !w.Submit(func() (int64, error) {
			ran.Add(1)
			return 0, nil
		}) {
			ran.Add(1)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := ran.Load(); got != 16 {
		t.Fatalf("ran %d jobs, want 16", got)
	}
	if w.Submit(func() (int64, error) { return 0, nil }) {
		t.Fatal("Submit after Close returned true")
	}
}

func TestAsyncWriter_SubmitFallsBackWhenQueueFull(t *testing.T) {
	w := cacheutil.NewAsyncWriter(1, 1)
	block := make(chan struct{})
	if !w.Submit(func() (int64, error) {
		<-block
		return 0, nil
	}) {
		t.Fatal("first Submit returned false")
	}

	var queued int
	for range 64 {
		if w.Submit(func() (int64, error) { return 0, nil }) {
			queued++
		}
	}
	if queued > 1 {
		t.Fatalf("queued %d jobs into a size-1 queue while worker was blocked", queued)
	}
	close(block)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAsyncWriter_CloseReturnsJobErrors(t *testing.T) {
	w := cacheutil.NewAsyncWriter(1, 4)
	want := errors.New("boom")
	if !w.Submit(func() (int64, error) { return 0, want }) {
		t.Fatal("Submit returned false")
	}
	err := w.Close()
	if !errors.Is(err, want) {
		t.Fatalf("Close error = %v, want %v", err, want)
	}
}

func TestAsyncWriter_CloseWhileSubmittingDoesNotPanic(t *testing.T) {
	w := cacheutil.NewAsyncWriter(1, 1)
	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for first := true; ; first = false {
			select {
			case <-stop:
				return
			default:
				_ = w.Submit(func() (int64, error) { return 0, nil })
			}
			if first {
				close(started)
			}
		}
	}()
	// Close only once the submitter is spinning, and keep it spinning
	// until Close has returned, so the two always overlap.
	<-started
	_ = w.Close()
	close(stop)
	<-done
}
