package middleware

import (
	"net/http"
	"sync"
	"time"
)

type TrafficStat struct {
	Method     string        `json:"method"`
	Path       string        `json:"path"`
	Count      uint64        `json:"count"`
	Status2xx  uint64        `json:"status2xx"`
	Status4xx  uint64        `json:"status4xx"`
	Status5xx  uint64        `json:"status5xx"`
	LastStatus int           `json:"lastStatus"`
	LastNanos  time.Duration `json:"lastNanos"`
	TotalNanos time.Duration `json:"totalNanos"`
}

type TrafficRecorder struct {
	mu    sync.Mutex
	stats map[string]*TrafficStat
}

func NewTrafficRecorder() *TrafficRecorder {
	return &TrafficRecorder{stats: map[string]*TrafficStat{}}
}

func (t *TrafficRecorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		t.record(r.Method, r.URL.Path, sw.status, time.Since(start))
	})
}

func (t *TrafficRecorder) record(method, path string, status int, dur time.Duration) {
	key := method + " " + path
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.stats[key]
	if !ok {
		s = &TrafficStat{Method: method, Path: path}
		t.stats[key] = s
	}
	s.Count++
	s.LastStatus = status
	s.LastNanos = dur
	s.TotalNanos += dur
	switch {
	case status >= 500:
		s.Status5xx++
	case status >= 400:
		s.Status4xx++
	case status >= 200 && status < 300:
		s.Status2xx++
	}
}

func (t *TrafficRecorder) Snapshot() []TrafficStat {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TrafficStat, 0, len(t.stats))
	for _, s := range t.stats {
		out = append(out, *s)
	}
	return out
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}
