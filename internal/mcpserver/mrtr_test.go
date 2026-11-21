package mcpserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/idgen"
	"github.com/kaeawc/golang-build/internal/tokens"
)

// elicitRecorder is a client ElicitationHandler that returns a canned
// answer and records what it was asked.
type elicitRecorder struct {
	mu     sync.Mutex
	asked  []*mcp.ElicitParams
	answer func(*mcp.ElicitParams) *mcp.ElicitResult
}

func (e *elicitRecorder) handle(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	e.mu.Lock()
	e.asked = append(e.asked, req.Params)
	e.mu.Unlock()
	return e.answer(req.Params), nil
}

func (e *elicitRecorder) calls() []*mcp.ElicitParams {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*mcp.ElicitParams(nil), e.asked...)
}

func TestDeleteUserConfirmation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		answer      *mcp.ElicitResult
		wantDeleted bool
	}{
		{"accepted", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, true},
		{"accepted unchecked", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": false}}, false},
		{"declined", &mcp.ElicitResult{Action: "decline"}, false},
		{"cancelled", &mcp.ElicitResult{Action: "cancel"}, false},
	}
	// On 2026-07-28 the client SDK drives the round trip (InputRequests on
	// the wire); on 2025-11-25 the server SDK converts it into a classic
	// elicitation/create request. Same handler code, same outcome.
	for _, v := range versions {
		for _, tt := range tests {
			t.Run(v+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				rec := &elicitRecorder{answer: func(*mcp.ElicitParams) *mcp.ElicitResult { return tt.answer }}
				h := newHarness(t, v, &mcp.ClientOptions{ElicitationHandler: rec.handle})

				res := h.call(t, ToolDeleteUser, map[string]any{"id": 5})
				if res.IsError {
					t.Fatalf("unexpected tool error: %s", resultText(res))
				}
				out := structured[DeleteUserOutput](t, res)
				if out.Deleted != tt.wantDeleted || out.User.Name != "Edsger Dijkstra" {
					t.Errorf("output = %+v", out)
				}
				_, err := h.store.Get(t.Context(), 5)
				if gone := errors.Is(err, ErrNotFound); gone != tt.wantDeleted {
					t.Errorf("store: deleted=%t, want %t", gone, tt.wantDeleted)
				}
				asked := rec.calls()
				if len(asked) != 1 || !strings.Contains(asked[0].Message, "Edsger Dijkstra") {
					t.Errorf("elicitations = %+v", asked)
				}
			})
		}
	}
}

func TestDeleteUserWithoutElicitationSupport(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, v, nil)
			res := h.call(t, ToolDeleteUser, map[string]any{"id": 1})
			if !res.IsError || !strings.Contains(resultText(res), "elicitation") {
				t.Fatalf("want tool error about elicitation, got %+v", res)
			}
			if _, err := h.store.Get(t.Context(), 1); err != nil {
				t.Fatal("user must not be deleted")
			}
		})
	}
}

// TestDeleteUserManualRoundTrip drives SEP-2322 by hand with the client's
// automatic MRTR middleware disabled, covering the wire contract and the
// requestState integrity checks.
func TestDeleteUserManualRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t, latest, &mcp.ClientOptions{
		// The handler is never invoked; it only makes the client declare
		// the elicitation capability.
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return nil, errors.New("unused")
		},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	ctx := t.Context()

	first := func(id int) *mcp.CallToolResult {
		res, err := h.cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolDeleteUser, Arguments: map[string]any{"id": id}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := first(6)
	if !res.NeedsInput() || res.RequestState == "" || len(res.Content) != 0 {
		t.Fatalf("want input_required result, got %+v", res)
	}
	ep, ok := res.InputRequests[inputConfirmDelete].(*mcp.ElicitParams)
	if !ok || !strings.Contains(ep.Message, "Frances Allen") {
		t.Fatalf("input requests = %+v", res.InputRequests)
	}
	otherState := first(7).RequestState

	accept := mcp.InputResponseMap{inputConfirmDelete: &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}}
	retry := func(state string) (*mcp.CallToolResult, error) {
		return h.cs.CallTool(ctx, &mcp.CallToolParams{
			Name: ToolDeleteUser, Arguments: map[string]any{"id": 6},
			InputResponses: accept, RequestState: state,
		})
	}

	for name, state := range map[string]string{
		"tampered":          res.RequestState + "x",
		"minted for user 7": otherState,
		"missing":           "",
	} {
		_, err := retry(state)
		var werr *jsonrpc.Error
		if !errors.As(err, &werr) || werr.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("%s state: err = %v, want invalid params", name, err)
		}
	}
	if _, err := h.store.Get(ctx, 6); err != nil {
		t.Fatal("user deleted despite invalid state")
	}

	final, err := retry(res.RequestState)
	if err != nil {
		t.Fatal(err)
	}
	if out := structured[DeleteUserOutput](t, final); !out.Deleted || final.NeedsInput() {
		t.Fatalf("final = %+v", final)
	}
}

func TestConnectCalendarURLElicitation(t *testing.T) {
	t.Parallel()
	urlCaps := &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}}
	tests := []struct {
		name          string
		visit         bool // whether the "user" completes the browser flow
		caps          *mcp.ClientCapabilities
		wantConnected bool
		wantErr       string
	}{
		{name: "completed", visit: true, caps: urlCaps, wantConnected: true},
		{name: "accepted but not completed", visit: false, caps: urlCaps, wantErr: "not completed"},
		{name: "form-only client", caps: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}}}, wantErr: "url mode"},
	}
	for _, v := range versions {
		for _, tt := range tests {
			t.Run(v+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				consent := NewMemoryConsent(nil)
				rec := &elicitRecorder{answer: func(p *mcp.ElicitParams) *mcp.ElicitResult {
					if tt.visit {
						consent.Complete(p.ElicitationID)
					}
					return &mcp.ElicitResult{Action: "accept"}
				}}
				h := newHarness(t, v, &mcp.ClientOptions{Capabilities: tt.caps, ElicitationHandler: rec.handle}, func(d *Deps) {
					d.Consent = consent
					d.ConsentURL = "http://127.0.0.1:1/consent"
					d.IDs = idgen.NewSequence("elicit")
				})
				res := h.call(t, ToolConnectCalendar, map[string]any{"provider": "work"})
				if tt.wantErr != "" {
					if !res.IsError || !strings.Contains(resultText(res), tt.wantErr) {
						t.Fatalf("want error containing %q, got %+v", tt.wantErr, res)
					}
					return
				}
				out := structured[CalendarOutput](t, res)
				if out.Connected != tt.wantConnected || out.Provider != "work" || out.ElicitationID != "elicit-1" {
					t.Errorf("output = %+v", out)
				}
				asked := rec.calls()
				if len(asked) != 1 || asked[0].Mode != "url" || asked[0].URL != "http://127.0.0.1:1/consent?id=elicit-1" {
					t.Errorf("elicitation = %+v", asked)
				}
			})
		}
	}
}

func TestStateSigner(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	s := newStateSigner(tokens.NewHMAC256([]byte("k"), tokens.WithClock(clk)), clk)
	state, err := s.issue(ToolDeleteUser, "7")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, state, tool, target string
		ok                        bool
	}{
		{"valid", state, ToolDeleteUser, "7", true},
		{"wrong target", state, ToolDeleteUser, "8", false},
		{"wrong tool", state, ToolConnectCalendar, "7", false},
		{"garbage", "abc", ToolDeleteUser, "7", false},
		{"empty", "", ToolDeleteUser, "7", false},
	}
	for _, tt := range tests {
		if err := s.verify(tt.state, tt.tool, tt.target); (err == nil) != tt.ok {
			t.Errorf("%s: err = %v", tt.name, err)
		}
	}
	clk.Advance(requestStateTTL + time.Second)
	if err := s.verify(state, ToolDeleteUser, "7"); !errors.Is(err, errRequestState) {
		t.Errorf("expired state accepted: %v", err)
	}
}
