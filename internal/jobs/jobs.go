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

type PingWorker struct {
	river.WorkerDefaults[PingArgs]
}

func (*PingWorker) Work(_ context.Context, job *river.Job[PingArgs]) error {
	log.Printf("jobs.ping: %s (scheduled=%s)", job.Args.Message, job.ScheduledAt.Format(time.RFC3339))
	return nil
}

func NewWorkers() *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &PingWorker{})
	return w
}
