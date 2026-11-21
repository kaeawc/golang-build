package mcpserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/kaeawc/golang-build/internal/healthcheck"
	"github.com/kaeawc/golang-build/internal/tokens"
)

// Paths served by NewHTTPHandler.
const (
	PathMCP     = "/mcp"
	PathConsent = "/consent"
	PathPRM     = "/.well-known/oauth-protected-resource"
)

// statelessProtocolVersion is the first revision served without sessions.
const statelessProtocolVersion = "2026-07-28"

// HTTPOptions configures NewHTTPHandler.
type HTTPOptions struct {
	Logger *slog.Logger
	// Auth enables bearer-token protection of the MCP endpoint. Nil means
	// no authentication; callers should then bind to loopback only.
	Auth *AuthOptions
	// Health, if set, is served at /healthz and /readyz.
	Health *healthcheck.Registry
	// Consent, if set, serves the URL-elicitation consent page at /consent.
	Consent ConsentStore
	// SessionTimeout closes idle legacy (stateful) sessions. Default 30m.
	SessionTimeout time.Duration
	// TrustedOrigins are browser origins allowed to make cross-origin
	// requests (e.g. "http://localhost:6274" for the MCP Inspector UI).
	TrustedOrigins []string
}

// AuthOptions configures OAuth 2.1 resource-server behaviour.
type AuthOptions struct {
	// Verifier checks bearer-token signatures (HS256 via internal/tokens).
	Verifier tokens.Verifier
	// ResourceURL is this server's canonical MCP endpoint URL. It is the
	// required token audience and the "resource" in protected resource
	// metadata (RFC 9728).
	ResourceURL string
	// AuthorizationServers are advertised in protected resource metadata.
	AuthorizationServers []string
	// RequiredScopes must all be present on every request. Default
	// [ScopeRead].
	RequiredScopes []string
}

// NewHTTPHandler serves server over streamable HTTP at /mcp.
//
// Two SDK handlers share the one *mcp.Server:
//
//   - Requests whose Mcp-Protocol-Version header is 2026-07-28 or later go
//     to a Stateless handler: no Mcp-Session-Id, every POST is a complete
//     request lifecycle (SEP-2575). The SDK only accepts the new protocol
//     in stateless mode.
//   - Everything else (initialize, and requests from 2025-11-25-era
//     clients) goes to a stateful handler, preserving sessions, the GET
//     SSE stream, and server-to-client requests such as elicitation.
//
// The SDK's DNS-rebinding protection stays on (requests to a loopback
// listener must carry a loopback Host), and http.CrossOriginProtection
// rejects cross-site browser requests.
func NewHTTPHandler(server *mcp.Server, opts HTTPOptions) (http.Handler, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.SessionTimeout == 0 {
		opts.SessionTimeout = 30 * time.Minute
	}
	getServer := func(*http.Request) *mcp.Server { return server }
	stateless := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		Logger:                       opts.Logger,
		PropagateRequestCancellation: true,
	})
	stateful := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Logger:         opts.Logger,
		SessionTimeout: opts.SessionTimeout,
	})
	endpoint := ProtocolRouter(stateless, stateful)

	cop := http.NewCrossOriginProtection()
	for _, o := range opts.TrustedOrigins {
		if err := cop.AddTrustedOrigin(o); err != nil {
			return nil, fmt.Errorf("trusted origin %q: %w", o, err)
		}
	}

	mux := http.NewServeMux()
	if opts.Auth != nil {
		wrapped, err := withAuth(mux, endpoint, opts.Auth)
		if err != nil {
			return nil, err
		}
		endpoint = wrapped
	}
	mux.Handle(PathMCP, cop.Handler(endpoint))
	if opts.Health != nil {
		mux.Handle("GET /healthz", opts.Health.LivenessHandler())
		mux.Handle("GET /readyz", opts.Health.ReadinessHandler())
	}
	if opts.Consent != nil {
		mux.Handle("GET "+PathConsent, ConsentHandler(opts.Consent))
	}
	return mux, nil
}

// ProtocolRouter dispatches 2026-07-28+ requests to stateless and all others
// to stateful. Protocol versions are ISO dates, so string comparison orders
// them correctly.
func ProtocolRouter(stateless, stateful http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Protocol-Version") >= statelessProtocolVersion {
			stateless.ServeHTTP(w, r)
			return
		}
		stateful.ServeHTTP(w, r)
	})
}

// withAuth registers protected resource metadata on mux and returns endpoint
// wrapped in bearer-token verification.
func withAuth(mux *http.ServeMux, endpoint http.Handler, a *AuthOptions) (http.Handler, error) {
	if a.Verifier == nil || a.ResourceURL == "" {
		return nil, fmt.Errorf("auth: Verifier and ResourceURL are required")
	}
	res, err := url.Parse(a.ResourceURL)
	if err != nil || res.Scheme == "" || res.Host == "" {
		return nil, fmt.Errorf("auth: ResourceURL %q must be an absolute URL", a.ResourceURL)
	}
	scopes := a.RequiredScopes
	if len(scopes) == 0 {
		scopes = []string{ScopeRead}
	}
	// RFC 9728 §3.1: the metadata URL inserts the well-known segment before
	// the resource path.
	prmPath := PathPRM + res.Path
	prmURL := (&url.URL{Scheme: res.Scheme, Host: res.Host, Path: prmPath}).String()

	prm := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               a.ResourceURL,
		AuthorizationServers:   a.AuthorizationServers,
		ScopesSupported:        []string{ScopeRead, ScopeWrite},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "golang-build users MCP server",
	})
	mux.Handle(prmPath, prm)
	if prmPath != PathPRM {
		mux.Handle(PathPRM, prm)
	}
	return auth.RequireBearerToken(TokenVerifier(a.Verifier, a.ResourceURL), &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: prmURL,
		Scopes:              scopes,
	})(endpoint), nil
}
