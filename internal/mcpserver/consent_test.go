package mcpserver

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/idgen"
)

func TestMemoryConsent(t *testing.T) {
	t.Parallel()
	type op struct {
		do      string // begin, complete, consume, advance
		id      string
		advance time.Duration
		want    bool
	}
	tests := []struct {
		name    string
		ops     []op
		wantLen int
	}{
		{"unknown id rejected and not stored", []op{
			{do: "complete", id: "x", want: false},
			{do: "consume", id: "x", want: false},
		}, 0},
		{"issued id completes and is consumed once", []op{
			{do: "begin", id: "a", want: true},
			{do: "consume", id: "a", want: false}, // pending, not yet completed
			{do: "complete", id: "a", want: true},
			{do: "consume", id: "a", want: true},
			{do: "consume", id: "a", want: false},  // single use
			{do: "complete", id: "a", want: false}, // gone, cannot be revived
		}, 0},
		{"expired id rejected", []op{
			{do: "begin", id: "b", want: true},
			{do: "advance", advance: requestStateTTL},
			{do: "complete", id: "b", want: false},
		}, 0},
		{"completed but expired before use", []op{
			{do: "begin", id: "c", want: true},
			{do: "complete", id: "c", want: true},
			{do: "advance", advance: requestStateTTL + time.Second},
			{do: "consume", id: "c", want: false},
		}, 0},
		{"still valid just before expiry", []op{
			{do: "begin", id: "d", want: true},
			{do: "advance", advance: requestStateTTL - time.Second},
			{do: "complete", id: "d", want: true},
		}, 1},
		{"pending ids are evicted lazily", []op{
			{do: "begin", id: "e1", want: true},
			{do: "begin", id: "e2", want: true},
			{do: "advance", advance: requestStateTTL},
			{do: "begin", id: "e3", want: true},
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(time.Unix(1_800_000_000, 0))
			m := NewMemoryConsent(clk)
			for i, o := range tt.ops {
				var got bool
				switch o.do {
				case "begin":
					got = m.Begin(o.id) == nil
				case "complete":
					got = m.Complete(o.id)
				case "consume":
					got = m.Consume(o.id)
				case "advance":
					clk.Advance(o.advance)
					continue
				}
				if got != o.want {
					t.Fatalf("op %d %s(%s) = %t, want %t", i, o.do, o.id, got, o.want)
				}
			}
			if n := m.Len(); n != tt.wantLen {
				t.Errorf("Len = %d, want %d", n, tt.wantLen)
			}
		})
	}
}

func TestMemoryConsentBounded(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(0, 0))
	m := NewMemoryConsent(clk)
	for i := range maxPendingConsents {
		if err := m.Begin(strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Begin("one-too-many"); !errors.Is(err, ErrConsentFull) {
		t.Fatalf("err = %v, want ErrConsentFull", err)
	}
	// Once the pending flows expire, hitting the cap sweeps them away.
	clk.Advance(requestStateTTL)
	if err := m.Begin("after-expiry"); err != nil {
		t.Fatalf("Begin after expiry: %v", err)
	}
	if n := m.Len(); n != 1 {
		t.Errorf("Len = %d, want 1", n)
	}
}

// TestConnectCalendarConsentLifecycle drives connect_calendar by hand to
// check that the consent recorded for one elicitation is single-use and
// expires with the requestState window.
func TestConnectCalendarConsentLifecycle(t *testing.T) {
	t.Parallel()
	urlOnly := &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}}
	tests := []struct {
		name      string
		complete  bool
		expired   bool
		replay    bool // retry a second time with the same state
		wantErr   string
		wantFinal bool
	}{
		{name: "completed then connected", complete: true, wantFinal: true},
		{name: "never completed", complete: false, wantErr: "not completed"},
		{name: "consumed consent cannot be replayed", complete: true, replay: true, wantErr: "already used"},
		{name: "consent expired before retry", complete: true, expired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(time.Unix(1_800_000_000, 0))
			consent := NewMemoryConsent(clk)
			h := newHarness(t, latest, &mcp.ClientOptions{
				Capabilities: urlOnly,
				ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					return nil, errors.New("unused: round trip is driven manually")
				},
				MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
			}, func(d *Deps) {
				d.Consent, d.Clock, d.IDs = consent, clk, idgen.NewSequence("cal")
			})
			ctx := t.Context()
			first, err := h.cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolConnectCalendar})
			if err != nil || !first.NeedsInput() {
				t.Fatalf("first call: %v %+v", err, first)
			}
			id := first.InputRequests[inputCalendar].(*mcp.ElicitParams).ElicitationID
			if tt.complete && !consent.Complete(id) {
				t.Fatalf("issued id %q could not be completed", id)
			}
			if tt.expired {
				clk.Advance(requestStateTTL)
			}
			retry := func() (*mcp.CallToolResult, error) {
				return h.cs.CallTool(ctx, &mcp.CallToolParams{
					Name:           ToolConnectCalendar,
					InputResponses: mcp.InputResponseMap{inputCalendar: &mcp.ElicitResult{Action: "accept"}},
					RequestState:   first.RequestState,
				})
			}
			res, err := retry()
			if tt.expired {
				// At exactly requestStateTTL the signed state is still
				// accepted, but the consent entry has expired and been
				// evicted, so the tool refuses to connect.
				if err != nil || !res.IsError || !strings.Contains(resultText(res), "not completed") || consent.Len() != 0 {
					t.Fatalf("expired flow: err=%v res=%+v len=%d", err, res, consent.Len())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.replay {
				if out := structured[CalendarOutput](t, res); !out.Connected {
					t.Fatalf("first retry should connect: %+v", res)
				}
				if res, err = retry(); err != nil {
					t.Fatal(err)
				}
			}
			if tt.wantErr != "" {
				if !res.IsError || !strings.Contains(resultText(res), tt.wantErr) {
					t.Fatalf("want error containing %q, got %+v", tt.wantErr, res)
				}
				return
			}
			if out := structured[CalendarOutput](t, res); out.Connected != tt.wantFinal {
				t.Errorf("output = %+v", out)
			}
		})
	}
}
