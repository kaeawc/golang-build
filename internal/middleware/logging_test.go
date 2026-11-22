package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/idgen"
	"github.com/kaeawc/golang-build/internal/logger"
)

// decodeRecords parses one JSON log record per line.
func decodeRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestLoggingScopesLoggerToRequest(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := httpmw.RequestID(idgen.NewSequence("req"))(Logging(base)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.FromContext(r.Context()).Info("from handler", "k", "v")
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("short and stout"))
		})))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/brew?x=1", nil))

	recs := decodeRecords(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %s", len(recs), buf.String())
	}
	for _, rec := range recs {
		if rec["request_id"] != "req-1" || rec["method"] != "POST" || rec["path"] != "/brew" {
			t.Errorf("record missing request scope: %v", rec)
		}
	}
	if recs[0]["msg"] != "from handler" || recs[0]["k"] != "v" {
		t.Errorf("handler record = %v", recs[0])
	}
	access := recs[1]
	if access["msg"] != "http request" || access["status"] != float64(http.StatusTeapot) || access["bytes"] != float64(len("short and stout")) {
		t.Errorf("access record = %v", access)
	}
}

func TestRecoverLogsThroughRequestLogger(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := Logging(base)(Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/explode", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	recs := decodeRecords(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %s", len(recs), buf.String())
	}
	p := recs[0]
	if p["level"] != "ERROR" || p["panic"] != "boom" || p["path"] != "/explode" || !strings.Contains(p["stack"].(string), "recovery.go") {
		t.Errorf("panic record = %v", p)
	}
	if recs[1]["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("access record status = %v, want 500", recs[1]["status"])
	}
}

func TestRecoverRepanicsErrAbortHandler(t *testing.T) {
	handler := Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if rec, _ := recover().(error); !errors.Is(rec, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler", rec)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
