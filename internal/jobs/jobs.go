// Package jobs defines River background jobs and the worker registry.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
)

type PingArgs struct {
	Message string `json:"message"`
}

func (PingArgs) Kind() string { return "ping" }

// InsertOpts dedupes ping enqueues with the same Message within a 1-minute
// window across pending/scheduled/available/running states, so a burst of
// identical inserts collapses to a single job.
func (PingArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		UniqueOpts: river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: time.Minute,
		},
	}
}

type PingWorker struct {
	river.WorkerDefaults[PingArgs]
	log *slog.Logger
}

func (w *PingWorker) Work(ctx context.Context, job *river.Job[PingArgs]) error {
	w.log.InfoContext(ctx, "jobs.ping",
		"job_id", job.ID,
		"message", job.Args.Message,
		"scheduled_at", job.ScheduledAt.Format(time.RFC3339),
	)
	return nil
}

func (*PingWorker) Timeout(*river.Job[PingArgs]) time.Duration { return 30 * time.Second }

// NewWorkers registers every worker; they log through log.
func NewWorkers(log *slog.Logger) *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &PingWorker{log: log})
	return w
}
