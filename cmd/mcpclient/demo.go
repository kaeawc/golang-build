package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const notifyTimeout = 5 * time.Second

type step struct {
	title string
	run   func(context.Context, *mcp.ClientSession) error
}

// demo walks through every feature the server implements. Each step is
// independent so a failure names exactly which feature broke.
func (c *cli) demo(ctx context.Context, cs *mcp.ClientSession) error {
	steps := []step{
		{"Discovery", c.stepDiscovery},
		{"Tools (paginated listing)", c.stepListTools},
		{"Tool calls and content types", c.stepCallTools},
		{"Tool execution errors vs protocol errors", c.stepErrors},
		{"Resources and templates", c.stepResources},
		{"Subscriptions", c.stepSubscribe},
		{"Prompts and completion", c.stepPrompts},
		{"Progress", c.stepProgress},
		{"Cancellation", c.stepCancel},
		{"Multi round-trip: form elicitation (delete_user)", c.stepDelete},
		{"Multi round-trip: URL elicitation (connect_calendar)", c.stepCalendar},
		{"List changed at runtime", c.stepListChanged},
	}
	for i, s := range steps {
		c.printf("\n== %d. %s ==\n", i+1, s.title)
		if err := s.run(ctx, cs); err != nil {
			return fmt.Errorf("step %q: %w", s.title, err)
		}
	}
	c.printf("\nAll %d demo steps completed.\n", len(steps))
	return nil
}

func (c *cli) stepDiscovery(ctx context.Context, cs *mcp.ClientSession) error {
	init := cs.InitializeResult()
	mode := "no session ID"
	if id := cs.ID(); id != "" {
		mode = "session " + truncate(id, 12)
	}
	c.printf("protocol: %s, %s\n", init.ProtocolVersion, mode)
	if si := init.ServerInfo; si != nil {
		c.printf("server:   %s %q %s (%d icon(s))\n", si.Name, si.Title, si.Version, len(si.Icons))
	}
	caps := init.Capabilities
	c.printf("caps:     tools=%t prompts=%t resources(subscribe=%t) completions=%t\n",
		caps.Tools != nil, caps.Prompts != nil, caps.Resources != nil && caps.Resources.Subscribe, caps.Completions != nil)
	c.printf("instructions: %s\n", truncate(init.Instructions, 100))
	if init.ProtocolVersion < "2026-07-28" {
		if err := cs.Ping(ctx, nil); err != nil {
			return err
		}
		c.printf("ping:     ok (legacy session)\n")
	}
	return nil
}

func (c *cli) stepListTools(ctx context.Context, cs *mcp.ClientSession) error {
	params := &mcp.ListToolsParams{}
	for page := 1; ; page++ {
		res, err := cs.ListTools(ctx, params)
		if err != nil {
			return err
		}
		c.printf("page %d (ttlMs=%d cacheScope=%q):\n", page, res.TTLMs, res.CacheScope)
		for _, t := range res.Tools {
			c.printf("  %-26s %s%s\n", t.Name, t.Title, hints(t.Annotations))
		}
		if res.NextCursor == "" {
			return nil
		}
		params = &mcp.ListToolsParams{Cursor: res.NextCursor}
	}
}

func hints(a *mcp.ToolAnnotations) string {
	if a == nil {
		return ""
	}
	var h []string
	if a.ReadOnlyHint {
		h = append(h, "read-only")
	}
	if a.DestructiveHint != nil && *a.DestructiveHint {
		h = append(h, "destructive")
	}
	if a.IdempotentHint {
		h = append(h, "idempotent")
	}
	if a.OpenWorldHint != nil && *a.OpenWorldHint {
		h = append(h, "open-world")
	}
	if len(h) == 0 {
		return ""
	}
	return " [" + strings.Join(h, ", ") + "]"
}

func (c *cli) call(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, error) {
	c.printf("call %s %v\n", name, args)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	c.printResult(res)
	return res, nil
}

func (c *cli) stepCallTools(ctx context.Context, cs *mcp.ClientSession) error {
	res, err := c.call(ctx, cs, "list_users", map[string]any{"limit": 5})
	if err != nil {
		return err
	}
	if next, _ := res.StructuredContent.(map[string]any)["nextCursor"].(string); next != "" {
		if _, err := c.call(ctx, cs, "list_users", map[string]any{"limit": 5, "cursor": next}); err != nil {
			return err
		}
	}
	for _, name := range []string{"get_user", "user_card"} {
		if _, err := c.call(ctx, cs, name, map[string]any{"id": 1}); err != nil {
			return err
		}
	}
	_, err = c.call(ctx, cs, "health_check", nil)
	return err
}

func (c *cli) stepErrors(ctx context.Context, cs *mcp.ClientSession) error {
	res, err := c.call(ctx, cs, "get_user", map[string]any{"id": 999})
	if err != nil {
		return err
	}
	if !res.IsError {
		return errors.New("expected an isError result for a missing user")
	}
	_, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "no_such_tool"})
	c.printf("call no_such_tool\n  -> protocol error: %v\n", err)
	if err == nil {
		return errors.New("expected a JSON-RPC error for an unknown tool")
	}
	return nil
}

func (c *cli) stepResources(ctx context.Context, cs *mcp.ClientSession) error {
	for r, err := range cs.Resources(ctx, nil) {
		if err != nil {
			return err
		}
		c.printf("resource  %-22s %s\n", r.URI, r.MIMEType)
	}
	for t, err := range cs.ResourceTemplates(ctx, nil) {
		if err != nil {
			return err
		}
		c.printf("template  %-22s %s\n", t.URITemplate, t.MIMEType)
	}
	for _, uri := range []string{"server://info", "assets://logo.png", "users://3", "users://3/avatar.png"} {
		if err := c.read(ctx, cs, uri); err != nil {
			return err
		}
	}
	return nil
}

func (c *cli) stepSubscribe(ctx context.Context, cs *mcp.ClientSession) error {
	if err := cs.Subscribe(ctx, &mcp.SubscribeParams{URI: "users://directory"}); err != nil {
		return err
	}
	if cs.InitializeResult().ProtocolVersion >= "2026-07-28" {
		err := c.acks.wait(ctx, func(n mcp.NotificationSubscriptions) bool {
			return slices.Contains(n.ResourceSubscriptions, "users://directory")
		})
		if err != nil {
			return err
		}
		c.printf("subscriptions/listen acknowledged for users://directory\n")
	} else {
		c.printf("resources/subscribe users://directory ok\n")
	}
	if _, err := c.call(ctx, cs, "create_user", map[string]any{"name": "Demo User"}); err != nil {
		return err
	}
	select {
	case uri := <-c.updates:
		c.printf("notifications/resources/updated: %s\n", uri)
	case <-time.After(notifyTimeout):
		return errors.New("no resources/updated notification")
	}
	return c.read(ctx, cs, "users://directory")
}

func (c *cli) stepPrompts(ctx context.Context, cs *mcp.ClientSession) error {
	for p, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			return err
		}
		var args []string
		for _, a := range p.Arguments {
			args = append(args, a.Name)
		}
		c.printf("prompt %-18s args=%v\n", p.Name, args)
	}
	completions := []*mcp.CompleteParams{
		{Ref: &mcp.CompleteReference{Type: "ref/prompt", Name: "summarize_user"}, Argument: mcp.CompleteParamsArgument{Name: "user_id", Value: "1"}},
		{Ref: &mcp.CompleteReference{Type: "ref/prompt", Name: "summarize_user"}, Argument: mcp.CompleteParamsArgument{Name: "style", Value: "b"}},
		{Ref: &mcp.CompleteReference{Type: "ref/resource", URI: "users://{id}"}, Argument: mcp.CompleteParamsArgument{Name: "id", Value: "1"}},
	}
	for _, p := range completions {
		res, err := cs.Complete(ctx, p)
		if err != nil {
			return err
		}
		c.printf("complete %s%s %s=%q -> %v\n", p.Ref.Name, p.Ref.URI, p.Argument.Name, p.Argument.Value, res.Completion.Values)
	}
	return c.promptCommand(ctx, cs, []string{"summarize_user", "user_id=1", "style=brief"})
}

func (c *cli) stepProgress(ctx context.Context, cs *mcp.ClientSession) error {
	c.printf("call export_users (with progress token)\n")
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "export_users", Arguments: map[string]any{"batchSize": 4},
		Meta: mcp.Meta{"progressToken": "demo-export"},
	})
	if err != nil {
		return err
	}
	// Notification handlers run concurrently with response delivery, so the
	// final progress update can land after CallTool returns.
	if err := c.awaitFinalProgress(ctx); err != nil {
		return err
	}
	c.printResult(res)
	return nil
}

// awaitFinalProgress prints progress notifications until one reports
// progress == total.
func (c *cli) awaitFinalProgress(ctx context.Context) error {
	for {
		select {
		case p := <-c.progress:
			c.printf("     progress %.0f/%.0f %s\n", p.Progress, p.Total, p.Message)
			if p.Total > 0 && p.Progress >= p.Total {
				return nil
			}
		case <-time.After(notifyTimeout):
			return errors.New("final progress notification never arrived")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *cli) stepCancel(ctx context.Context, cs *mcp.ClientSession) error {
	c.discardProgress() // late notifications from the previous step
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case p := <-c.progress:
			c.printf("     progress %.0f/%.0f — cancelling\n", p.Progress, p.Total)
			cancel()
		case <-callCtx.Done():
		}
	}()
	c.printf("call export_users (cancel after first progress)\n")
	_, err := cs.CallTool(callCtx, &mcp.CallToolParams{
		Name: "export_users", Arguments: map[string]any{"batchSize": 1},
		Meta: mcp.Meta{"progressToken": "demo-cancel"},
	})
	if !errors.Is(err, context.Canceled) {
		return fmt.Errorf("expected cancellation, got %w", err)
	}
	c.printf("  -> cancelled: %v (notifications/cancelled sent)\n", err)
	return nil
}

func (c *cli) stepDelete(ctx context.Context, cs *mcp.ClientSession) error {
	id, err := c.createScratchUser(ctx, cs)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, cs, "delete_user", map[string]any{"id": id})
	return err
}

func (c *cli) createScratchUser(ctx context.Context, cs *mcp.ClientSession) (float64, error) {
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "create_user", Arguments: map[string]any{"name": "Scratch User"}})
	if err != nil {
		return 0, err
	}
	id, ok := res.StructuredContent.(map[string]any)["id"].(float64)
	if !ok || res.IsError {
		return 0, fmt.Errorf("create_user failed: %+v", res)
	}
	return id, nil
}

func (c *cli) stepCalendar(ctx context.Context, cs *mcp.ClientSession) error {
	_, err := c.call(ctx, cs, "connect_calendar", map[string]any{"provider": "work"})
	return err
}

func (c *cli) stepListChanged(ctx context.Context, cs *mcp.ClientSession) error {
	if cs.InitializeResult().ProtocolVersion >= "2026-07-28" {
		// Connect opened a list-changed subscriptions/listen stream; make
		// sure it is live before mutating the tool list.
		err := c.acks.wait(ctx, func(n mcp.NotificationSubscriptions) bool { return n.ToolsListChanged })
		if err != nil {
			return err
		}
	}
	if _, err := c.call(ctx, cs, "set_experimental_features", map[string]any{"enabled": true}); err != nil {
		return err
	}
	select {
	case <-c.toolsChanged:
		c.printf("notifications/tools/list_changed received\n")
	case <-time.After(notifyTimeout):
		return errors.New("no tools/list_changed notification")
	}
	if _, err := c.call(ctx, cs, "generate_report", nil); err != nil {
		return err
	}
	_, err := c.call(ctx, cs, "set_experimental_features", map[string]any{"enabled": false})
	return err
}

func (c *cli) list(ctx context.Context, cs *mcp.ClientSession) error {
	if err := c.stepListTools(ctx, cs); err != nil {
		return err
	}
	if err := c.stepResourcesList(ctx, cs); err != nil {
		return err
	}
	for p, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			return err
		}
		c.printf("prompt    %s\n", p.Name)
	}
	return nil
}

func (c *cli) stepResourcesList(ctx context.Context, cs *mcp.ClientSession) error {
	for r, err := range cs.Resources(ctx, nil) {
		if err != nil {
			return err
		}
		c.printf("resource  %s\n", r.URI)
	}
	for t, err := range cs.ResourceTemplates(ctx, nil) {
		if err != nil {
			return err
		}
		c.printf("template  %s\n", t.URITemplate)
	}
	return nil
}
