// Command mcpclient is a plain command-line MCP client for cmd/mcp. It
// demonstrates discovery, paginated listing, tool calls, resources,
// subscriptions, prompts, completion, progress, cancellation, and the
// multi round-trip (elicitation) flow, over stdio or streamable HTTP.
//
//	mcpclient                                  # spawn ./mcp (or `go run ./cmd/mcp`) over stdio, run the demo
//	mcpclient -url http://127.0.0.1:8081/mcp   # talk to a running HTTP server
//	mcpclient -protocol 2025-11-25 demo        # pin the legacy protocol
//	mcpclient call get_user '{"id":1}'
//	mcpclient read users://directory
//	mcpclient prompt summarize_user user_id=1 style=brief
//	mcpclient list
//
// Elicitations are answered interactively on the terminal, or
// automatically with -yes.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/env"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], env.OS{}, os.Stdin, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpclient:", err)
		os.Exit(1)
	}
}

type options struct {
	url        string
	server     string
	protocol   string
	token      string
	yes        bool
	verbose    bool
	timeout    time.Duration
	command    string
	args       []string
	httpClient *http.Client
}

func parseOptions(args []string, r env.Reader, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("mcpclient", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: mcpclient [flags] [demo | list | call <tool> [json] | read <uri> | prompt <name> [k=v...]]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.StringVar(&o.url, "url", env.GetTrim(r, "MCP_URL"), "streamable HTTP endpoint; empty means spawn a stdio server")
	fs.StringVar(&o.server, "server", env.GetTrim(r, "MCP_SERVER_CMD"), "stdio server command (default: ./mcp next to this binary, else `go run ./cmd/mcp`)")
	fs.StringVar(&o.protocol, "protocol", "", "pin a protocol version, e.g. 2025-11-25 (default: newest, 2026-07-28)")
	fs.StringVar(&o.token, "token", env.Get(r, "MCP_TOKEN"), "bearer token for HTTP (or MCP_TOKEN)")
	fs.BoolVar(&o.yes, "yes", false, "auto-accept elicitations (and visit URL-mode consent pages)")
	fs.BoolVar(&o.verbose, "v", false, "show client SDK and stdio server logs on stderr")
	fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "overall deadline")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	o.command, o.args = "demo", fs.Args()
	if len(o.args) > 0 {
		o.command, o.args = o.args[0], o.args[1:]
	}
	o.httpClient = &http.Client{}
	if o.token != "" {
		o.httpClient.Transport = bearerTransport{token: o.token, base: http.DefaultTransport}
	}
	return o, nil
}

func run(ctx context.Context, args []string, r env.Reader, stdin io.Reader, stdout, stderr io.Writer) error {
	o, err := parseOptions(args, r, stderr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	transport, err := o.transport(ctx, stderr)
	if err != nil {
		return err
	}
	c := newCLI(stdout, bufio.NewReader(stdin), o.yes, o.httpClient)
	logger := slog.New(slog.DiscardHandler)
	if o.verbose {
		logger = slog.New(slog.NewTextHandler(stderr, nil))
	}
	cs, err := c.connect(ctx, transport, o.protocol, logger)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer cs.Close()
	return c.dispatch(ctx, cs, o.command, o.args)
}

func (o options) transport(ctx context.Context, stderr io.Writer) (mcp.Transport, error) {
	if o.url != "" {
		return &mcp.StreamableClientTransport{Endpoint: o.url, HTTPClient: o.httpClient}, nil
	}
	argv, err := serverCommand(o.server)
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- the command is operator-supplied (flag/env), like an MCP host's server config; never remote input
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], "-transport=stdio")...)
	cmd.Stderr = io.Discard
	if o.verbose {
		cmd.Stderr = stderr
	}
	return &mcp.CommandTransport{Command: cmd}, nil
}

func serverCommand(flagValue string) ([]string, error) {
	if f := strings.Fields(flagValue); len(f) > 0 {
		return f, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "mcp")
		if st, err := os.Stat(sibling); err == nil && !st.IsDir() {
			return []string{sibling}, nil
		}
	}
	if _, err := exec.LookPath("go"); err == nil {
		return []string{"go", "run", "./cmd/mcp"}, nil
	}
	return nil, errors.New("no stdio server found: build ./cmd/mcp next to mcpclient, pass -server, or use -url")
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}
