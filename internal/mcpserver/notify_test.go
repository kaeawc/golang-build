package mcpserver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResourceSubscriptions(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			updates := make(chan string, 16)
			h := newHarness(t, v, &mcp.ClientOptions{
				ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
					updates <- req.Params.URI
				},
				ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
				},
			})
			ctx := t.Context()
			for _, uri := range []string{URIDirectory, "users://3"} {
				if err := h.cs.Subscribe(ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
					t.Fatalf("subscribe %s: %v", uri, err)
				}
				h.waitAck(t, v)
			}

			h.call(t, ToolCreateUser, map[string]any{"name": "Niklaus Wirth"})
			if got := waitFor(t, updates, "directory update after create"); got != URIDirectory {
				t.Fatalf("update for %q, want %q", got, URIDirectory)
			}

			h.call(t, ToolDeleteUser, map[string]any{"id": 3})
			got := []string{waitFor(t, updates, "update 1 after delete"), waitFor(t, updates, "update 2 after delete")}
			slices.Sort(got)
			if !slices.Equal(got, []string{"users://3", URIDirectory}) {
				t.Fatalf("updates after delete = %v", got)
			}

			// The notification only carries the URI; re-reading shows the change.
			res, err := h.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: URIDirectory})
			if err != nil {
				t.Fatal(err)
			}
			if txt := res.Contents[0].Text; !strings.Contains(txt, "Niklaus Wirth") || strings.Contains(txt, "Barbara Liskov") {
				t.Errorf("directory not refreshed:\n%s", txt)
			}
			if err := h.cs.Unsubscribe(ctx, &mcp.UnsubscribeParams{URI: URIDirectory}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSubscribeRejectsUnknownURI(t *testing.T) {
	t.Parallel()
	// Only observable on legacy sessions: 2026-07-28 subscriptions ride a
	// background subscriptions/listen stream whose errors Subscribe does
	// not surface.
	h := newHarness(t, legacy, nil)
	for _, uri := range []string{"nope://x", URILogo, "users://1/avatar.png"} {
		if err := h.cs.Subscribe(t.Context(), &mcp.SubscribeParams{URI: uri}); err == nil {
			t.Errorf("subscribe %s: want error", uri)
		}
	}
}

func TestListChangedAtRuntime(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			toolsChanged := make(chan struct{}, 8)
			resourcesChanged := make(chan struct{}, 8)
			h := newHarness(t, v, &mcp.ClientOptions{
				ToolListChangedHandler:     func(context.Context, *mcp.ToolListChangedRequest) { toolsChanged <- struct{}{} },
				ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) { resourcesChanged <- struct{}{} },
			})
			ctx := t.Context()
			h.waitAck(t, v) // Connect opened subscriptions/listen for list changes
			hasReport := func() bool {
				for tool, err := range h.cs.Tools(ctx, nil) {
					if err != nil {
						t.Fatal(err)
					}
					if tool.Name == ToolGenerateReport {
						return true
					}
				}
				return false
			}
			if hasReport() {
				t.Fatal("generate_report present before enabling")
			}

			out := structured[ExperimentalOutput](t, h.call(t, ToolSetExperimental, map[string]any{"enabled": true}))
			if !out.Changed {
				t.Fatalf("output = %+v", out)
			}
			waitFor(t, toolsChanged, "tools/list_changed")
			waitFor(t, resourcesChanged, "resources/list_changed")
			// The notification also invalidates the 2026-07-28 client's
			// cached tools/list page, so the new tool is visible at once.
			if !hasReport() {
				t.Fatal("generate_report missing after enabling")
			}
			if rep := structured[ReportOutput](t, h.call(t, ToolGenerateReport, nil)); rep.Users != len(DemoUsers) {
				t.Errorf("report = %+v", rep)
			}
			if _, err := h.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: URIReport}); err != nil {
				t.Errorf("read report: %v", err)
			}

			if again := structured[ExperimentalOutput](t, h.call(t, ToolSetExperimental, map[string]any{"enabled": true})); again.Changed {
				t.Error("enabling twice should be a no-op")
			}
			h.call(t, ToolSetExperimental, map[string]any{"enabled": false})
			waitFor(t, toolsChanged, "tools/list_changed after disabling")
			if _, err := h.cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolGenerateReport}); err == nil {
				t.Error("generate_report still callable after disabling")
			}
		})
	}
}

func TestExportProgress(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var mu sync.Mutex
				var progress []float64
				var total float64
				h := newHarness(t, v, &mcp.ClientOptions{
					ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
						mu.Lock()
						defer mu.Unlock()
						progress = append(progress, req.Params.Progress)
						total = req.Params.Total
					},
				}, func(d *Deps) { d.ExportStepDelay = 2 * time.Second })

				start := time.Now()
				res, err := h.cs.CallTool(t.Context(), &mcp.CallToolParams{
					Name:      ToolExportUsers,
					Arguments: map[string]any{"batchSize": 5},
					Meta:      mcp.Meta{"progressToken": "export-1"},
				})
				if err != nil {
					t.Fatal(err)
				}
				// Virtual time: exactly three 2s batches elapsed.
				if elapsed := time.Since(start); elapsed != 6*time.Second {
					t.Errorf("elapsed = %v, want 6s", elapsed)
				}
				synctest.Wait()
				mu.Lock()
				defer mu.Unlock()
				if !slices.Equal(progress, []float64{5, 10, 12}) || total != 12 {
					t.Errorf("progress = %v / %v", progress, total)
				}
				if out := structured[ExportOutput](t, res); out.Exported != 12 {
					t.Errorf("output = %+v", out)
				}
			})
		})
	}
}

func TestExportCancellation(t *testing.T) {
	t.Parallel()
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var notes atomic.Int32
				h := newHarness(t, v, &mcp.ClientOptions{
					ProgressNotificationHandler: func(context.Context, *mcp.ProgressNotificationClientRequest) {
						notes.Add(1)
						cancel() // the user gives up after the first batch
					},
				}, func(d *Deps) { d.ExportStepDelay = time.Second })

				_, err := h.cs.CallTool(ctx, &mcp.CallToolParams{
					Name:      ToolExportUsers,
					Arguments: map[string]any{"batchSize": 1},
					Meta:      mcp.Meta{"progressToken": 7},
				})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err = %v, want context.Canceled", err)
				}
				// If the handler ignored cancellation it would keep sleeping
				// and emitting progress for 11 more virtual seconds.
				time.Sleep(time.Minute)
				synctest.Wait()
				if n := notes.Load(); n != 1 {
					t.Errorf("received %d progress notifications after cancel, want 1", n)
				}
				if logs := h.logs.String(); !strings.Contains(logs, "tool=export_users isError=true") {
					t.Errorf("server handler did not stop with an error:\n%s", logs)
				}
			})
		})
	}
}

// TestClientHonoursListTTL shows the SEP-2549 ttlMs hint end to end: a
// 2026-07-28 client serves tools/list from its cache until the TTL expires.
// synctest makes the 60s TTL elapse instantly and deterministically.
func TestClientHonoursListTTL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version   string
		wantCalls []int32 // server-side tools/list count after each step
	}{
		{latest, []int32{1, 1, 2}},
		{legacy, []int32{1, 2, 3}}, // no cacheability before 2026-07-28
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, tt.version, nil)
				var calls atomic.Int32
				h.server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
					return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
						if method == "tools/list" {
							calls.Add(1)
						}
						return next(ctx, method, req)
					}
				})
				var got []int32
				for _, wait := range []time.Duration{0, 30 * time.Second, 31 * time.Second} {
					time.Sleep(wait)
					if _, err := h.cs.ListTools(t.Context(), nil); err != nil {
						t.Fatal(err)
					}
					got = append(got, calls.Load())
				}
				if !slices.Equal(got, tt.wantCalls) {
					t.Errorf("server tools/list calls = %v, want %v", got, tt.wantCalls)
				}
			})
		})
	}
}
