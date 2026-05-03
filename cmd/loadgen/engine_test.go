package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClient is a deterministic HTTPClient: returns a fixed status and
// counts every Do call.
type fakeClient struct {
	calls  atomic.Int32
	status int
	err    error
	delay  time.Duration
}

func (f *fakeClient) Do(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestEngineHitsTestServerAndRecordsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	eng := NewEngine(Config{
		URL:         srv.URL,
		Concurrency: 4,
		Duration:    100 * time.Millisecond,
	})
	eng.Start(context.Background())

	snap := eng.Snapshot()
	if !snap.Done {
		t.Errorf("expected Done after Start returns")
	}
	if snap.Total < 1 {
		t.Errorf("expected at least 1 request, got %d", snap.Total)
	}
	if snap.StatusCounts[200] != snap.Total {
		t.Errorf("all requests should be 200, got counts=%v total=%d", snap.StatusCounts, snap.Total)
	}
	if snap.RPS <= 0 {
		t.Errorf("expected positive RPS, got %f", snap.RPS)
	}
	if snap.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", snap.Errors)
	}
}

func TestEngineRecordsErrorsForFailedRequests(t *testing.T) {
	client := &fakeClient{err: errors.New("dial fail")}
	eng := NewEngine(Config{
		URL:         "http://example.invalid",
		Concurrency: 2,
		Duration:    50 * time.Millisecond,
		Client:      client,
	})
	eng.Start(context.Background())

	snap := eng.Snapshot()
	if snap.Total == 0 || snap.Errors != snap.Total {
		t.Errorf("expected all errors, got total=%d errors=%d", snap.Total, snap.Errors)
	}
}

func TestEngineRecordsBadStatusAsError(t *testing.T) {
	client := &fakeClient{status: 503}
	eng := NewEngine(Config{
		URL:         "http://example.invalid",
		Concurrency: 1,
		Duration:    50 * time.Millisecond,
		Client:      client,
	})
	eng.Start(context.Background())

	snap := eng.Snapshot()
	if snap.StatusCounts[503] == 0 {
		t.Errorf("expected 503 entries, got %v", snap.StatusCounts)
	}
	if snap.Errors == 0 {
		t.Errorf("expected errors > 0 for 5xx, got %d", snap.Errors)
	}
}

func TestEngineRespectsContextCancellation(t *testing.T) {
	client := &fakeClient{status: 200, delay: 10 * time.Millisecond}
	eng := NewEngine(Config{
		URL:         "http://example.invalid",
		Concurrency: 2,
		Duration:    time.Hour,
		Client:      client,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	done := make(chan struct{})
	go func() { eng.Start(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("engine did not honor cancel")
	}
}

func TestPercentilesEmpty(t *testing.T) {
	p50, p95, p99 := percentiles(nil)
	if p50 != 0 || p95 != 0 || p99 != 0 {
		t.Errorf("expected zeros for empty input, got %v %v %v", p50, p95, p99)
	}
}

func TestPercentilesSorted(t *testing.T) {
	in := []time.Duration{
		10 * time.Millisecond,
		1 * time.Millisecond,
		100 * time.Millisecond,
		50 * time.Millisecond,
		5 * time.Millisecond,
	}
	p50, p95, p99 := percentiles(in)
	if p50 != 10*time.Millisecond {
		t.Errorf("p50 = %v, want 10ms", p50)
	}
	if p99 != 100*time.Millisecond {
		t.Errorf("p99 = %v, want 100ms", p99)
	}
	if p95 < p50 || p99 < p95 {
		t.Errorf("percentiles not monotonic: %v %v %v", p50, p95, p99)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	eng := NewEngine(Config{
		URL:         "http://x",
		Concurrency: 1,
		Duration:    time.Millisecond,
		Client:      &fakeClient{status: 200},
	})
	eng.Start(context.Background())
	a := eng.Snapshot()
	a.StatusCounts[999] = 42
	b := eng.Snapshot()
	if _, ok := b.StatusCounts[999]; ok {
		t.Errorf("Snapshot returned shared map; mutation leaked")
	}
}

func TestRenderSnapshotMentionsKeyMetrics(t *testing.T) {
	s := Snapshot{
		Elapsed:      2 * time.Second,
		Total:        100,
		Errors:       3,
		StatusCounts: map[int]int{200: 97, 500: 3},
		RPS:          50,
		P50:          5 * time.Millisecond,
		P99:          20 * time.Millisecond,
		TargetURL:    "http://example/x",
		Concurrency:  4,
	}
	out := renderSnapshot(s)
	for _, want := range []string{"http://example/x", "100", "50.0", "5.0ms", "200", "500"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("renderSnapshot missing %q in:\n%s", want, out)
		}
	}
}

func TestHeadlessRunsAgainstTestServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	snap, err := runHeadless(&buf, srv.URL, 2, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("runHeadless: %v", err)
	}
	if snap.Total < 1 {
		t.Errorf("expected requests > 0")
	}
	if buf.Len() == 0 {
		t.Errorf("expected output written")
	}
}

func TestHeadlessRejectsInvalidArgs(t *testing.T) {
	if _, err := runHeadless(&bytes.Buffer{}, "", 1, time.Second); err == nil {
		t.Errorf("expected error for empty URL")
	}
	if _, err := runHeadless(&bytes.Buffer{}, "http://x", 1, 0); err == nil {
		t.Errorf("expected error for zero duration")
	}
}
