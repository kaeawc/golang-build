package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/mcpserver"
)

// lockedBuffer is a goroutine-safe io.Writer for capturing CLI output.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestDemoInMemory runs the complete demo against the real server over
// in-memory transports, for both protocol generations. synctest virtualises
// the export delays and notification timeouts.
func TestDemoInMemory(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				server := mcpserver.New(mcpserver.Deps{
					Store:           mcpserver.NewMemoryStore(mcpserver.DemoUsers...),
					ExportStepDelay: 100 * time.Millisecond,
				})
				ct, st := mcp.NewInMemoryTransports()
				ss, err := server.Connect(t.Context(), st, nil)
				if err != nil {
					t.Fatal(err)
				}
				out := &lockedBuffer{}
				c := newCLI(out, bufio.NewReader(strings.NewReader("")), true, http.DefaultClient)
				cs, err := c.connect(t.Context(), ct, v, slog.New(slog.DiscardHandler))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					cs.Close()
					ss.Wait()
				}()
				if err := c.dispatch(t.Context(), cs, "demo", nil); err != nil {
					t.Fatalf("demo: %v\n%s", err, out)
				}
				got := out.String()
				for _, want := range []string{
					"protocol: " + v,
					"All 12 demo steps completed.",
					"resource_link: users://1",
					"image: image/png",
					"audio: audio/wav",
					"notifications/resources/updated: users://directory",
					"progress 13/13",
					"cancelled: context canceled",
					`"deleted":true`,
					`"connected":true`,
					"notifications/tools/list_changed received",
				} {
					if !strings.Contains(got, want) {
						t.Errorf("demo output missing %q", want)
					}
				}
				if t.Failed() {
					t.Logf("output:\n%s", got)
				}
			})
		})
	}
}

// TestRunAgainstHTTP drives run() end to end against an httptest server,
// including -token handling and a URL-mode consent page visit.
func TestRunAgainstHTTP(t *testing.T) {
	t.Parallel()
	consent := mcpserver.NewMemoryConsent(nil)
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	server := mcpserver.New(mcpserver.Deps{
		Store: mcpserver.NewMemoryStore(mcpserver.DemoUsers...), Consent: consent,
		ConsentURL: base + mcpserver.PathConsent, ExportStepDelay: time.Millisecond,
	})
	h, err := mcpserver.NewHTTPHandler(server, mcpserver.HTTPOptions{Consent: consent})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h
	ts.Start()
	defer ts.Close()

	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-url", base + "/mcp", "-yes", "call", "connect_calendar"}, `"connected":true`},
		{[]string{"-url", base + "/mcp", "-protocol", "2025-11-25", "-yes", "call", "delete_user", `{"id":2}`}, `"deleted":true`},
		{[]string{"-url", base + "/mcp", "read", "users://directory"}, "Ada Lovelace"},
		{[]string{"-url", base + "/mcp", "prompt", "onboarding_email", "name=Rob", "tone=formal"}, "[assistant]"},
		{[]string{"-url", base + "/mcp", "list"}, "template  users://{id}"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		if err := run(t.Context(), tt.args, env.Map{}, strings.NewReader(""), &out, io.Discard); err != nil {
			t.Fatalf("%v: %v", tt.args, err)
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Errorf("%v: output missing %q:\n%s", tt.args, tt.want, out.String())
		}
	}
}

func TestInteractiveElicitation(t *testing.T) {
	t.Parallel()
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"confirm": map[string]any{"type": "boolean", "description": "sure?"},
		"reason":  map[string]any{"type": "string"},
		"count":   map[string]any{"type": "integer"},
	}}
	tests := []struct {
		name, input string
		wantAction  string
		wantContent map[string]any
	}{
		{"accept and fill", "y\ny\n3\nspring cleaning\n", "accept", map[string]any{"confirm": true, "count": int64(3), "reason": "spring cleaning"}},
		{"accept but say no", "y\nn\n\n\n", "accept", map[string]any{"confirm": false}},
		{"decline", "n\n", "decline", nil},
		{"eof declines", "", "decline", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newCLI(io.Discard, bufio.NewReader(strings.NewReader(tt.input)), false, http.DefaultClient)
			res, err := c.elicit(t.Context(), &mcp.ElicitRequest{Params: &mcp.ElicitParams{Message: "Delete?", RequestedSchema: schema}})
			if err != nil {
				t.Fatal(err)
			}
			if res.Action != tt.wantAction || len(res.Content) != len(tt.wantContent) {
				t.Fatalf("result = %+v", res)
			}
			for k, v := range tt.wantContent {
				if res.Content[k] != v {
					t.Errorf("content[%s] = %#v, want %#v", k, res.Content[k], v)
				}
			}
		})
	}
}

func TestURLElicitationOnlyFetchesLoopback(t *testing.T) {
	t.Parallel()
	var hits sync.WaitGroup
	hits.Add(1)
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Done() }))
	defer ts.Close()
	c := newCLI(io.Discard, bufio.NewReader(strings.NewReader("")), true, ts.Client())
	for _, u := range []string{ts.URL + "/consent?id=1", "https://calendar.example.com/consent?id=2"} {
		res, err := c.elicit(t.Context(), &mcp.ElicitRequest{Params: &mcp.ElicitParams{Mode: "url", Message: "m", URL: u}})
		if err != nil || res.Action != "accept" {
			t.Fatalf("%s: %+v %v", u, res, err)
		}
	}
	done := make(chan struct{})
	go func() { hits.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loopback consent URL was not visited")
	}
}

func TestParseOptions(t *testing.T) {
	t.Parallel()
	o, err := parseOptions([]string{"-protocol", "2025-11-25", "call", "get_user", `{"id":1}`}, env.Map{"MCP_TOKEN": "tok", "MCP_URL": "http://127.0.0.1:1/mcp"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if o.command != "call" || len(o.args) != 2 || o.url != "http://127.0.0.1:1/mcp" || o.protocol != "2025-11-25" {
		t.Errorf("options = %+v", o)
	}
	if _, ok := o.httpClient.Transport.(bearerTransport); !ok {
		t.Error("MCP_TOKEN did not install the bearer transport")
	}
	if o, _ := parseOptions(nil, env.Map{}, io.Discard); o.command != "demo" {
		t.Errorf("default command = %q", o.command)
	}
}

func TestDispatchErrors(t *testing.T) {
	t.Parallel()
	c := newCLI(io.Discard, bufio.NewReader(strings.NewReader("")), true, http.DefaultClient)
	ctx := context.Background()
	for _, args := range [][]string{{"bogus"}, {"read"}, {"call"}, {"call", "x", "not-json"}, {"prompt"}, {"prompt", "p", "novalue"}} {
		if err := c.dispatch(ctx, nil, args[0], args[1:]); err == nil {
			t.Errorf("dispatch %v: want error", args)
		}
	}
}
