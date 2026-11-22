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

// Gzip sits inside Recover in the server chain: a panic must still give a
// clean 500 (no gzip bytes committing a 200), and the logged stack must
// reach the panicking handler.
func TestRecoverInsideGzipReturns500WithOriginalStack(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := Logging(base)(Recover(Gzip(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panicInHandler()
	}))))

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q on the 500, want none", got)
	}
	if got := rr.Body.String(); got != "Internal Server Error\n" {
		t.Errorf("body = %q", got)
	}
	recs := decodeRecords(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %s", len(recs), buf.String())
	}
	if stack, _ := recs[0]["stack"].(string); !strings.Contains(stack, "panicInHandler") {
		t.Errorf("panic stack lost the handler frame:\n%s", stack)
	}
	if recs[1]["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("access status = %v, want 500", recs[1]["status"])
	}
}

//go:noinline
func panicInHandler() { panic("boom") }

func TestLoggingRecordsAbortedRequests(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := Logging(base)(Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})))
	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream", nil))
	}()
	recs := decodeRecords(t, &buf)
	if len(recs) != 1 || recs[0]["msg"] != "http request" || recs[0]["aborted"] != true {
		t.Errorf("records = %v, want one access line with aborted=true", recs)
	}
}

func TestLoggingRecordsFinalStatusAndQuery(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := Logging(base)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusAccepted)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x?page=2", nil))
	recs := decodeRecords(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
	if recs[0]["status"] != float64(http.StatusAccepted) || recs[0]["query"] != "page=2" || recs[0]["aborted"] != nil {
		t.Errorf("access record = %v, want status 202, query page=2, not aborted", recs[0])
	}
}

func TestAccessWriterForwardsFlush(t *testing.T) {
	rr := httptest.NewRecorder()
	Logging(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
		if _, ok := w.(http.Flusher); !ok {
			t.Error("writer does not implement http.Flusher")
		}
	})).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !rr.Flushed {
		t.Error("flush did not reach the underlying writer")
	}
}
