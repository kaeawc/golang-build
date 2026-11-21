package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/healthcheck"
	"github.com/kaeawc/golang-build/internal/paginator"
)

// Tool names.
const (
	ToolListUsers       = "list_users"
	ToolGetUser         = "get_user"
	ToolCreateUser      = "create_user"
	ToolDeleteUser      = "delete_user"
	ToolUserCard        = "user_card"
	ToolExportUsers     = "export_users"
	ToolHealthCheck     = "health_check"
	ToolConnectCalendar = "connect_calendar"
	ToolSetExperimental = "set_experimental_features"
	ToolGenerateReport  = "generate_report"
)

const (
	defaultListLimit = 5
	maxListLimit     = 50
)

func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}
}

func (a *app) registerTools() {
	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolListUsers,
		Title:       "List users",
		Description: "List users ordered by ID. Pass nextCursor from a previous call to fetch the next page.",
		Annotations: readOnly(),
		Icons:       []mcp.Icon{svgIcon(iconUser)},
	}, a.listUsers)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolGetUser,
		Title:       "Get user",
		Description: "Fetch one user by ID. Returns the user as structured content and as an embedded users://{id} resource.",
		Annotations: readOnly(),
		Icons:       []mcp.Icon{svgIcon(iconUser)},
	}, a.getUser)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolCreateUser,
		Title:       "Create user",
		Description: "Create a user. Subscribers to users://directory are notified.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)},
	}, a.createUser)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolDeleteUser,
		Title:       "Delete user",
		Description: "Permanently delete a user. The server asks the human to confirm via elicitation before deleting.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
		Icons:       []mcp.Icon{svgIcon(iconDanger)},
	}, a.deleteUser)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolUserCard,
		Title:       "User card",
		Description: "Render a user as rich content: text, a PNG identicon, a WAV chime, and a link to the user resource.",
		Annotations: readOnly(),
	}, a.userCard)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolExportUsers,
		Title:       "Export users",
		Description: "Export every user as CSV. Long-running: reports progress when a progress token is supplied and stops promptly when cancelled.",
		Annotations: readOnly(),
	}, a.exportUsers)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolHealthCheck,
		Title:       "Health check",
		Description: "Run the server's readiness probes (e.g. database connectivity).",
		Annotations: readOnly(),
	}, a.healthCheck)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolConnectCalendar,
		Title:       "Connect calendar",
		Description: "Link a (simulated) third-party calendar. Sends the user to a consent page via URL-mode elicitation.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(true)},
	}, a.connectCalendar)

	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolSetExperimental,
		Title:       "Toggle experimental features",
		Description: "Enable or disable experimental features at runtime. Adds/removes the generate_report tool and the reports://latest resource, which emits tools and resources list_changed notifications.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)},
	}, a.setExperimental)
}

// ListUsersInput is the list_users argument object.
type ListUsersInput struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque cursor returned as nextCursor by a previous call"`
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, 1-50 (default 5)"`
}

// ListUsersOutput is the list_users structured result.
type ListUsersOutput struct {
	Users      []User `json:"users" jsonschema:"users on this page"`
	NextCursor string `json:"nextCursor,omitempty" jsonschema:"cursor for the next page; absent on the last page"`
}

type userCursor struct {
	After int64 `json:"after"`
}

func (a *app) listUsers(ctx context.Context, _ *mcp.CallToolRequest, in ListUsersInput) (*mcp.CallToolResult, ListUsersOutput, error) {
	var zero ListUsersOutput
	limit := in.Limit
	switch {
	case limit == 0:
		limit = defaultListLimit
	case limit < 0 || limit > maxListLimit:
		return nil, zero, fmt.Errorf("limit must be between 1 and %d", maxListLimit)
	}
	var cur userCursor
	if in.Cursor != "" {
		if err := paginator.Decode(in.Cursor, &cur); err != nil {
			return nil, zero, fmt.Errorf("invalid cursor: %w", err)
		}
	}
	rows, err := a.Store.List(ctx, cur.After, limit+1)
	if err != nil {
		return nil, zero, err
	}
	page, hasMore := paginator.SplitPage(rows, limit)
	out := ListUsersOutput{Users: append([]User{}, page...)}
	if hasMore {
		if out.NextCursor, err = paginator.Encode(userCursor{After: page[len(page)-1].ID}); err != nil {
			return nil, zero, err
		}
	}

	content := []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%d user(s); more pages: %t", len(page), hasMore)}}
	for _, u := range page {
		content = append(content, &mcp.ResourceLink{
			URI: UserURI(u.ID), Name: u.Name, MIMEType: "application/json",
		})
	}
	return &mcp.CallToolResult{Content: content}, out, nil
}

// UserIDInput identifies a single user.
type UserIDInput struct {
	ID int64 `json:"id" jsonschema:"user ID"`
}

func (a *app) getUser(ctx context.Context, _ *mcp.CallToolRequest, in UserIDInput) (*mcp.CallToolResult, User, error) {
	u, err := a.Store.Get(ctx, in.ID)
	if err != nil {
		// A plain error becomes an IsError tool result (visible to the
		// model) rather than a JSON-RPC protocol error.
		return nil, User{}, err
	}
	body, err := json.Marshal(u)
	if err != nil {
		return nil, User{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI: UserURI(u.ID), MIMEType: "application/json", Text: string(body),
		}},
	}}, u, nil
}

// CreateUserInput is the create_user argument object.
type CreateUserInput struct {
	Name string `json:"name" jsonschema:"display name, 1-100 bytes"`
}

func (a *app) createUser(ctx context.Context, req *mcp.CallToolRequest, in CreateUserInput) (*mcp.CallToolResult, User, error) {
	if err := requireScope(req, ScopeWrite); err != nil {
		return nil, User{}, err
	}
	u, err := a.Store.Create(ctx, in.Name)
	if err != nil {
		return nil, User{}, err
	}
	a.notifyUserChanged(ctx, u.ID)
	return nil, u, nil
}

// UserCardOutput summarises the media returned by user_card.
type UserCardOutput struct {
	User       User   `json:"user"`
	AvatarURI  string `json:"avatarUri" jsonschema:"resource URI of the avatar image"`
	ImageBytes int    `json:"imageBytes"`
	AudioBytes int    `json:"audioBytes"`
}

func (a *app) userCard(ctx context.Context, _ *mcp.CallToolRequest, in UserIDInput) (*mcp.CallToolResult, UserCardOutput, error) {
	u, err := a.Store.Get(ctx, in.ID)
	if err != nil {
		return nil, UserCardOutput{}, err
	}
	img, wav := identiconPNG(u.Name), chimeWAV(u.Name)
	out := UserCardOutput{User: u, AvatarURI: AvatarURI(u.ID), ImageBytes: len(img), AudioBytes: len(wav)}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf("#%d %s", u.ID, u.Name)},
		&mcp.ImageContent{Data: img, MIMEType: "image/png"},
		&mcp.AudioContent{Data: wav, MIMEType: "audio/wav"},
		&mcp.ResourceLink{URI: UserURI(u.ID), Name: u.Name, MIMEType: "application/json"},
	}}, out, nil
}

// HealthOutput is the health_check structured result.
type HealthOutput struct {
	Status string                    `json:"status" jsonschema:"ok or fail"`
	Checks []healthcheck.CheckResult `json:"checks"`
}

func (a *app) healthCheck(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, HealthOutput, error) {
	rep := a.Health.CheckReadiness(ctx)
	out := HealthOutput{Status: string(rep.Status), Checks: append([]healthcheck.CheckResult{}, rep.Checks...)}
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "readiness: " + out.Status}},
		IsError: rep.Status != healthcheck.StatusOK,
	}
	return res, out, nil
}

// ExperimentalInput is the set_experimental_features argument object.
type ExperimentalInput struct {
	Enabled bool `json:"enabled" jsonschema:"whether experimental features should be available"`
}

// ExperimentalOutput reports what changed.
type ExperimentalOutput struct {
	Enabled bool `json:"enabled"`
	Changed bool `json:"changed" jsonschema:"false when the requested state was already active"`
}

func (a *app) setExperimental(_ context.Context, _ *mcp.CallToolRequest, in ExperimentalInput) (*mcp.CallToolResult, ExperimentalOutput, error) {
	a.mu.Lock()
	changed := a.experimental != in.Enabled
	a.experimental = in.Enabled
	a.mu.Unlock()
	if changed {
		if in.Enabled {
			a.addExperimental()
		} else {
			a.server.RemoveTools(ToolGenerateReport)
			a.server.RemoveResources(URIReport)
		}
	}
	return nil, ExperimentalOutput{Enabled: in.Enabled, Changed: changed}, nil
}

// ReportOutput is the generate_report structured result.
type ReportOutput struct {
	Users       int    `json:"users"`
	GeneratedAt string `json:"generatedAt" jsonschema:"RFC 3339 timestamp"`
}

func (a *app) addExperimental() {
	mcp.AddTool(a.server, &mcp.Tool{
		Name:        ToolGenerateReport,
		Title:       "Generate report (experimental)",
		Description: "Summarise the directory as a markdown report. Only present while experimental features are enabled.",
		Annotations: readOnly(),
	}, a.generateReport)
	a.server.AddResource(&mcp.Resource{
		URI: URIReport, Name: "latest-report", Title: "Latest report",
		MIMEType: "text/markdown", Description: "Most recent directory report (experimental).",
	}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		md, _, err := a.report(ctx)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: URIReport, MIMEType: "text/markdown", Text: md}}}, nil
	})
}

func (a *app) generateReport(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ReportOutput, error) {
	md, out, err := a.report(ctx)
	if err != nil {
		return nil, ReportOutput{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf("Report covers %d users.", out.Users)},
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: URIReport, MIMEType: "text/markdown", Text: md}},
	}}, out, nil
}

func (a *app) report(ctx context.Context) (string, ReportOutput, error) {
	users, err := a.allUsers(ctx)
	if err != nil {
		return "", ReportOutput{}, err
	}
	var b strings.Builder
	b.WriteString("# Directory report\n\n")
	for _, u := range users {
		fmt.Fprintf(&b, "- #%d %s\n", u.ID, u.Name)
	}
	return b.String(), ReportOutput{Users: len(users), GeneratedAt: a.Clock.Now().UTC().Format("2006-01-02T15:04:05Z07:00")}, nil
}

// allUsers pages through the whole store. Fine for the demo directory; a
// production export would stream instead of materialising everything.
func (a *app) allUsers(ctx context.Context) ([]User, error) {
	const page = 100
	var all []User
	var after int64
	for {
		rows, err := a.Store.List(ctx, after, page)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
		if len(rows) < page {
			return all, nil
		}
		after = rows[len(rows)-1].ID
	}
}

var errNeedsElicitation = errors.New("this tool needs a client that supports elicitation")
