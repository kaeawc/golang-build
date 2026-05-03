package cacheutil

import (
	"errors"
	"sync"
	"sync/atomic"
)

// AsyncWriter runs bounded background cache-write jobs. Submit is
// deliberately non-blocking: callers can fall back to synchronous writes
// when the queue is full or closing instead of silently dropping entries.
type AsyncWriter struct {
	jobs chan func() (int64, error)

	mu       sync.Mutex
	closed   bool
	firstErr error

	jobsWG    sync.WaitGroup
	workersWG sync.WaitGroup

	queued    atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
	bytes     atomic.Int64
}

type AsyncWriterStats struct {
	Queued    int64 `json:"queued"`
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
	Bytes     int64 `json:"bytes"`
}

// NewAsyncWriter starts workers background goroutines and buffers up to
// queueSize accepted jobs. Values below one are clamped to one.
func NewAsyncWriter(workers, queueSize int) *AsyncWriter {
	if workers < 1 {
		workers = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}
	w := &AsyncWriter{jobs: make(chan func() (int64, error), queueSize)}
	for i := 0; i < workers; i++ {
		w.workersWG.Add(1)
		go w.run()
	}
	return w
}

// Submit accepts a write job if the writer is open and its queue has
// capacity. It returns false without blocking when the caller should
// perform the write synchronously.
func (w *AsyncWriter) Submit(job func() (int64, error)) bool {
	if w == nil || job == nil {
		return false
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}

	w.jobsWG.Add(1)
	select {
	case w.jobs <- job:
		w.queued.Add(1)
		return true
	default:
		w.jobsWG.Done()
		return false
	}
}

// Flush waits for all accepted jobs to finish and returns accumulated
// write errors observed so far, if any.
func (w *AsyncWriter) Flush() error {
	if w == nil {
		return nil
	}
	w.jobsWG.Wait()

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.firstErr
}

// Close prevents new submissions, drains accepted jobs, and waits for
// worker goroutines to exit.
func (w *AsyncWriter) Close() error {
	if w == nil {
		return nil
	}

	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.jobs)
	}
	w.mu.Unlock()

	err := w.Flush()
	w.workersWG.Wait()
	return err
}

func (w *AsyncWriter) Stats() AsyncWriterStats {
	if w == nil {
		return AsyncWriterStats{}
	}
	return AsyncWriterStats{
		Queued:    w.queued.Load(),
		Completed: w.completed.Load(),
		Failed:    w.failed.Load(),
		Bytes:     w.bytes.Load(),
	}
}

func (w *AsyncWriter) run() {
	defer w.workersWG.Done()
	for job := range w.jobs {
		bytes, err := job()
		if bytes > 0 {
			w.bytes.Add(bytes)
		}
		if err != nil {
			w.failed.Add(1)
			w.recordErr(err)
		}
		w.completed.Add(1)
		w.jobsWG.Done()
	}
}

func (w *AsyncWriter) recordErr(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.firstErr = errors.Join(w.firstErr, err)
}
