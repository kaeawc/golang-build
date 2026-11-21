package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/healthcheck"
	"github.com/kaeawc/golang-build/internal/tokens"
)

type httpFixture struct {
	url     string // MCP endpoint
	base    string
	store   *MemoryStore
	issuer  *tokens.HMAC256
	consent *MemoryConsent
}

// newHTTPFixture serves New(...) via NewHTTPHandler on an httptest server.
// When withAuth is set, bearer tokens are required and verified.
func newHTTPFixture(t *testing.T, withAuth bool) *httpFixture {
	t.Helper()
	f := &httpFixture{store: NewMemoryStore(DemoUsers...), issuer: tokens.NewHMAC256([]byte("test-secret")), consent: NewMemoryConsent()}
	ts := httptest.NewUnstartedServer(nil)
	f.base = "http://" + ts.Listener.Addr().String()
	f.url = f.base + PathMCP

	health := healthcheck.NewRegistry()
	health.RegisterReadiness("store", func(ctx context.Context) error {
		_, err := f.store.List(ctx, 0, 1)
		return err
	})
	server := New(Deps{Store: f.store, Health: health, Consent: f.consent, ConsentURL: f.base + PathConsent, ExportStepDelay: time.Millisecond})
	opts := HTTPOptions{Logger: slog.New(slog.DiscardHandler), Health: health, Consent: f.consent}
	if withAuth {
		opts.Auth = &AuthOptions{Verifier: f.issuer, ResourceURL: f.url, AuthorizationServers: []string{"https://auth.example.com"}}
	}
	h, err := NewHTTPHandler(server, opts)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h
	ts.Start()
	t.Cleanup(ts.Close)
	return f
}

func (f *httpFixture) token(t *testing.T, audience string, ttl time.Duration, scopes ...string) string {
	t.Helper()
	tok, err := MintToken(f.issuer, clock.Default, "alice", audience, scopes, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func (f *httpFixture) connect(t *testing.T, version, token string, copts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: f.url, MaxRetries: -1}
	if token != "" {
		transport.HTTPClient = &http.Client{Transport: bearer{token, http.DefaultTransport}}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "v0"}, copts).
		Connect(t.Context(), transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("connect %s: %v", version, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func acceptDeletes(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
}

func TestHTTPProtocolModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version     string
		wantSession bool
	}{
		// 2026-07-28 is routed to the stateless handler: no Mcp-Session-Id.
		{latest, false},
		// 2025-11-25 gets a classic stateful session.
		{legacy, true},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			f := newHTTPFixture(t, false)
			updates := make(chan string, 8)
			progress := make(chan float64, 16)
			cs := f.connect(t, tt.version, "", &mcp.ClientOptions{
				ElicitationHandler:     acceptDeletes,
				ResourceUpdatedHandler: func(_ context.Context, r *mcp.ResourceUpdatedNotificationRequest) { updates <- r.Params.URI },
				ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
					progress <- r.Params.Progress
				},
			})
			ctx := t.Context()
			if got := cs.InitializeResult().ProtocolVersion; got != tt.version {
				t.Fatalf("negotiated %s, want %s", got, tt.version)
			}
			if hasSession := cs.ID() != ""; hasSession != tt.wantSession {
				t.Errorf("session id %q, wantSession=%t", cs.ID(), tt.wantSession)
			}

			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolGetUser, Arguments: map[string]any{"id": 1}})
			if err != nil || res.IsError {
				t.Fatalf("get_user: %v %+v", err, res)
			}

			// Progress notifications ride the POST response stream.
			if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolExportUsers, Arguments: map[string]any{"batchSize": 6}, Meta: mcp.Meta{"progressToken": "p"}}); err != nil {
				t.Fatal(err)
			}
			if got := waitFor(t, progress, "progress"); got != 6 {
				t.Errorf("first progress = %v", got)
			}

			// Subscriptions: subscriptions/listen (stateless) vs resources/subscribe + GET stream (stateful).
			if err := cs.Subscribe(ctx, &mcp.SubscribeParams{URI: URIDirectory}); err != nil {
				t.Fatal(err)
			}
			waitSubscribed(t, cs, f, updates)

			// MRTR delete: client-driven retry (stateless) vs server-sent elicitation (stateful).
			res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolDeleteUser, Arguments: map[string]any{"id": 2}})
			if err != nil || res.IsError {
				t.Fatalf("delete_user: %v %+v", err, res)
			}
			if _, err := f.store.Get(ctx, 2); !errors.Is(err, ErrNotFound) {
				t.Errorf("user 2 not deleted: %v", err)
			}
		})
	}
}

// waitSubscribed creates users until a directory update arrives. Over HTTP
// the 2026-07-28 Subscribe returns before the listen stream is registered,
// so the first create may race it; retrying keeps the test deterministic
// without sleeping.
func waitSubscribed(t *testing.T, cs *mcp.ClientSession, f *httpFixture, updates <-chan string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for i := 0; ; i++ {
		if _, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: ToolCreateUser, Arguments: map[string]any{"name": "sub-probe"}}); err != nil {
			t.Fatal(err)
		}
		select {
		case uri := <-updates:
			if uri != URIDirectory {
				t.Fatalf("update for %s", uri)
			}
			return
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("no resource update after %d creates", i+1)
		}
	}
}

func TestHTTPAuth(t *testing.T) {
	t.Parallel()
	f := newHTTPFixture(t, true)
	prm := f.base + PathPRM + PathMCP

	t.Run("rejections", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name       string
			token      string
			wantStatus int
		}{
			{"no token", "", http.StatusUnauthorized},
			{"garbage", "not-a-jwt", http.StatusUnauthorized},
			{"wrong audience", f.token(t, "https://other.example/mcp", time.Hour, ScopeRead), http.StatusUnauthorized},
			{"expired", f.token(t, f.url, -time.Minute, ScopeRead), http.StatusUnauthorized},
			{"missing scope", f.token(t, f.url, time.Hour, "profile"), http.StatusForbidden},
			{"forged signature", forge(t, f.url), http.StatusUnauthorized},
		}
		for _, tt := range tests {
			resp := postJSON(t, f.url, tt.token, latest, `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`)
			resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("%s: status %d, want %d", tt.name, resp.StatusCode, tt.wantStatus)
			}
			if wa := resp.Header.Get("WWW-Authenticate"); !strings.Contains(wa, `resource_metadata="`+prm+`"`) {
				t.Errorf("%s: WWW-Authenticate = %q", tt.name, wa)
			}
		}
	})

	t.Run("protected resource metadata", func(t *testing.T) {
		t.Parallel()
		for _, u := range []string{prm, f.base + PathPRM} {
			resp, err := http.Get(u) //nolint:noctx // test helper
			if err != nil {
				t.Fatal(err)
			}
			var md struct {
				Resource             string   `json:"resource"`
				AuthorizationServers []string `json:"authorization_servers"`
				Scopes               []string `json:"scopes_supported"`
			}
			err = json.NewDecoder(resp.Body).Decode(&md)
			resp.Body.Close()
			if err != nil || md.Resource != f.url || len(md.AuthorizationServers) != 1 || len(md.Scopes) != 2 {
				t.Errorf("%s: %+v %v", u, md, err)
			}
		}
	})

	for _, v := range versions {
		t.Run("read-only token/"+v, func(t *testing.T) {
			t.Parallel()
			cs := f.connect(t, v, f.token(t, f.url, time.Hour, ScopeRead), nil)
			res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: ToolCreateUser, Arguments: map[string]any{"name": "Mallory"}})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError || !strings.Contains(resultText(res), "users:write") {
				t.Errorf("want insufficient scope tool error, got %+v", res)
			}
		})
		t.Run("read-write token/"+v, func(t *testing.T) {
			t.Parallel()
			cs := f.connect(t, v, f.token(t, f.url, time.Hour, ScopeRead, ScopeWrite), nil)
			res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: ToolCreateUser, Arguments: map[string]any{"name": "Trent"}})
			if err != nil || res.IsError {
				t.Fatalf("create_user: %v %+v", err, res)
			}
		})
	}
}

func forge(t *testing.T, aud string) string {
	t.Helper()
	tok, err := MintToken(tokens.NewHMAC256([]byte("attacker")), clock.Default, "alice", aud, []string{ScopeRead}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func postJSON(t *testing.T, url, token, version, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	setMCPHeaders(t, req, version, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// setMCPHeaders adds the SEP-2243 headers a 2026-07-28 HTTP request must
// carry: Mcp-Protocol-Version and Mcp-Method mirroring the body.
func setMCPHeaders(t *testing.T, req *http.Request, version, body string) {
	t.Helper()
	if version == "" {
		return
	}
	req.Header.Set("Mcp-Protocol-Version", version)
	if version < latest {
		return
	}
	var msg struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Mcp-Method", msg.Method)
}

const meta2026 = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

// TestHTTPRawProtocol pins wire-level behaviour that the Go client hides.
func TestHTTPRawProtocol(t *testing.T) {
	t.Parallel()
	f := newHTTPFixture(t, false)
	tests := []struct {
		name, version, body string
		wantStatus          int
		wantBody            string
	}{
		{"discover", latest, `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + meta2026 + `}}`, 200, `"supportedVersions":["2026-07-28","2025-11-25","2025-06-18","2025-03-26"]`},
		{"ping removed in 2026-07-28", latest, `{"jsonrpc":"2.0","id":2,"method":"ping","params":{` + meta2026 + `}}`, 404, `-32601`},
		{"cacheable tools/list", latest, `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{` + meta2026 + `}}`, 200, `"ttlMs":60000`},
		{"legacy initialize", "", `{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"raw","version":"0"}}}`, 200, `"protocolVersion":"2025-11-25"`},
		{"unsupported legacy version header", "2024-01-01", `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, 400, "Unsupported protocol version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := postJSON(t, f.url, "", tt.version, tt.body)
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tt.wantStatus || !strings.Contains(string(b), tt.wantBody) {
				t.Errorf("status %d body %s; want %d containing %s", resp.StatusCode, b, tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestHTTPBrowserProtections(t *testing.T) {
	t.Parallel()
	f := newHTTPFixture(t, false)
	body := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + meta2026 + `}}`
	tests := []struct {
		name       string
		mutate     func(*http.Request)
		wantStatus int
	}{
		{"same-origin tool", func(*http.Request) {}, http.StatusOK},
		{"cross-site browser request", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		{"foreign Origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		// DNS rebinding: a loopback listener reached via a non-loopback Host.
		{"rebinding Host", func(r *http.Request) { r.Host = "evil.example" }, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.url, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			setMCPHeaders(t, req, latest, body)
			tt.mutate(req)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestHTTPAuxEndpoints(t *testing.T) {
	t.Parallel()
	f := newHTTPFixture(t, false)
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, PathConsent + "?id=abc": 200, PathConsent: 400} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, f.base+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
	if !f.consent.Completed("abc") {
		t.Error("consent page did not record completion")
	}
}

func TestNewHTTPHandlerValidation(t *testing.T) {
	t.Parallel()
	srv := New(Deps{Store: NewMemoryStore()})
	bad := []HTTPOptions{
		{Auth: &AuthOptions{ResourceURL: "http://x/mcp"}},
		{Auth: &AuthOptions{Verifier: tokens.NewHMAC256([]byte("k")), ResourceURL: "/relative"}},
		{TrustedOrigins: []string{"not an origin"}},
	}
	for i, o := range bad {
		if _, err := NewHTTPHandler(srv, o); err == nil {
			t.Errorf("case %d: want error", i)
		}
	}
}
