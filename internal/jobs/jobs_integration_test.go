//go:build integration

package jobs_test

import (
	"context"
	"os"
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

// newMigratedPool returns a pool on a fresh database with River's schema.
func newMigratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), stack.Postgres.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := jobs.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("jobs.Migrate: %v", err)
	}
	return pool
}

// startClient starts a River client and stops it (via jobs.Stop) when the
// test ends.
func startClient(t *testing.T, pool *pgxpool.Pool, workers *river.Workers) *river.Client[pgx.Tx] {
	t.Helper()
	client, err := jobs.NewClient(pool, workers)
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
		if err := jobs.Stop(client)(ctx); err != nil {
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
	client := startClient(t, pool, jobs.NewWorkers())
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
}

func TestPingJobRunsOnInteractiveQueue(t *testing.T) {
	t.Parallel()
	pool := newMigratedPool(t)
	client := startClient(t, pool, jobs.NewWorkers())
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
	client, err := jobs.NewClient(pool, jobs.NewWorkers())
	if err != nil {
		t.Fatal(err)
	}

	first, err := client.Insert(ctx, jobs.PingArgs{Message: "dup"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Insert(ctx, jobs.PingArgs{Message: "dup"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.UniqueSkippedAsDuplicate || !second.UniqueSkippedAsDuplicate || second.Job.ID != first.Job.ID {
		t.Errorf("dedupe: first=(%d, skipped=%v) second=(%d, skipped=%v)",
			first.Job.ID, first.UniqueSkippedAsDuplicate, second.Job.ID, second.UniqueSkippedAsDuplicate)
	}

	other, err := client.Insert(ctx, jobs.PingArgs{Message: "different"}, nil)
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

	client, err := jobs.NewClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
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
	if err := jobs.Stop(client)(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
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
