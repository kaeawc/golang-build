package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kaeawc/golang-build/internal/cache"
	"github.com/kaeawc/golang-build/internal/middleware"
)

type Probe struct {
	Name      string `json:"name"`
	Healthy   bool   `json:"healthy"`
	LatencyNs int64  `json:"latencyNs"`
	Err       string `json:"err,omitempty"`
}

func AdminTraffic(rec *middleware.TrafficRecorder) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, rec.Snapshot())
	}
}

func AdminHealthchecks(pool *pgxpool.Pool, c cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		probes := []Probe{probeDB(ctx, pool)}
		if c != nil {
			probes = append(probes, probeCache(ctx, c))
		}
		writeJSON(w, probes)
	}
}

func probeDB(ctx context.Context, pool *pgxpool.Pool) Probe {
	start := time.Now()
	err := pool.Ping(ctx)
	dur := time.Since(start)
	if err != nil {
		return Probe{Name: "db", Healthy: false, LatencyNs: dur.Nanoseconds(), Err: err.Error()}
	}
	return Probe{Name: "db", Healthy: true, LatencyNs: dur.Nanoseconds()}
}

func probeCache(ctx context.Context, c cache.Cache) Probe {
	start := time.Now()
	_, err := c.Get(ctx, "_admin_probe")
	dur := time.Since(start)
	if err != nil && !isMissingKey(err) {
		return Probe{Name: "valkey", Healthy: false, LatencyNs: dur.Nanoseconds(), Err: err.Error()}
	}
	return Probe{Name: "valkey", Healthy: true, LatencyNs: dur.Nanoseconds()}
}

func isMissingKey(err error) bool {
	type nilErr interface{ IsValkeyNil() bool }
	var ne nilErr
	if errors.As(err, &ne) && ne.IsValkeyNil() {
		return true
	}
	return strings.Contains(err.Error(), "valkey nil")
}

func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(data); err != nil {
		log.Printf("write error: %v", err)
	}
}
