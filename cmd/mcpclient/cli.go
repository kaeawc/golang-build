package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cli holds the client-side handlers and the channels the demo uses to
// observe server notifications.
type cli struct {
	out  io.Writer
	in   *bufio.Reader
	yes  bool
	http *http.Client

	mu sync.Mutex // serialises terminal output from concurrent handlers

	updates      chan string
	toolsChanged chan struct{}
	acks         ackLog
	progress     chan *mcp.ProgressNotificationParams
}

func newCLI(out io.Writer, in *bufio.Reader, yes bool, hc *http.Client) *cli {
	return &cli{
		out: out, in: in, yes: yes, http: hc,
		updates:      make(chan string, 32),
		toolsChanged: make(chan struct{}, 8),
		acks:         ackLog{signal: make(chan struct{}, 1)},
		progress:     make(chan *mcp.ProgressNotificationParams, 64),
	}
}

func (c *cli) printf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.out, format, args...)
}

func (c *cli) connect(ctx context.Context, t mcp.Transport, protocol string, logger *slog.Logger) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "golang-build-mcpclient", Title: "golang-build MCP CLI", Version: "v0.1.0"}, &mcp.ClientOptions{
		Logger: logger,
		// Declare both elicitation modes and nothing else: roots and
		// sampling are deprecated by SEP-2577 and not used here.
		Capabilities: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{
			Form: &mcp.FormElicitationCapabilities{},
			URL:  &mcp.URLElicitationCapabilities{},
		}},
		ElicitationHandler: c.elicit,
		ResourceUpdatedHandler: func(_ context.Context, r *mcp.ResourceUpdatedNotificationRequest) {
			nonBlockingSend(c.updates, r.Params.URI)
		},
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			nonBlockingSend(c.toolsChanged, struct{}{})
		},
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			nonBlockingSend(c.progress, r.Params)
		},
	})
	// ClientSession.Subscribe returns before a 2026-07-28 subscriptions/listen
	// stream is registered; watching for the acknowledgement lets the demo
	// wait instead of racing the server.
	client.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if p, ok := req.GetParams().(*mcp.SubscriptionsAcknowledgedParams); ok && p != nil {
				c.acks.record(p.Notifications)
			}
			return next(ctx, method, req)
		}
	})
	var opts *mcp.ClientSessionOptions
	if protocol != "" {
		opts = &mcp.ClientSessionOptions{ProtocolVersion: protocol}
	}
	return client.Connect(ctx, t, opts)
}

// ackLog remembers every subscriptions/acknowledged notification, so a
// waiter can match the ack for its own listen stream even if acks for other
// streams (e.g. the list-changed stream opened by Connect) arrive first.
type ackLog struct {
	mu     sync.Mutex
	seen   []mcp.NotificationSubscriptions
	signal chan struct{}
}

func (l *ackLog) record(n mcp.NotificationSubscriptions) {
	l.mu.Lock()
	l.seen = append(l.seen, n)
	l.mu.Unlock()
	nonBlockingSend(l.signal, struct{}{})
}

func (l *ackLog) wait(ctx context.Context, match func(mcp.NotificationSubscriptions) bool) error {
	timeout := time.After(notifyTimeout)
	for {
		l.mu.Lock()
		found := slices.ContainsFunc(l.seen, match)
		l.mu.Unlock()
		if found {
			return nil
		}
		select {
		case <-l.signal:
		case <-timeout:
			return errors.New("timed out waiting for subscriptions/acknowledged")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func nonBlockingSend[T any](ch chan T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func (c *cli) dispatch(ctx context.Context, cs *mcp.ClientSession, command string, args []string) error {
	switch command {
	case "demo":
		return c.demo(ctx, cs)
	case "list":
		return c.list(ctx, cs)
	case "call":
		return c.callCommand(ctx, cs, args)
	case "read":
		if len(args) != 1 {
			return fmt.Errorf("usage: read <uri>")
		}
		return c.read(ctx, cs, args[0])
	case "prompt":
		return c.promptCommand(ctx, cs, args)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func (c *cli) callCommand(ctx context.Context, cs *mcp.ClientSession, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: call <tool> [json-arguments]")
	}
	var arguments map[string]any
	if len(args) == 2 {
		if err := json.Unmarshal([]byte(args[1]), &arguments); err != nil {
			return fmt.Errorf("arguments must be a JSON object: %w", err)
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: args[0], Arguments: arguments, Meta: mcp.Meta{"progressToken": "cli"}})
	if err != nil {
		return err
	}
	c.drainProgress()
	c.printResult(res)
	return nil
}

func (c *cli) promptCommand(ctx context.Context, cs *mcp.ClientSession, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: prompt <name> [key=value...]")
	}
	params := &mcp.GetPromptParams{Name: args[0], Arguments: map[string]string{}}
	for _, kv := range args[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("prompt argument %q is not key=value", kv)
		}
		params.Arguments[k] = v
	}
	res, err := cs.GetPrompt(ctx, params)
	if err != nil {
		return err
	}
	c.printf("%s\n", res.Description)
	for _, m := range res.Messages {
		c.printf("  [%s] %s\n", m.Role, describeContent(m.Content))
	}
	return nil
}

func (c *cli) read(ctx context.Context, cs *mcp.ClientSession, uri string) error {
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return err
	}
	for _, rc := range res.Contents {
		if rc.Blob != nil {
			c.printf("%s (%s): %d bytes of binary data\n", rc.URI, rc.MIMEType, len(rc.Blob))
			continue
		}
		c.printf("%s (%s):\n%s\n", rc.URI, rc.MIMEType, indent(rc.Text))
	}
	return nil
}

func (c *cli) printResult(res *mcp.CallToolResult) {
	status := "ok"
	if res.IsError {
		status = "tool error"
	}
	c.printf("  -> %s\n", status)
	for _, ct := range res.Content {
		c.printf("     %s\n", describeContent(ct))
	}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		c.printf("     structured: %s\n", truncate(string(b), 160))
	}
}

func (c *cli) drainProgress() {
	for {
		select {
		case p := <-c.progress:
			c.printf("     progress %.0f/%.0f %s\n", p.Progress, p.Total, p.Message)
		default:
			return
		}
	}
}

func (c *cli) discardProgress() {
	for {
		select {
		case <-c.progress:
		default:
			return
		}
	}
}

func describeContent(ct mcp.Content) string {
	switch v := ct.(type) {
	case *mcp.TextContent:
		return "text: " + truncate(v.Text, 120)
	case *mcp.ImageContent:
		return fmt.Sprintf("image: %s, %d bytes", v.MIMEType, len(v.Data))
	case *mcp.AudioContent:
		return fmt.Sprintf("audio: %s, %d bytes", v.MIMEType, len(v.Data))
	case *mcp.ResourceLink:
		return fmt.Sprintf("resource_link: %s (%s)", v.URI, v.Name)
	case *mcp.EmbeddedResource:
		size := len(v.Resource.Text) + len(v.Resource.Blob)
		return fmt.Sprintf("embedded resource: %s (%s, %d bytes)", v.Resource.URI, v.Resource.MIMEType, size)
	default:
		return fmt.Sprintf("%T", ct)
	}
}

// elicit answers form- and URL-mode elicitations. It runs for classic
// server-to-client elicitation/create requests (2025-11-25) and for input
// requests embedded in 2026-07-28 results alike: the SDK routes both here.
func (c *cli) elicit(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	p := req.Params
	c.printf("  ?? server asks (%s mode): %s\n", modeOf(p), p.Message)
	if modeOf(p) == "url" {
		return c.elicitURL(ctx, p)
	}
	if !c.ask("     accept?") {
		return &mcp.ElicitResult{Action: "decline"}, nil
	}
	content, err := c.fillForm(p.RequestedSchema)
	if err != nil {
		return nil, err
	}
	return &mcp.ElicitResult{Action: "accept", Content: content}, nil
}

func modeOf(p *mcp.ElicitParams) string {
	if p.Mode == "url" || (p.Mode == "" && p.URL != "") {
		return "url"
	}
	return "form"
}

func (c *cli) elicitURL(ctx context.Context, p *mcp.ElicitParams) (*mcp.ElicitResult, error) {
	c.printf("     open: %s\n", p.URL)
	if !c.yes {
		if !c.ask("     done in the browser?") {
			return &mcp.ElicitResult{Action: "decline"}, nil
		}
		return &mcp.ElicitResult{Action: "accept"}, nil
	}
	// With -yes, stand in for the user's browser visit. Only loopback
	// http(s) URLs are fetched, so the CLI never makes surprise outbound
	// requests on a server's behalf.
	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid elicitation URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !isLoopbackHost(u.Hostname()) {
		c.printf("     (not fetching non-loopback URL; accepting)\n")
		return &mcp.ElicitResult{Action: "accept"}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("visiting consent URL: %w", err)
	}
	resp.Body.Close()
	c.printf("     visited consent page: %s\n", resp.Status)
	return &mcp.ElicitResult{Action: "accept"}, nil
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func (c *cli) ask(question string) bool {
	if c.yes {
		c.printf("%s [y/N] y (auto)\n", question)
		return true
	}
	c.printf("%s [y/N] ", question)
	line, _ := c.in.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// fillForm collects a value for each property of a flat elicitation schema.
func (c *cli) fillForm(schema any) (map[string]any, error) {
	var s struct {
		Properties map[string]struct {
			Type        string          `json:"type"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Default     json.RawMessage `json:"default"`
		} `json:"properties"`
	}
	if schema != nil {
		b, err := json.Marshal(schema)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	slices.Sort(names)
	content := map[string]any{}
	for _, name := range names {
		prop := s.Properties[name]
		label := cmp.Or(prop.Title, name)
		if prop.Type == "boolean" {
			content[name] = c.ask("     " + label + " (" + prop.Description + ")")
			continue
		}
		val := ""
		if !c.yes {
			c.printf("     %s: ", label)
			val, _ = c.in.ReadString('\n')
			val = strings.TrimSpace(val)
		}
		if v, ok := coerce(prop.Type, val); ok {
			content[name] = v
		}
	}
	return content, nil
}

func coerce(typ, val string) (any, bool) {
	if val == "" {
		return nil, false
	}
	switch typ {
	case "integer":
		n, err := strconv.ParseInt(val, 10, 64)
		return n, err == nil
	case "number":
		f, err := strconv.ParseFloat(val, 64)
		return f, err == nil
	default:
		return val, true
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ")
}
