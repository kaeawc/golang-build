package jobs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestPingWorkerLogsThroughInjectedLogger(t *testing.T) {
	var buf bytes.Buffer
	w := &PingWorker{log: slog.New(slog.NewJSONHandler(&buf, nil))}
	job := &river.Job[PingArgs]{
		JobRow: &rivertype.JobRow{ID: 42, ScheduledAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		Args:   PingArgs{Message: "hello"},
	}
	if err := w.Work(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode %q: %v", buf.String(), err)
	}
	if rec["msg"] != "jobs.ping" || rec["job_id"] != float64(42) || rec["message"] != "hello" || rec["scheduled_at"] != "2026-01-02T03:04:05Z" {
		t.Errorf("record = %v", rec)
	}
}
