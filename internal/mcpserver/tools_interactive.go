package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Input-request keys. The client echoes them back in InputResponses.
const (
	inputConfirmDelete = "confirm_delete"
	inputCalendar      = "calendar_consent"
)

// DeleteUserOutput is the delete_user structured result.
type DeleteUserOutput struct {
	Deleted bool   `json:"deleted"`
	User    User   `json:"user"`
	Reason  string `json:"reason,omitempty" jsonschema:"why nothing was deleted"`
}

// deleteUser demonstrates a multi round-trip request (SEP-2322):
//
//  1. First call: no InputResponses, so the handler returns an
//     InputRequests map holding a form elicitation plus a signed
//     RequestState, and does nothing else.
//  2. The client asks the human, then retries tools/call with
//     InputResponses and the echoed RequestState.
//  3. The handler verifies the state was minted for this tool and this user
//     before acting on the answer.
//
// On 2026-07-28 the round trip happens on the wire. For 2025-11-25 clients
// the SDK's server middleware turns the InputRequests into a classic
// elicitation/create request and re-invokes the handler once, so this code
// is protocol-version independent.
func (a *app) deleteUser(ctx context.Context, req *mcp.CallToolRequest, in UserIDInput) (*mcp.CallToolResult, DeleteUserOutput, error) {
	var zero DeleteUserOutput
	if err := requireScope(req, ScopeWrite); err != nil {
		return nil, zero, err
	}
	u, err := a.Store.Get(ctx, in.ID)
	if err != nil {
		return nil, zero, err
	}
	target := strconv.FormatInt(u.ID, 10)

	answer, answered := req.Params.InputResponses[inputConfirmDelete]
	if !answered {
		res, err := a.askDeleteConfirmation(req, u, target)
		return res, zero, err
	}

	if err := a.state.verify(req.Params.RequestState, ToolDeleteUser, target); err != nil {
		// Tampered or replayed state is a protocol violation, not a tool
		// outcome, so it surfaces as a JSON-RPC error.
		return nil, zero, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
	}
	if ok, action := confirmed(answer); !ok {
		return nil, DeleteUserOutput{User: u, Reason: "not confirmed (action=" + action + ")"}, nil
	}
	if err := a.Store.Delete(ctx, u.ID); err != nil {
		return nil, zero, err
	}
	a.notifyUserChanged(ctx, u.ID)
	return nil, DeleteUserOutput{Deleted: true, User: u}, nil
}

// askDeleteConfirmation builds the first-round input_required result.
func (a *app) askDeleteConfirmation(req *mcp.CallToolRequest, u User, target string) (*mcp.CallToolResult, error) {
	if !supportsElicitation(req.ClientCapabilities(), "form") {
		return nil, errNeedsElicitation
	}
	state, err := a.state.issue(ToolDeleteUser, target)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{inputConfirmDelete: confirmDeleteRequest(u)},
		RequestState:  state,
	}, nil
}

// confirmed reports whether the elicitation answer is an explicit yes, and
// the action taken otherwise.
func confirmed(answer mcp.InputResponse) (bool, string) {
	res, _ := answer.(*mcp.ElicitResult)
	if res == nil {
		return false, "missing"
	}
	return res.Action == "accept" && res.Content["confirm"] == true, res.Action
}

func confirmDeleteRequest(u User) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Message: fmt.Sprintf("Permanently delete user #%d (%s)?", u.ID, u.Name),
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"confirm": {Type: "boolean", Title: "Delete", Description: "Check to confirm the deletion"},
			},
			Required: []string{"confirm"},
		},
	}
}

// supportsElicitation reports whether caps cover the given elicitation mode.
// Per the spec an empty elicitation capability means form-only.
func supportsElicitation(caps *mcp.ClientCapabilities, mode string) bool {
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	e := caps.Elicitation
	if mode == "url" {
		return e.URL != nil
	}
	return e.Form != nil || e.URL == nil
}

// CalendarInput is the connect_calendar argument object.
type CalendarInput struct {
	Provider string `json:"provider,omitempty" jsonschema:"calendar provider label (default example-calendar)"`
}

// CalendarOutput is the connect_calendar structured result.
type CalendarOutput struct {
	Connected     bool   `json:"connected"`
	Provider      string `json:"provider"`
	ElicitationID string `json:"elicitationId"`
}

// connectCalendar demonstrates URL-mode elicitation through MRTR: sensitive
// interactions (OAuth consent, payments) must not flow through the MCP
// client, so the server sends the user to a URL instead of a form.
func (a *app) connectCalendar(_ context.Context, req *mcp.CallToolRequest, in CalendarInput) (*mcp.CallToolResult, CalendarOutput, error) {
	var zero CalendarOutput
	provider := strings.TrimSpace(in.Provider)
	if provider == "" {
		provider = "example-calendar"
	}
	answer, answered := req.Params.InputResponses[inputCalendar]
	if !answered {
		res, err := a.askCalendarConsent(req, provider)
		return res, zero, err
	}

	// The elicitation ID travels inside the signed state, so any replica can
	// verify the retry; only the consent bookkeeping is stateful.
	id, err := a.state.open(req.Params.RequestState, ToolConnectCalendar)
	if err != nil {
		return nil, zero, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error()}
	}
	if res, _ := answer.(*mcp.ElicitResult); res == nil || res.Action != "accept" {
		return nil, CalendarOutput{Provider: provider, ElicitationID: id}, nil
	}
	// Consume makes the completed consent single-use: replaying this
	// (still validly signed) state cannot connect a second time.
	if a.Consent != nil && !a.Consent.Consume(id) {
		return nil, zero, fmt.Errorf("consent %s is not completed (or was already used); finish it in the browser and call connect_calendar again", id)
	}
	return nil, CalendarOutput{Connected: true, Provider: provider, ElicitationID: id}, nil
}

// askCalendarConsent issues the URL-mode input request and registers its ID
// with the consent store, so only IDs minted here can be completed.
func (a *app) askCalendarConsent(req *mcp.CallToolRequest, provider string) (*mcp.CallToolResult, error) {
	if !supportsElicitation(req.ClientCapabilities(), "url") {
		return nil, fmt.Errorf("%w (url mode)", errNeedsElicitation)
	}
	id := a.IDs.Next()
	if a.Consent != nil {
		if err := a.Consent.Begin(id); err != nil {
			return nil, err
		}
	}
	state, err := a.state.issue(ToolConnectCalendar, id)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{inputCalendar: &mcp.ElicitParams{
			Mode:          "url",
			Message:       "Authorize access to your " + provider + " calendar in the browser.",
			URL:           a.consentURL(id),
			ElicitationID: id,
		}},
		RequestState: state,
	}, nil
}

func (a *app) consentURL(id string) string {
	base := a.ConsentURL
	if base == "" {
		base = "https://calendar.example.com/consent"
	}
	return base + "?id=" + url.QueryEscape(id)
}

// ExportInput is the export_users argument object.
type ExportInput struct {
	BatchSize int `json:"batchSize,omitempty" jsonschema:"users per batch, 1-50 (default 5)"`
}

// ExportOutput is the export_users structured result.
type ExportOutput struct {
	Exported int `json:"exported"`
	Batches  int `json:"batches"`
}

// exportUsers is the long-running tool: it emits notifications/progress
// after each batch (when the caller supplied a progress token) and returns
// as soon as ctx is cancelled, which is how notifications/cancelled (or a
// dropped HTTP request) reaches handler code.
func (a *app) exportUsers(ctx context.Context, req *mcp.CallToolRequest, in ExportInput) (*mcp.CallToolResult, ExportOutput, error) {
	batch := in.BatchSize
	if batch == 0 {
		batch = defaultListLimit
	}
	if batch < 1 || batch > maxListLimit {
		return nil, ExportOutput{}, fmt.Errorf("batchSize must be between 1 and %d", maxListLimit)
	}
	users, err := a.allUsers(ctx)
	if err != nil {
		return nil, ExportOutput{}, err
	}
	token := req.Params.GetProgressToken()
	var csv strings.Builder
	csv.WriteString("id,name\n")
	out := ExportOutput{}
	for start := 0; start < len(users); start += batch {
		for _, u := range users[start:min(start+batch, len(users))] {
			fmt.Fprintf(&csv, "%d,%q\n", u.ID, u.Name)
			out.Exported++
		}
		out.Batches++
		if err := sleepCtx(ctx, a.ExportStepDelay); err != nil {
			return nil, ExportOutput{}, fmt.Errorf("export cancelled after %d users: %w", out.Exported, err)
		}
		if token != nil {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Progress:      float64(out.Exported),
				Total:         float64(len(users)),
				Message:       fmt.Sprintf("exported %d/%d users", out.Exported, len(users)),
			})
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf("Exported %d users in %d batches.", out.Exported, out.Batches)},
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "exports://users.csv", MIMEType: "text/csv", Text: csv.String()}},
	}}, out, nil
}

// sleepCtx waits for d or until ctx is done. Real timers are used on
// purpose: tests run inside testing/synctest bubbles, where they are
// virtualised and complete instantly.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
		return nil
	}
}
