package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/tokens"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		env     env.Map
		wantErr string
		check   func(t *testing.T, c config)
	}{
		{name: "defaults", check: func(t *testing.T, c config) {
			if c.transport != "stdio" || c.addr != "127.0.0.1:8081" || c.backend != "memory" || c.resourceURL != "http://127.0.0.1:8081/mcp" {
				t.Errorf("config = %+v", c)
			}
		}},
		{name: "env then flags", args: []string{"-addr", "127.0.0.1:9000"}, env: env.Map{"MCP_TRANSPORT": "http", "MCP_ADDR": "127.0.0.1:1"}, check: func(t *testing.T, c config) {
			if c.transport != "http" || c.addr != "127.0.0.1:9000" {
				t.Errorf("config = %+v", c)
			}
		}},
		{name: "public bind needs auth", args: []string{"-transport=http", "-addr=0.0.0.0:8081"}, wantErr: "refusing"},
		{name: "public bind with auth", args: []string{"-transport=http", "-addr=0.0.0.0:8081", "-resource-url=https://mcp.example.com/mcp"}, env: env.Map{"MCP_AUTH_SECRET": "s"}},
		{name: "public bind override", args: []string{"-transport=http", "-addr=:8081", "-insecure-bind"}},
		{name: "bad transport", args: []string{"-transport=sse"}, wantErr: "-transport"},
		{name: "postgres needs dsn", args: []string{"-backend=postgres"}, wantErr: "DATABASE_URL"},
		{name: "mint needs secret", args: []string{"-mint-token"}, wantErr: "MCP_AUTH_SECRET"},
		{name: "bad log level", args: []string{"-log-level=loud"}, wantErr: "log-level"},
		{name: "lists", env: env.Map{"MCP_TRUSTED_ORIGINS": " http://a , ,http://b"}, check: func(t *testing.T, c config) {
			if len(c.trustedOrigins) != 2 || c.trustedOrigins[1] != "http://b" {
				t.Errorf("origins = %q", c.trustedOrigins)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.env == nil {
				tt.env = env.Map{}
			}
			c, err := parseConfig(tt.args, tt.env, io.Discard)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}

func TestMintToken(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := run(t.Context(), []string{"-mint-token", "-token-subject=bob", "-token-scopes=users:read"}, env.Map{"MCP_AUTH_SECRET": "secret"}, &out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Sub   string `json:"sub"`
		Aud   string `json:"aud"`
		Scope string `json:"scope"`
	}
	if err := tokens.NewHMAC256([]byte("secret")).Verify(strings.TrimSpace(out.String()), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Sub != "bob" || claims.Aud != "http://127.0.0.1:8081/mcp" || claims.Scope != "users:read" {
		t.Errorf("claims = %+v", claims)
	}
}

// TestServeHTTPLifecycle runs the real HTTP serving path, talks to it with
// both protocol generations, then cancels the context and expects a clean,
// bounded shutdown even with long-lived subscription streams open.
func TestServeHTTPLifecycle(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	cfg, err := parseConfig([]string{"-transport=http", "-addr=" + addr, "-shutdown-timeout=2s"}, env.Map{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	// The listener is already bound, so clients can connect immediately.
	go func() { done <- serve(ctx, cfg, slog.New(slog.DiscardHandler), ln) }()

	endpoint := "http://" + addr + "/mcp"
	for _, v := range []string{"2026-07-28", "2025-11-25"} {
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).
			Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint}, &mcp.ClientSessionOptions{ProtocolVersion: v})
		if err != nil {
			t.Fatalf("%s connect: %v", v, err)
		}
		defer cs.Close()
		if got := cs.InitializeResult().ProtocolVersion; got != v {
			t.Errorf("negotiated %s, want %s", got, v)
		}
		if err := cs.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "users://directory"}); err != nil {
			t.Fatal(err)
		}
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/readyz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz = %d", resp.StatusCode)
	}

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("shutdown took %v", d)
	}
}
