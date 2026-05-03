// Package jobs defines River background jobs and the worker registry.
package jobs

import (
	"context"
	"log"
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
}

func (*PingWorker) Work(_ context.Context, job *river.Job[PingArgs]) error {
	log.Printf("jobs.ping: %s (scheduled=%s)", job.Args.Message, job.ScheduledAt.Format(time.RFC3339))
	return nil
}

func (*PingWorker) Timeout(*river.Job[PingArgs]) time.Duration { return 30 * time.Second }

func NewWorkers() *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &PingWorker{})
	return w
}
