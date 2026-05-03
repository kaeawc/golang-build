package httpmw

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/idgen"
	"github.com/kaeawc/golang-build/internal/logger"
)

func TestComposeAppliesInOrder(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+"-pre")
				next.ServeHTTP(w, r)
				order = append(order, name+"-post")
			})
		}
	}
	chain := Compose(mw("a"), mw("b"), mw("c"))
	chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	want := []string{"a-pre", "b-pre", "c-pre", "handler", "c-post", "b-post", "a-post"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("order[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestRequestIDGenerates(t *testing.T) {
	gen := idgen.NewSequence("req")
	mw := RequestID(gen)
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFrom(r)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if captured != "req-1" {
		t.Errorf("captured = %q, want req-1", captured)
	}
	if got := rec.Header().Get("X-Request-ID"); got != "req-1" {
		t.Errorf("response header = %q", got)
	}
}

func TestRequestIDPreservesIncoming(t *testing.T) {
	gen := idgen.NewSequence("req")
	mw := RequestID(gen)
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFrom(r)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-ID", "client-supplied")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if captured != "client-supplied" {
		t.Errorf("captured = %q", captured)
	}
	if rec.Header().Get("X-Request-ID") != "client-supplied" {
		t.Errorf("response did not echo client id")
	}
}

func TestRequestIDSanitizesIncoming(t *testing.T) {
	gen := idgen.NewSequence("req")
	mw := RequestID(gen)
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFrom(r)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-ID", "evil\r\nX-Injected: yes")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if strings.ContainsAny(captured, "\r\n") {
		t.Errorf("captured contains CR/LF: %q", captured)
	}
	if strings.ContainsAny(rec.Header().Get("X-Request-ID"), "\r\n") {
		t.Errorf("response header contains CR/LF: %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestRequestIDBoundsLength(t *testing.T) {
	gen := idgen.NewSequence("req")
	mw := RequestID(gen)
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFrom(r)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-ID", strings.Repeat("a", MaxRequestIDLength*4))
	h.ServeHTTP(httptest.NewRecorder(), r)

	if len(captured) > MaxRequestIDLength {
		t.Errorf("captured length = %d, want ≤ %d", len(captured), MaxRequestIDLength)
	}
}

func TestRequestIDFromNilReturnsEmpty(t *testing.T) {
	if RequestIDFrom(nil) != "" {
		t.Error("RequestIDFrom(nil) should be empty")
	}
}

func TestRealIPTrustsForwardedHeader(t *testing.T) {
	trusted := NewTrustedProxies("10.0.0.0/8")
	mw := RealIP(trusted)
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RealIPFrom(r)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")

	h.ServeHTTP(httptest.NewRecorder(), r)
	if captured != "203.0.113.5" {
		t.Errorf("captured = %q, want 203.0.113.5", captured)
	}
}

func TestRealIPRejectsUntrustedPeer(t *testing.T) {
	trusted := NewTrustedProxies("10.0.0.0/8")
	mw := RealIP(trusted)
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RealIPFrom(r)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.99:1234"
	r.Header.Set("X-Forwarded-For", "10.0.0.5") // attacker tries to spoof

	h.ServeHTTP(httptest.NewRecorder(), r)
	if captured != "203.0.113.99" {
		t.Errorf("captured = %q, want untrusted peer's RemoteAddr", captured)
	}
}

func TestRealIPFallsBackToXRealIP(t *testing.T) {
	trusted := NewTrustedProxies("10.0.0.0/8")
	mw := RealIP(trusted)
	var captured string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RealIPFrom(r)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Real-IP", "198.51.100.7")

	h.ServeHTTP(httptest.NewRecorder(), r)
	if captured != "198.51.100.7" {
		t.Errorf("captured = %q", captured)
	}
}

func TestTrustedProxiesIPLiteral(t *testing.T) {
	tp := NewTrustedProxies("203.0.113.5", "192.0.2.0/24", "garbage", "")
	if !tp.Trusts(parseIP("203.0.113.5")) {
		t.Error("should trust exact IP")
	}
	if !tp.Trusts(parseIP("192.0.2.99")) {
		t.Error("should trust CIDR member")
	}
	if tp.Trusts(parseIP("198.51.100.1")) {
		t.Error("should not trust outside CIDR")
	}
}

func TestRecoverWritesInternalServerError(t *testing.T) {
	cap := logger.NewCapture(slog.LevelDebug)
	mw := Recover(cap)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !cap.HasMessage("panic in handler") {
		t.Error("expected panic to be logged")
	}
}

func TestRecoverPropagatesAbortHandler(t *testing.T) {
	mw := Recover(nil)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		rec := recover()
		err, _ := rec.(error)
		if !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("expected ErrAbortHandler to propagate, got %v", rec)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestLoggerEmitsAccessLog(t *testing.T) {
	cap := logger.NewCapture(slog.LevelDebug)
	mw := Logger(cap)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("hi"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/users", nil))

	recs := cap.Records()
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	r := recs[0]
	if r.Attrs["method"] != "POST" || r.Attrs["path"] != "/users" {
		t.Errorf("attrs = %+v", r.Attrs)
	}
	if r.Attrs["status"] != 201 {
		t.Errorf("status = %v, want 201", r.Attrs["status"])
	}
	if r.Attrs["bytes"] != int64(2) {
		t.Errorf("bytes = %v, want 2", r.Attrs["bytes"])
	}
}

func TestLoggerWithoutWriteHeaderDefaultsToOK(t *testing.T) {
	cap := logger.NewCapture(slog.LevelDebug)
	mw := Logger(cap)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	recs := cap.Records()
	if recs[0].Attrs["status"] != 200 {
		t.Errorf("status = %v, want 200", recs[0].Attrs["status"])
	}
}

func TestTimeoutCancelsContext(t *testing.T) {
	mw := Timeout(20 * time.Millisecond)
	var observedErr error
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		observedErr = r.Context().Err()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if observedErr == nil {
		t.Fatal("expected ctx error")
	}
}

func TestSecureHeadersDefaults(t *testing.T) {
	mw := SecureHeaders(DefaultSecureHeaders)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("X-Frame-Options not set")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options not set")
	}
	if rec.Header().Get("Referrer-Policy") == "" {
		t.Error("Referrer-Policy not set")
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS should NOT be set by default")
	}
}

func TestSecureHeadersOmitsEmpty(t *testing.T) {
	mw := SecureHeaders(SecureHeadersConfig{HSTS: "max-age=63072000"})
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Header().Get("Strict-Transport-Security") != "max-age=63072000" {
		t.Error("HSTS not set")
	}
	if rec.Header().Get("X-Frame-Options") != "" {
		t.Error("X-Frame-Options should be omitted when empty")
	}
}

func TestCORSAllowedOriginExact(t *testing.T) {
	mw := CORS(CORSConfig{AllowedOrigins: []string{"https://app.example"}})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Errorf("ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Error("Vary should include Origin when echoing exact origin")
	}
}

func TestCORSPreflight(t *testing.T) {
	mw := CORS(CORSConfig{AllowedOrigins: []string{"*"}, MaxAge: 600})
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("preflight should not invoke next handler")
	}))

	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://x")
	r.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Max-Age") != "600" {
		t.Error("Max-Age missing")
	}
}

func TestCORSWildcardWithCredentialsEchoesOrigin(t *testing.T) {
	mw := CORS(CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true})
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://specific.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://specific.example" {
		t.Error("with credentials, * should be replaced by exact origin")
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("ACAC missing")
	}
}

func TestCORSOriginNotAllowed(t *testing.T) {
	mw := CORS(CORSConfig{AllowedOrigins: []string{"https://allowed"}})
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://attacker")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("disallowed origin should not get CORS headers")
	}
}

// hijackableRecorder is an httptest.ResponseRecorder that also satisfies
// http.Hijacker for testing the Logger middleware's interface passthrough.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, bufio.NewReadWriter(bufio.NewReader(c1), bufio.NewWriter(c1)), nil
}

func TestLoggerForwardsHijack(t *testing.T) {
	cap := logger.NewCapture(slog.LevelDebug)
	mw := Logger(cap)
	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("recordingWriter should implement http.Hijacker when underlying does")
		}
		c, _, err := h.Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		_ = c.Close()
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !rec.hijacked {
		t.Error("Hijack was not forwarded to underlying writer")
	}
}

func TestLoggerHijackErrorsWhenUnsupported(t *testing.T) {
	cap := logger.NewCapture(slog.LevelDebug)
	mw := Logger(cap)
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("recordingWriter should advertise http.Hijacker")
		}
		_, _, err := h.Hijack()
		if err == nil {
			t.Error("Hijack on non-hijackable writer should error")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

// fullChain end-to-end exercises Compose + every middleware.
func TestFullChainEndToEnd(t *testing.T) {
	gen := idgen.NewSequence("rid")
	cap := logger.NewCapture(slog.LevelDebug)
	chain := Compose(
		RequestID(gen),
		RealIP(NewTrustedProxies("127.0.0.0/8")),
		Recover(cap),
		Logger(cap),
		SecureHeaders(DefaultSecureHeaders),
	)
	srv := httptest.NewServer(chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFrom(r) == "" {
			t.Error("RequestID not propagated")
		}
		_, _ = io.WriteString(w, "hello")
	})))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if !cap.HasMessage("http request") {
		t.Error("access log not emitted")
	}
}

func parseIP(s string) net.IP { return net.ParseIP(s) }
