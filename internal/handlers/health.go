package handlers

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Cache window for the DB ping. Fly probes /healthz every ~15s, but other
// callers (LBs, uptime checks) can hit it harder; this keeps a single
// connection from being burned per probe.
const healthCacheTTL = 5 * time.Second

type healthState struct {
	at      time.Time
	healthy bool
}

func Health(pool *pgxpool.Pool) http.HandlerFunc {
	var state atomic.Pointer[healthState]
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")

		s := state.Load()
		if s == nil || time.Since(s.at) > healthCacheTTL {
			ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
			s = &healthState{at: time.Now(), healthy: pool.Ping(ctx) == nil}
			cancel()
			state.Store(s)
		}
		if !s.healthy {
			http.Error(w, `{"status":"unhealthy"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}
