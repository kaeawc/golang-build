package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDiscoveryAndLifecycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		requested string
		want      string
	}{
		{"stateless discover", latest, latest},
		{"legacy initialize", legacy, legacy},
		{"older legacy", "2025-06-18", "2025-06-18"},
		// 2024-11-05 is excluded by SupportedProtocolVersions; the legacy
		// handshake answers with the newest version it can negotiate.
		{"excluded version is renegotiated", "2024-11-05", legacy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tt.requested, nil)
			init := h.cs.InitializeResult()
			if init.ProtocolVersion != tt.want {
				t.Fatalf("negotiated %q, want %q", init.ProtocolVersion, tt.want)
			}
			if !strings.Contains(init.Instructions, "list_users") {
				t.Errorf("instructions missing: %q", init.Instructions)
			}
			if si := init.ServerInfo; si == nil || si.Name != ServerName || si.Version != "v1.2.3-test" || si.Title == "" || len(si.Icons) == 0 {
				t.Errorf("server info = %+v", si)
			}
			caps := init.Capabilities
			if caps.Tools == nil || !caps.Tools.ListChanged {
				t.Error("tools.listChanged not advertised")
			}
			if caps.Resources == nil || !caps.Resources.Subscribe || !caps.Resources.ListChanged {
				t.Error("resources.subscribe/listChanged not advertised")
			}
			if caps.Prompts == nil || caps.Completions == nil {
				t.Error("prompts/completions not advertised")
			}
			if b, _ := json.Marshal(caps); bytes.Contains(b, []byte(`"logging"`)) {
				t.Errorf("deprecated logging capability advertised: %s", b)
			}
		})
	}
}

// TestLegacyPing: ping was removed in 2026-07-28 (SEP-2575) but must keep
// working for legacy sessions. The 2026-07-28 rejection is asserted with a
// raw request in TestHTTPRawProtocol, because ClientSession.Ping sends no
// per-request _meta and is therefore treated as a legacy request.
func TestLegacyPing(t *testing.T) {
	t.Parallel()
	h := newHarness(t, legacy, nil)
	if err := h.cs.Ping(t.Context(), nil); err != nil {
		t.Fatalf("legacy ping: %v", err)
	}
}

func TestToolListing(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, v, nil)

			first, err := h.cs.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Tools) != 3 || first.NextCursor == "" {
				t.Fatalf("page size 3 not honoured: %d tools, cursor %q", len(first.Tools), first.NextCursor)
			}

			tools := map[string]*mcp.Tool{}
			for tool, err := range h.cs.Tools(t.Context(), nil) {
				if err != nil {
					t.Fatal(err)
				}
				tools[tool.Name] = tool
			}
			for _, name := range []string{ToolListUsers, ToolGetUser, ToolCreateUser, ToolDeleteUser, ToolUserCard, ToolExportUsers, ToolHealthCheck, ToolConnectCalendar, ToolSetExperimental} {
				tool, ok := tools[name]
				if !ok {
					t.Errorf("missing tool %s", name)
					continue
				}
				if tool.OutputSchema == nil || tool.Title == "" || tool.Annotations == nil {
					t.Errorf("%s: want output schema, title, and annotations", name)
				}
			}
			if _, ok := tools[ToolGenerateReport]; ok {
				t.Error("experimental tool listed before being enabled")
			}

			del := tools[ToolDeleteUser].Annotations
			if del.DestructiveHint == nil || !*del.DestructiveHint || del.ReadOnlyHint {
				t.Errorf("delete_user annotations = %+v", del)
			}
			list := tools[ToolListUsers]
			if !list.Annotations.ReadOnlyHint || len(list.Icons) == 0 {
				t.Errorf("list_users annotations/icons = %+v %v", list.Annotations, list.Icons)
			}
			schema, _ := json.Marshal(list.InputSchema)
			if !bytes.Contains(schema, []byte("opaque cursor")) {
				t.Errorf("jsonschema tag description missing from input schema: %s", schema)
			}
		})
	}
}

func TestToolCalls(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tool    string
		args    map[string]any
		wantErr bool // IsError tool result (not a protocol error)
		check   func(t *testing.T, res *mcp.CallToolResult)
	}{
		{name: "list first page", tool: ToolListUsers, args: map[string]any{"limit": 2}, check: func(t *testing.T, res *mcp.CallToolResult) {
			out := structured[ListUsersOutput](t, res)
			if len(out.Users) != 2 || out.NextCursor == "" {
				t.Fatalf("got %+v", out)
			}
			links := 0
			for _, c := range res.Content {
				if _, ok := c.(*mcp.ResourceLink); ok {
					links++
				}
			}
			if links != 2 {
				t.Errorf("want 2 resource_link blocks, got %d", links)
			}
		}},
		{name: "list bad cursor", tool: ToolListUsers, args: map[string]any{"cursor": "!!"}, wantErr: true},
		{name: "list limit out of range", tool: ToolListUsers, args: map[string]any{"limit": 999}, wantErr: true},
		{name: "get user embeds resource", tool: ToolGetUser, args: map[string]any{"id": 1}, check: func(t *testing.T, res *mcp.CallToolResult) {
			if u := structured[User](t, res); u.Name != "Ada Lovelace" {
				t.Errorf("user = %+v", u)
			}
			er, ok := res.Content[0].(*mcp.EmbeddedResource)
			if !ok || er.Resource.URI != "users://1" || er.Resource.MIMEType != "application/json" {
				t.Errorf("content[0] = %#v", res.Content[0])
			}
		}},
		{name: "get missing user", tool: ToolGetUser, args: map[string]any{"id": 999}, wantErr: true, check: func(t *testing.T, res *mcp.CallToolResult) {
			if !strings.Contains(resultText(res), "not found") {
				t.Errorf("text = %q", resultText(res))
			}
		}},
		{name: "schema validation", tool: ToolGetUser, args: map[string]any{"id": "one"}, wantErr: true},
		{name: "user card media", tool: ToolUserCard, args: map[string]any{"id": 2}, check: func(t *testing.T, res *mcp.CallToolResult) {
			var img, audio, link bool
			for _, c := range res.Content {
				switch c := c.(type) {
				case *mcp.ImageContent:
					img = c.MIMEType == "image/png" && bytes.HasPrefix(c.Data, []byte("\x89PNG"))
				case *mcp.AudioContent:
					audio = c.MIMEType == "audio/wav" && bytes.HasPrefix(c.Data, []byte("RIFF"))
				case *mcp.ResourceLink:
					link = c.URI == "users://2"
				}
			}
			if !img || !audio || !link {
				t.Errorf("image=%t audio=%t link=%t", img, audio, link)
			}
		}},
		{name: "create user", tool: ToolCreateUser, args: map[string]any{"name": "  Linus Torvalds "}, check: func(t *testing.T, res *mcp.CallToolResult) {
			if u := structured[User](t, res); u.ID != 13 || u.Name != "Linus Torvalds" {
				t.Errorf("user = %+v", u)
			}
		}},
		{name: "create invalid", tool: ToolCreateUser, args: map[string]any{"name": " "}, wantErr: true},
		{name: "health", tool: ToolHealthCheck, check: func(t *testing.T, res *mcp.CallToolResult) {
			if out := structured[HealthOutput](t, res); out.Status != "ok" {
				t.Errorf("health = %+v", out)
			}
		}},
		{name: "export without progress token", tool: ToolExportUsers, args: map[string]any{"batchSize": 4}, check: func(t *testing.T, res *mcp.CallToolResult) {
			if out := structured[ExportOutput](t, res); out.Exported != 12 || out.Batches != 3 {
				t.Errorf("export = %+v", out)
			}
			er, ok := res.Content[1].(*mcp.EmbeddedResource)
			if !ok || !strings.HasPrefix(er.Resource.Text, "id,name\n1,") {
				t.Errorf("csv = %#v", res.Content[1])
			}
		}},
	}
	for _, v := range versions {
		for _, tt := range tests {
			t.Run(v+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, v, nil)
				res := h.call(t, tt.tool, tt.args)
				if res.IsError != tt.wantErr {
					t.Fatalf("IsError = %t, want %t (%s)", res.IsError, tt.wantErr, resultText(res))
				}
				if tt.check != nil {
					tt.check(t, res)
				}
			})
		}
	}
}

func TestListUsersPagination(t *testing.T) {
	t.Parallel()
	h := newHarness(t, latest, nil)
	var ids []int64
	cursor := ""
	for range 10 {
		out := structured[ListUsersOutput](t, h.call(t, ToolListUsers, map[string]any{"cursor": cursor, "limit": 5}))
		for _, u := range out.Users {
			ids = append(ids, u.ID)
		}
		if cursor = out.NextCursor; cursor == "" {
			break
		}
	}
	if len(ids) != len(DemoUsers) || !slices.IsSorted(ids) {
		t.Fatalf("paged ids = %v", ids)
	}
}

func TestUnknownToolIsProtocolError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, latest, nil)
	if _, err := h.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "nope"}); err == nil {
		t.Fatal("unknown tool should be a JSON-RPC error")
	}
	if logs := h.logs.String(); !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "tool=nope") {
		t.Errorf("logging middleware did not record the failure:\n%s", logs)
	}
}

func TestLoggingMiddleware(t *testing.T) {
	t.Parallel()
	h := newHarness(t, latest, nil)
	h.call(t, ToolGetUser, map[string]any{"id": 1})
	logs := h.logs.String()
	for _, want := range []string{"msg=\"mcp request\"", "method=tools/call", "tool=get_user", "is_error=false", "duration=", "protocol=2026-07-28"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestResources(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, v, nil)
			ctx := t.Context()

			var uris, templates []string
			for r, err := range h.cs.Resources(ctx, nil) {
				if err != nil {
					t.Fatal(err)
				}
				uris = append(uris, r.URI)
			}
			for rt, err := range h.cs.ResourceTemplates(ctx, nil) {
				if err != nil {
					t.Fatal(err)
				}
				templates = append(templates, rt.URITemplate)
			}
			slices.Sort(uris)
			slices.Sort(templates)
			if !slices.Equal(uris, []string{URILogo, URIServerInfo, URIDirectory}) {
				t.Errorf("resources = %v", uris)
			}
			if !slices.Equal(templates, []string{URITemplateUser, URITemplateAvatar}) {
				t.Errorf("templates = %v", templates)
			}

			reads := []struct {
				uri, mime string
				blob      bool
				contains  string
			}{
				{URIDirectory, "text/markdown", false, "[Grace Hopper](users://7)"},
				{URIServerInfo, "application/json", false, `"2026-07-28"`},
				{URILogo, "image/png", true, "\x89PNG"},
				{"users://3", "application/json", false, `"Barbara Liskov"`},
				{"users://3/avatar.png", "image/png", true, "\x89PNG"},
			}
			for _, r := range reads {
				res, err := h.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: r.uri})
				if err != nil {
					t.Fatalf("read %s: %v", r.uri, err)
				}
				c := res.Contents[0]
				got := c.Text
				if r.blob {
					got = string(c.Blob)
				}
				if c.MIMEType != r.mime || (r.blob != (c.Text == "")) || !strings.Contains(got, r.contains) {
					t.Errorf("read %s: mime=%q text=%q blob=%d bytes", r.uri, c.MIMEType, c.Text, len(c.Blob))
				}
			}

			for _, bad := range []string{"users://999", "users://01", "users://abc", "nope://x"} {
				if _, err := h.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: bad}); err == nil {
					t.Errorf("read %s: want error", bad)
				}
			}
		})
	}
}

func TestCacheableResults(t *testing.T) {
	t.Parallel()
	h := newHarness(t, latest, nil)
	ctx := t.Context()
	tools, err := h.cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tools.TTLMs != 60_000 || tools.CacheScope != "public" {
		t.Errorf("tools/list cacheable = %+v", tools.Cacheable)
	}
	info, err := h.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: URIServerInfo})
	if err != nil {
		t.Fatal(err)
	}
	if info.TTLMs != 300_000 || info.CacheScope != "public" {
		t.Errorf("server://info cacheable = %+v", info.Cacheable)
	}
	user, err := h.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "users://1"})
	if err != nil {
		t.Fatal(err)
	}
	if user.TTLMs != 0 || user.CacheScope != "private" {
		t.Errorf("users://1 cacheable = %+v", user.Cacheable)
	}
}

func TestPrompts(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, v, nil)
			ctx := t.Context()

			var names []string
			for p, err := range h.cs.Prompts(ctx, nil) {
				if err != nil {
					t.Fatal(err)
				}
				names = append(names, p.Name)
				if len(p.Arguments) != 2 || !p.Arguments[0].Required {
					t.Errorf("%s arguments = %+v", p.Name, p.Arguments)
				}
			}
			slices.Sort(names)
			if !slices.Equal(names, []string{PromptOnboarding, PromptSummarizeUser}) {
				t.Fatalf("prompts = %v", names)
			}

			sum, err := h.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: PromptSummarizeUser, Arguments: map[string]string{"user_id": "4", "style": "detailed"}})
			if err != nil {
				t.Fatal(err)
			}
			er, ok := sum.Messages[0].Content.(*mcp.EmbeddedResource)
			if len(sum.Messages) != 2 || !ok || er.Resource.URI != "users://4" || !strings.Contains(er.Resource.Text, "Donald Knuth") {
				t.Errorf("summarize_user messages = %+v", sum.Messages)
			}

			mail, err := h.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: PromptOnboarding, Arguments: map[string]string{"name": "Rob"}})
			if err != nil {
				t.Fatal(err)
			}
			var roles []string
			for _, m := range mail.Messages {
				roles = append(roles, string(m.Role))
			}
			if !slices.Equal(roles, []string{"user", "assistant", "user"}) {
				t.Errorf("onboarding roles = %v", roles)
			}

			for _, args := range []map[string]string{{"user_id": "x"}, {"user_id": "999"}, {"user_id": "1", "style": "haiku"}} {
				if _, err := h.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: PromptSummarizeUser, Arguments: args}); err == nil {
					t.Errorf("summarize_user %v: want error", args)
				}
			}
		})
	}
}

func TestCompletion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ref  *mcp.CompleteReference
		arg  mcp.CompleteParamsArgument
		want []string
	}{
		{"prompt user ids", &mcp.CompleteReference{Type: "ref/prompt", Name: PromptSummarizeUser}, mcp.CompleteParamsArgument{Name: "user_id", Value: "1"}, []string{"1", "10", "11", "12"}},
		{"prompt enum", &mcp.CompleteReference{Type: "ref/prompt", Name: PromptSummarizeUser}, mcp.CompleteParamsArgument{Name: "style", Value: "B"}, []string{"brief", "bullet-points"}},
		{"prompt names", &mcp.CompleteReference{Type: "ref/prompt", Name: PromptOnboarding}, mcp.CompleteParamsArgument{Name: "name", Value: "ro"}, []string{"Rob Pike", "Robert Griesemer"}},
		{"prompt tone", &mcp.CompleteReference{Type: "ref/prompt", Name: PromptOnboarding}, mcp.CompleteParamsArgument{Name: "tone", Value: "f"}, []string{"formal", "friendly"}},
		{"template param", &mcp.CompleteReference{Type: "ref/resource", URI: URITemplateAvatar}, mcp.CompleteParamsArgument{Name: "id", Value: "2"}, []string{"2"}},
		{"unknown ref", &mcp.CompleteReference{Type: "ref/prompt", Name: "nope"}, mcp.CompleteParamsArgument{Name: "x"}, []string{}},
	}
	for _, v := range versions {
		for _, tt := range tests {
			t.Run(v+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, v, nil)
				res, err := h.cs.Complete(t.Context(), &mcp.CompleteParams{Ref: tt.ref, Argument: tt.arg})
				if err != nil {
					t.Fatal(err)
				}
				if got := res.Completion.Values; !slices.Equal(got, tt.want) || res.Completion.Total != len(tt.want) {
					t.Errorf("values = %v (total %d), want %v", got, res.Completion.Total, tt.want)
				}
			})
		}
	}
}

func TestNewRequiresStore(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("New without a store should panic")
		}
	}()
	New(Deps{})
}

func TestRequireScope(t *testing.T) {
	t.Parallel()
	withScopes := func(scopes ...string) *mcp.CallToolRequest {
		return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{Scopes: scopes}}}
	}
	tests := []struct {
		name string
		req  *mcp.CallToolRequest
		want error
	}{
		{"unauthenticated transport", &mcp.CallToolRequest{}, nil},
		{"scope present", withScopes(ScopeRead, ScopeWrite), nil},
		{"scope missing", withScopes(ScopeRead), errInsufficientScope},
	}
	for _, tt := range tests {
		if err := requireScope(tt.req, ScopeWrite); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
}
