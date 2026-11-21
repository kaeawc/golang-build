package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	latest = "2026-07-28"
	legacy = "2025-11-25"
)

// versions is the protocol matrix most tests run against: the stateless
// 2026-07-28 revision and the last handshake-based revision.
var versions = []string{latest, legacy}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	server *mcp.Server
	cs     *mcp.ClientSession
	store  *MemoryStore
	logs   *syncBuffer
	// acks receives one value per notifications/subscriptions/acknowledged,
	// letting tests wait until a 2026-07-28 subscriptions/listen stream is
	// live (ClientSession.Subscribe returns before the server registers it).
	acks chan struct{}
}

type harnessOpt func(*Deps)

// newHarness wires New(deps) to a real client over in-memory transports at
// the given protocol version.
func newHarness(t testing.TB, version string, copts *mcp.ClientOptions, opts ...harnessOpt) *harness {
	t.Helper()
	logs := &syncBuffer{}
	h := &harness{store: NewMemoryStore(DemoUsers...), logs: logs, acks: make(chan struct{}, 16)}
	deps := Deps{
		Store:           h.store,
		Logger:          slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		ExportStepDelay: time.Millisecond,
		PageSize:        3, // small, so list pagination is exercised
		Version:         "v1.2.3-test",
	}
	for _, o := range opts {
		o(&deps)
	}
	h.server = New(deps)

	if copts == nil {
		copts = &mcp.ClientOptions{}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, copts)
	client.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "notifications/subscriptions/acknowledged" {
				select {
				case h.acks <- struct{}{}:
				default:
				}
			}
			return next(ctx, method, req)
		}
	})

	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := h.server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := client.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	h.cs = cs
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})
	return h
}

// waitAck blocks until a subscriptions/listen stream is acknowledged. It is
// a no-op for legacy sessions, which subscribe synchronously.
func (h *harness) waitAck(t testing.TB, version string) {
	t.Helper()
	if version < latest {
		return
	}
	select {
	case <-h.acks:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for subscriptions/acknowledged")
	}
}

func (h *harness) call(t testing.TB, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := h.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

// structured decodes a tool result's structured content into T.
func structured[T any](t testing.TB, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode structured content %s: %v", b, err)
	}
	return out
}

func resultText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func waitFor[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}
