package jobs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Queue names. Use QueueInteractive for jobs that touch shared external state
// (third-party APIs, write-hot rows) so cluster-wide concurrency stays bounded
// independent of how many machines are running.
const (
	QueueInteractive = "interactive"
)

// Tunables. Exported so tests and ops tooling can reference the same values.
const (
	// JobTimeout caps any worker's runtime unless the worker overrides Timeout.
	// Must be < RescueStuckJobsAfter.
	JobTimeout = 2 * time.Minute

	// RescueStuckJobsAfter requeues jobs whose worker process died mid-run.
	// Tuned to Fly redeploy cadence — a crashed machine's jobs become visible
	// again within ~5 minutes instead of the default 1 hour.
	RescueStuckJobsAfter = 5 * time.Minute

	// StopGracePeriod is how long graceful Stop waits for in-flight jobs
	// before we escalate to StopAndCancel.
	StopGracePeriod = 8 * time.Second
)

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("river migrate: new: %w", err)
	}
	if _, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river migrate: up: %w", err)
	}
	return nil
}

func NewClient(pool *pgxpool.Pool, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		JobTimeout:           JobTimeout,
		RescueStuckJobsAfter: RescueStuckJobsAfter,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
			// Per-instance cap of 2; with N machines the cluster ceiling is 2N.
			// Lower this further for jobs whose collective rate must be small.
			QueueInteractive: {MaxWorkers: 2},
		},
		Workers: workers,
	})
}

// Stop tries a graceful shutdown first and escalates to StopAndCancel if the
// grace period elapses. The returned function is suitable as a shutdown hook:
// it never returns nil-but-leaks-goroutines the way bare Stop can when a
// worker overruns the hook timeout.
func Stop(client *river.Client[pgx.Tx]) func(context.Context) error {
	return func(ctx context.Context) error {
		graceCtx, cancel := context.WithTimeout(ctx, StopGracePeriod)
		defer cancel()
		if err := client.Stop(graceCtx); err == nil {
			return nil
		} else if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("river stop: %w", err)
		}
		log.Printf("river: graceful stop exceeded %s; cancelling in-flight jobs", StopGracePeriod)
		if err := client.StopAndCancel(ctx); err != nil {
			return fmt.Errorf("river stop-and-cancel: %w", err)
		}
		return nil
	}
}
