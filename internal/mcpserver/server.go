// Package mcpserver builds the repository's Model Context Protocol server.
//
// New assembles an *mcp.Server exposing the users domain (internal/db) as
// tools, resources, and prompts, targeting MCP spec revision 2026-07-28
// while staying compatible with 2025-11-25-era clients. NewHTTPHandler wraps
// it in a streamable HTTP endpoint that serves the 2026-07-28 stateless mode
// and legacy stateful sessions side by side.
//
// Dependencies are injected through Deps so the server runs against the
// in-memory MemoryStore with zero infrastructure, or against Postgres via
// PgxStore.
package mcpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaeawc/golang-build/internal/clock"
	"github.com/kaeawc/golang-build/internal/healthcheck"
	"github.com/kaeawc/golang-build/internal/idgen"
	"github.com/kaeawc/golang-build/internal/tokens"
)

// ServerName is the MCP implementation name advertised to clients.
const ServerName = "golang-build-mcp"

// Protocol versions this server negotiates, newest first. 2024-11-05 is
// excluded because its HTTP+SSE transport is not served here.
var ProtocolVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26"}

// Deps are the collaborators New wires into the server. Only Store is
// required.
type Deps struct {
	Store  UserStore
	Logger *slog.Logger
	Clock  clock.Clock
	// Health backs the health_check tool. Nil yields an empty registry.
	Health *healthcheck.Registry
	// StateTokens signs multi round-trip requestState. Nil means a random
	// per-process HMAC key (fine for stdio and single-instance HTTP).
	StateTokens tokens.IssuerVerifier
	// IDs generates URL-elicitation IDs. Nil means idgen.UUID.
	IDs idgen.Generator
	// Consent, when set, makes connect_calendar verify that the URL-mode
	// elicitation was really completed out of band.
	Consent ConsentStore
	// ConsentURL is the page URL-mode elicitations send the user to.
	ConsentURL string
	// Version overrides the build-info derived implementation version.
	Version string
	// PageSize caps items per page for the MCP list methods. Default 5, so
	// clients visibly exercise cursor pagination.
	PageSize int
	// ListTTL is the SEP-2549 freshness hint attached to list results.
	ListTTL time.Duration
	// ExportStepDelay simulates per-batch work in export_users.
	ExportStepDelay time.Duration
}

// app carries the state shared by all feature handlers.
type app struct {
	Deps
	server *mcp.Server
	state  *stateSigner
	log    *slog.Logger

	mu           sync.Mutex
	experimental bool
}

// New builds the MCP server. It panics if deps.Store is nil, since that is a
// programming error rather than a runtime condition.
func New(deps Deps) *mcp.Server {
	if deps.Store == nil {
		panic("mcpserver.New: Deps.Store is required")
	}
	a := &app{Deps: deps}
	a.applyDefaults()

	a.server = mcp.NewServer(a.implementation(), &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       a.log,
		PageSize:     a.PageSize,
		// Explicit capabilities: logging is omitted on purpose (deprecated by
		// SEP-2577); everything else is declared up front so list-changed
		// notifications work even for features registered at runtime.
		Capabilities: &mcp.ServerCapabilities{
			Tools:       &mcp.ToolCapabilities{ListChanged: true},
			Prompts:     &mcp.PromptCapabilities{ListChanged: true},
			Resources:   &mcp.ResourceCapabilities{ListChanged: true, Subscribe: true},
			Completions: &mcp.CompletionCapabilities{},
		},
		CompletionHandler:         a.complete,
		SubscribeHandler:          a.subscribe,
		UnsubscribeHandler:        a.unsubscribe,
		SetCacheable:              a.setCacheable,
		SupportedProtocolVersions: ProtocolVersions,
	})
	a.server.AddReceivingMiddleware(LoggingMiddleware(a.log))

	a.registerTools()
	a.registerResources()
	a.registerPrompts()
	return a.server
}

func (a *app) applyDefaults() {
	if a.Logger == nil {
		a.Logger = slog.New(slog.DiscardHandler)
	}
	a.log = a.Logger
	if a.Clock == nil {
		a.Clock = clock.Default
	}
	if a.Health == nil {
		a.Health = healthcheck.NewRegistry(healthcheck.WithClock(a.Clock))
	}
	if a.IDs == nil {
		a.IDs = idgen.UUID{}
	}
	if a.PageSize <= 0 {
		a.PageSize = 5
	}
	if a.ListTTL == 0 {
		a.ListTTL = time.Minute
	}
	if a.ExportStepDelay == 0 {
		a.ExportStepDelay = 150 * time.Millisecond
	}
	if a.Version == "" {
		a.Version = BuildVersion()
	}
	a.state = newStateSigner(a.StateTokens, a.Clock)
}

const instructions = `This server manages the golang-build users directory.
Use list_users (paginated) or the users://{id} resource to look users up,
create_user / delete_user to modify them (delete asks the user to confirm),
and subscribe to users://directory to be told when the directory changes.
export_users is long-running and reports progress; it can be cancelled.`

func (a *app) implementation() *mcp.Implementation {
	return &mcp.Implementation{
		Name:        ServerName,
		Title:       "golang-build users",
		Description: "Users directory for the golang-build template, exposed over MCP.",
		Version:     a.Version,
		WebsiteURL:  "https://github.com/kaeawc/golang-build",
		Icons:       []mcp.Icon{svgIcon(iconServer)},
	}
}

// BuildVersion reports the module version from runtime build info, falling
// back to the VCS revision for source builds and "dev" when neither exists
// (e.g. under go test).
func BuildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			return "dev+" + s.Value[:12]
		}
	}
	return "dev"
}

// setCacheable implements SEP-2549 policy: list and discover results are
// identical for every caller, so they are public with a TTL. User data is
// private and uncached because clients learn about changes via
// subscriptions; static assets get a long public TTL.
func (a *app) setCacheable(_ context.Context, req mcp.Request, c *mcp.Cacheable) {
	if rr, ok := req.(*mcp.ReadResourceRequest); ok {
		switch uri := rr.Params.URI; {
		case uri == URIServerInfo, uri == URILogo:
			c.TTLMs, c.CacheScope = int((5 * time.Minute).Milliseconds()), "public"
		case strings.HasPrefix(uri, "users://"):
			c.TTLMs, c.CacheScope = 0, "private"
		}
		return
	}
	if c.TTLMs == 0 {
		c.TTLMs = int(a.ListTTL.Milliseconds())
	}
	c.CacheScope = "public"
}

func (a *app) subscribe(_ context.Context, req *mcp.SubscribeRequest) error {
	if !subscribable(req.Params.URI) {
		return mcp.ResourceNotFoundError(req.Params.URI)
	}
	a.log.Debug("resource subscribed", "uri", req.Params.URI)
	return nil
}

func (a *app) unsubscribe(_ context.Context, req *mcp.UnsubscribeRequest) error {
	a.log.Debug("resource unsubscribed", "uri", req.Params.URI)
	return nil
}

func subscribable(uri string) bool {
	if uri == URIDirectory {
		return true
	}
	_, avatar, err := ParseUserURI(uri)
	return err == nil && !avatar
}

// notifyUserChanged publishes resources/updated for the directory and the
// user's own resource. Errors are logged: a failed notification must not
// fail the mutation that already happened.
func (a *app) notifyUserChanged(ctx context.Context, id int64) {
	for _, uri := range []string{URIDirectory, UserURI(id)} {
		if err := a.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: uri}); err != nil {
			a.log.Warn("resource updated notification failed", "uri", uri, "err", err)
		}
	}
}

const (
	iconServer = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" rx="3" fill="#00add8"/><path d="M4 5h8v2H4zm0 4h8v2H4z" fill="#fff"/></svg>`
	iconUser   = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="5" r="3" fill="#00add8"/><path d="M2 15c0-4 3-6 6-6s6 2 6 6z" fill="#00add8"/></svg>`
	iconDanger = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M8 1l7 14H1z" fill="#d93025"/><path d="M7 6h2v4H7zm0 5h2v2H7z" fill="#fff"/></svg>`
)

func svgIcon(svg string) mcp.Icon {
	return mcp.Icon{
		Source:   "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)),
		MIMEType: "image/svg+xml",
		Sizes:    []string{"any"},
	}
}

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
