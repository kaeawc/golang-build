//go:build integration

package jobs_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"

	"github.com/kaeawc/golang-build/internal/jobs"
	"github.com/kaeawc/golang-build/internal/testinfra"
)

var stack *testinfra.Stack

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context, s *testinfra.Stack) error {
		stack = s
		_, err := s.StartPostgres(ctx)
		return err
	}))
}

// waitTimeout bounds how long a test waits for River to work a job.
const waitTimeout = 30 * time.Second

// logSink collects JSON slog records from River and the workers so tests
// can assert on what the jobs package logs. Safe for concurrent use.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// newLogger returns a logger writing JSON records into a fresh logSink.
func newLogger() (*slog.Logger, *logSink) {
	sink := &logSink{}
	return slog.New(slog.NewJSONHandler(sink, nil)), sink
}

// has reports whether a record with msg carries every attr in want.
func (s *logSink) has(msg string, want map[string]any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := bufio.NewScanner(bytes.NewReader(s.buf.Bytes()))
	for sc.Scan() {
		var rec map[string]any
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec["msg"] != msg {
			continue
		}
		match := true
		for k, v := range want {
			if rec[k] != v {
				match = false
			}
		}
		if match {
			return true
		}
	}
	return false
}

// newMigratedPool returns a pool on a fresh database with River's schema.
func newMigratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testinfra.NewPool(t, stack.Postgres.NewDatabase(t))
	if err := jobs.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("jobs.Migrate: %v", err)
	}
	return pool
}

// startClient starts a River client and stops it (via jobs.Stop) when the
// test ends.
func startClient(t *testing.T, pool *pgxpool.Pool, workers *river.Workers, log *slog.Logger) *river.Client[pgx.Tx] {
	t.Helper()
	client, err := jobs.NewClient(pool, workers, log)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		// t.Context is already cancelled during cleanup.
		ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
		defer cancel()
		if err := jobs.Stop(client, log)(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return client
}

func TestMigrateAppliesRiverSchema(t *testing.T) {
	t.Parallel()
	pool := newMigratedPool(t)
	ctx := t.Context()

	// Re-running is what every boot does; it must be a no-op.
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Validate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Errorf("river schema not current: %v", res.Messages)
	}
	for _, table := range []string{"river_job", "river_leader", "river_queue"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("table %s missing after Migrate", table)
		}
	}
}

func TestMigrateFailsWithoutDatabase(t *testing.T) {
	t.Parallel()
	pool := newMigratedPool(t)
	pool.Close()
	if err := jobs.Migrate(t.Context(), pool); err == nil {
		t.Error("Migrate on closed pool: want error")
	}
}

func TestPingJobRuns(t *testing.T) {
	t.Parallel()
	pool := newMigratedPool(t)
	log, sink := newLogger()
	client := startClient(t, pool, jobs.NewWorkers(log), log)
	events, cancel := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()

	res, err := client.Insert(t.Context(), jobs.PingArgs{Message: "hello"}, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	ev := waitForJob(t, events, res.Job.ID)
	if ev.Kind != river.EventKindJobCompleted {
		t.Fatalf("job %d: event %s, want completed (errors: %v)", res.Job.ID, ev.Kind, ev.Job.Errors)
	}
	row, err := client.JobGet(t.Context(), res.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != rivertype.JobStateCompleted || row.Kind != "ping" || row.Queue != river.QueueDefault {
		t.Errorf("job row = state %s kind %s queue %s", row.State, row.Kind, row.Queue)
	}
	// The worker logs through the injected logger, with the job's ID.
	if !sink.has("jobs.ping", map[string]any{"message": "hello", "job_id": float64(res.Job.ID)}) {
		t.Errorf("no jobs.ping record for job %d in logs:\n%s", res.Job.ID, sink.String())
	}
}

func TestPingJobRunsOnInteractiveQueue(t *testing.T) {
	t.Parallel()
	pool := newMigratedPool(t)
	log, _ := newLogger()
	client := startClient(t, pool, jobs.NewWorkers(log), log)
	events, cancel := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()

	res, err := client.Insert(t.Context(), jobs.PingArgs{Message: "interactive"},
		&river.InsertOpts{Queue: jobs.QueueInteractive})
	if err != nil {
		t.Fatal(err)
	}
	if ev := waitForJob(t, events, res.Job.ID); ev.Kind != river.EventKindJobCompleted || ev.Job.Queue != jobs.QueueInteractive {
		t.Errorf("event = %s on queue %s, want completed on %s", ev.Kind, ev.Job.Queue, jobs.QueueInteractive)
	}
}

func TestPingInsertsAreUnique(t *testing.T) {
	t.Parallel()
	pool := newMigratedPool(t)
	ctx := t.Context()
	// An insert-only client: nothing works the jobs, so both inserts see
	// the first one still available.
	log, _ := newLogger()
	client, err := jobs.NewClient(pool, jobs.NewWorkers(log), log)
	if err != nil {
		t.Fatal(err)
	}

	// ByPeriod buckets on ScheduledAt (default: now) truncated to the
	// minute; pin it so the two inserts cannot straddle a boundary.
	opts := &river.InsertOpts{ScheduledAt: time.Now().Truncate(time.Minute)}
	first, err := client.Insert(ctx, jobs.PingArgs{Message: "dup"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Insert(ctx, jobs.PingArgs{Message: "dup"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.UniqueSkippedAsDuplicate || !second.UniqueSkippedAsDuplicate || second.Job.ID != first.Job.ID {
		t.Errorf("dedupe: first=(%d, skipped=%v) second=(%d, skipped=%v)",
			first.Job.ID, first.UniqueSkippedAsDuplicate, second.Job.ID, second.UniqueSkippedAsDuplicate)
	}

	other, err := client.Insert(ctx, jobs.PingArgs{Message: "different"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if other.UniqueSkippedAsDuplicate {
		t.Error("different args were deduplicated")
	}
}

// blockArgs is a job whose worker runs until its context is cancelled, to
// exercise jobs.Stop's escalation to StopAndCancel.
type blockArgs struct{}

func (blockArgs) Kind() string { return "test_block" }

type blockWorker struct {
	river.WorkerDefaults[blockArgs]
	started   chan struct{}
	cancelled chan struct{}
}

func (w *blockWorker) Work(ctx context.Context, _ *river.Job[blockArgs]) error {
	close(w.started)
	<-ctx.Done()
	close(w.cancelled)
	return ctx.Err()
}

func TestStopEscalatesToCancel(t *testing.T) {
	t.Parallel()
	pool := newMigratedPool(t)
	w := &blockWorker{started: make(chan struct{}), cancelled: make(chan struct{})}
	workers := river.NewWorkers()
	river.AddWorker(workers, w)

	log, sink := newLogger()
	client, err := jobs.NewClient(pool, workers, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Backstop if the test fails before jobs.Stop: release the pool's
	// connections before newMigratedPool's cleanup closes it.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
		defer cancel()
		_ = client.StopAndCancel(ctx)
	})
	if _, err := client.Insert(t.Context(), blockArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.started:
	case <-time.After(waitTimeout):
		t.Fatal("blocking job never started")
	}

	ctx, cancel := context.WithTimeout(context.Background(), jobs.StopGracePeriod+waitTimeout)
	defer cancel()
	start := time.Now()
	if err := jobs.Stop(client, log)(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !sink.has("river graceful stop timed out; cancelling in-flight jobs", map[string]any{"level": "WARN"}) {
		t.Errorf("no escalation warning in logs:\n%s", sink.String())
	}
	if elapsed := time.Since(start); elapsed < jobs.StopGracePeriod {
		t.Errorf("Stop returned after %s, before the %s grace period", elapsed, jobs.StopGracePeriod)
	}
	select {
	case <-w.cancelled:
	default:
		t.Error("in-flight job was not cancelled")
	}
}

func waitForJob(t *testing.T, events <-chan *river.Event, id int64) *river.Event {
	t.Helper()
	timeout := time.After(waitTimeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed")
			}
			if ev.Job.ID == id {
				return ev
			}
		case <-timeout:
			t.Fatalf("job %d did not finish within %s", id, waitTimeout)
			return nil
		}
	}
}
