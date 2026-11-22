# MCP server (`cmd/mcp`, `internal/mcpserver`)

This repo ships a [Model Context Protocol](https://modelcontextprotocol.io)
server for the users directory (the same `users` table that `cmd/server`
serves), plus a CLI client that shows off every feature.

- **Spec target:** revision **2026-07-28**, the first stateless revision
  ([SEP-2575](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2575)).
- **Compatibility:** clients still using the `initialize` handshake
  (2025-11-25, 2025-06-18, 2025-03-26) keep working. The SDK negotiates the
  version for each client, and the same handler code serves both kinds.
- **SDK:** `github.com/modelcontextprotocol/go-sdk` **v1.8.0**, the latest
  stable release. There is no `/v2` module, and `v1.9.0-pre.1` is a
  prerelease, so it is not used.

## Architecture

```
                 ┌──────────────── cmd/mcp ────────────────┐
 stdio ─────────▶│ mcp.StdioTransport ─┐                   │
                 │                     ├─▶ *mcp.Server ◀── internal/mcpserver.New(Deps)
 HTTP  ─────────▶│ NewHTTPHandler ─────┘        │            tools / resources / prompts /
  /mcp           │   CrossOriginProtection      │            completion / SetCacheable /
                 │   RequireBearerToken (opt.)  │            LoggingMiddleware
                 │   ProtocolRouter             │
                 │    ├─ Mcp-Protocol-Version ≥ 2026-07-28 ─▶ StreamableHTTPHandler{Stateless}
                 │    └─ everything else ─────────────────▶ StreamableHTTPHandler{stateful}
                 └──────────────────────────────┼────────────────────────────┘
                                                ▼
                         UserStore ── MemoryStore (fake, seeded demo users)
                                   └─ PgxStore   (internal/db sqlc queries over pgxpool)
```

- `internal/mcpserver` holds everything: `New(Deps) *mcp.Server`,
  `NewHTTPHandler(server, HTTPOptions)`, the `UserStore` interface with its
  live and fake implementations, the auth adapter, and the slog middleware.
  Like `cmd/admin`'s `Backend` and `cmd/migrate`'s `Migrator`, it runs with
  no infrastructure (`MemoryStore`) or against Postgres (`PgxStore`).
- `Deps` brings in the repo's existing building blocks:
  - `internal/clock` for time.
  - `internal/idgen` for elicitation IDs.
  - `internal/healthcheck` for the `health_check` tool and `/readyz`.
  - `internal/paginator` for opaque `list_users` cursors.
  - `internal/tokens` to sign `requestState` and to verify bearer tokens.
  - `internal/db` for `PgxStore`.
  - `internal/env` for `cmd/mcp` configuration.
- **Why two HTTP handlers:** the SDK only accepts 2026-07-28 requests when
  `StreamableHTTPOptions.Stateless` is true. A stateless handler cannot make
  server-to-client requests, but legacy clients need those for elicitation.
  `ProtocolRouter` therefore picks a handler from the
  `Mcp-Protocol-Version` header (SEP-2243). Both handlers share a single
  `*mcp.Server`, so features, list-changed notifications, and resource
  subscriptions behave the same for both.

## Feature → spec mapping

"Tests" are in `internal/mcpserver` unless noted. Most tests run as table
tests against both `2026-07-28` and `2025-11-25` clients, using the real SDK
client over `mcp.NewInMemoryTransports`.

| Feature | Spec section / SEP | Where | Tests |
|---|---|---|---|
| stdio transport | Basic › Transports › stdio | `cmd/mcp/main.go` (`mcp.StdioTransport`, logs to stderr) | e2e run below; `cmd/mcpclient` `TestDemoInMemory` |
| Streamable HTTP, stateless 2026-07-28 | Transports › Streamable HTTP; SEP-2575, SEP-2243 headers | `http.go` `NewHTTPHandler`, `ProtocolRouter` | `TestHTTPProtocolModes/2026-07-28`, `TestHTTPRawProtocol` |
| Streamable HTTP, stateful legacy sessions | 2025-11-25 Streamable HTTP | same (stateful handler, `SessionTimeout` 30m) | `TestHTTPProtocolModes/2025-11-25`, `cmd/mcp` `TestServeHTTPLifecycle` |
| DNS-rebinding / Origin protection | Security best practices | SDK localhost Host check (on by default) + `http.CrossOriginProtection` | `TestHTTPBrowserProtections` |
| HTTP timeouts, graceful shutdown | n/a | `cmd/mcp/main.go` `serveHTTP` (ReadHeaderTimeout, IdleTimeout, MaxHeaderBytes, bounded `Shutdown`, stream cut-off) | `TestServeHTTPLifecycle` |
| `server/discover`, no handshake | Lifecycle; SEP-2575 | SDK, configured via `ServerOptions` | `TestDiscoveryAndLifecycle`, `TestHTTPRawProtocol/discover` |
| Explicit supported versions | Lifecycle › version negotiation | `ProtocolVersions` → `ServerOptions.SupportedProtocolVersions` | `TestDiscoveryAndLifecycle/excluded version…` |
| Implementation info (name/title/version/icons/websiteUrl) | Lifecycle; per-response `serverInfo` `_meta` | `server.go` `implementation`, `BuildVersion` (runtime/debug) | `TestDiscoveryAndLifecycle` |
| Server instructions | Lifecycle | `server.go` `instructions` | `TestDiscoveryAndLifecycle` |
| Typed tools, inferred input + output schemas | Server › Tools; SEP-2106 | `tools.go` `mcp.AddTool[In,Out]` with `jsonschema` tag descriptions | `TestToolListing` |
| Structured content | Tools › Structured content | every tool returns a typed `Out` | `TestToolCalls` |
| Annotations, titles, icons | Tools › Tool annotations | `tools.go` | `TestToolListing` |
| Tool errors (`isError`) vs protocol errors | Tools › Error handling | plain `error` → `isError`; `*jsonrpc.Error` for tampered `requestState`; unknown tool → JSON-RPC error | `TestToolCalls`, `TestUnknownToolIsProtocolError`, `TestDeleteUserManualRoundTrip` |
| Content: text, image, audio, resource_link, embedded resource | Tools › Tool result | `user_card` (text+PNG+WAV+link), `list_users` (links), `get_user`/`export_users`/`generate_report` (embedded) | `TestToolCalls` |
| Static resources | Server › Resources | `users://directory`, `server://info` | `TestResources` |
| Binary (blob) resources | Resources › Binary content | `assets://logo.png`, `users://{id}/avatar.png` | `TestResources` |
| Resource templates (RFC 6570) | Resources › Templates | `users://{id}`, `users://{id}/avatar.png`; strict parsing in `ParseUserURI` | `TestResources`, `TestParseUserURI`, `FuzzParseUserURI` |
| Resource subscriptions + `resources/updated` | Resources › Subscriptions; `subscriptions/listen` (SEP-2575) | `SubscribeHandler`; `notifyUserChanged` after create/delete | `TestResourceSubscriptions`, `TestSubscribeRejectsUnknownURI`, HTTP: `TestHTTPProtocolModes` |
| List-changed notifications at runtime | Tools/Resources › list_changed | `set_experimental_features` adds/removes `generate_report` + `reports://latest` | `TestListChangedAtRuntime` |
| Prompts: arguments, multi-message, embedded resource | Server › Prompts | `prompts.go` (`summarize_user`, `onboarding_email` with user/assistant/user turns) | `TestPrompts` |
| Completion (prompt args + template params) | Utilities › Completion | `completion.go` table of completers | `TestCompletion` |
| Multi round-trip requests, form elicitation | SEP-2322; Client › Elicitation | `delete_user` returns `InputRequests` + HMAC-signed `RequestState` | `TestDeleteUserConfirmation` (both versions), `TestDeleteUserManualRoundTrip` |
| URL-mode elicitation | Elicitation › URL mode | `connect_calendar` + `/consent` page + `ConsentStore` | `TestConnectCalendarURLElicitation`, `TestConnectCalendarConsentLifecycle`, `TestMemoryConsent`, `TestHTTPAuxEndpoints`, `cmd/mcpclient` `TestRunAgainstHTTP` |
| Legacy-client path for MRTR | SEP-2322 back-compat | SDK `serverMultiRoundTripMiddleware` turns `InputRequests` into `elicitation/create` | the `2025-11-25` rows of the two tests above |
| Progress notifications | Utilities › Progress | `export_users` | `TestExportProgress` (synctest, exact virtual timing) |
| Cancellation | Utilities › Cancellation | `export_users` honours `ctx`; HTTP `PropagateRequestCancellation` | `TestExportCancellation` (synctest) |
| Pagination | Utilities › Pagination | `ServerOptions.PageSize` (default 5); `list_users` cursors | `TestToolListing`, `TestListUsersPagination` |
| Cacheable list results (`ttlMs`, `cacheScope`) | SEP-2549 | `server.go` `setCacheable` | `TestCacheableResults`, `TestClientHonoursListTTL` (synctest), `TestHTTPRawProtocol` |
| Logging middleware | n/a (server-side observability) | `middleware.go` `LoggingMiddleware` (method, duration, protocol, session, tool, is_error, err) | `TestLoggingMiddleware`, `TestUnknownToolIsProtocolError` |
| Authorization (bearer) | Basic › Authorization (OAuth 2.1 resource server) | `auth.go` `TokenVerifier` (internal/tokens HS256, audience-bound, `UserID`=sub) + `auth.RequireBearerToken`; per-tool `users:write` scope | `TestHTTPAuth`, `TestRequireScope` |
| Protected Resource Metadata | RFC 9728 | `auth.ProtectedResourceMetadataHandler` at `/.well-known/oauth-protected-resource[/mcp]`, `WWW-Authenticate resource_metadata=` | `TestHTTPAuth` |
| Capabilities | Lifecycle › Capabilities | explicit `ServerOptions.Capabilities` (tools/prompts/resources listChanged + subscribe, completions; **no logging**) | `TestDiscoveryAndLifecycle` |
| Ping | Utilities › Ping (removed in 2026-07-28) | SDK | `TestLegacyPing`; 2026 rejection in `TestHTTPRawProtocol` |
| Live Postgres store | n/a | `store_pgx.go` | `TestPgxStoreContract` (skipped unless `MCP_TEST_DATABASE_URL`; `make test-integration` runs it, and `TestPgxStoreContractOnMigratedSchema`, against a container) |

Not implemented on purpose:

- **Extensions (SEP-2133).** No vendor extension has meaning for this
  server, so none are advertised.
- **Legacy HTTP+SSE transport (2024-11-05).** It is not served, and
  2024-11-05 is excluded from `ProtocolVersions`.
- **`URLElicitationRequiredError` (-32042) flow.** The URL-mode example uses
  MRTR input requests instead. The `-32042` flow depends on
  `notifications/elicitation/complete`, which has no channel on stateless
  2026-07-28 requests.
- **Tasks.** The SDK has no tasks support in v1.8.0, and the spec does not
  require it.

## Running it

```bash
go build -o mcp ./cmd/mcp && go build -o mcpclient ./cmd/mcpclient

./mcpclient -yes                     # spawns ./mcp over stdio and runs the 12-step demo
./mcpclient -protocol 2025-11-25 -yes

./mcp -transport=http                # http://127.0.0.1:8081/mcp (+ /healthz /readyz /consent)
./mcpclient -url http://127.0.0.1:8081/mcp -yes demo
./mcpclient -url http://127.0.0.1:8081/mcp call get_user '{"id":1}'
./mcpclient -url http://127.0.0.1:8081/mcp read users://directory
./mcpclient -url http://127.0.0.1:8081/mcp prompt summarize_user user_id=1 style=brief

DATABASE_URL=postgres://... ./mcp -backend=postgres   # live store (run migrations first)
```

Without `-yes`, the client asks before answering an elicitation:
`accept? [y/N]`, then one prompt per form field. For URL mode it prints the
URL and waits until you say you have finished in the browser.

### Authentication (HTTP only, off by default)

```bash
export MCP_AUTH_SECRET=$(openssl rand -hex 32)
./mcp -transport=http -addr=127.0.0.1:8081 -authorization-servers=https://auth.example.com &
TOKEN=$(./mcp -mint-token -token-scopes users:read,users:write)   # dev token; aud = resource URL
./mcpclient -url http://127.0.0.1:8081/mcp -token "$TOKEN" -yes
curl -s http://127.0.0.1:8081/.well-known/oauth-protected-resource/mcp
```

- Every request to `/mcp` needs a Bearer JWT with:
  - an HS256 signature made with `MCP_AUTH_SECRET`;
  - `aud` equal to `-resource-url` (default `http://<addr>/mcp`);
  - a `sub` claim;
  - an `exp` claim;
  - the `users:read` scope.
- `create_user` and `delete_user` also need `users:write`. Without it they
  return a tool error.
- The token `sub` becomes `TokenInfo.UserID`, so the SDK ties stateful
  sessions to that user (session-hijacking mitigation).
- `cmd/mcp` refuses to bind a non-loopback address without
  `MCP_AUTH_SECRET` unless you pass `-insecure-bind`.
- **HS256 limitation:** a real deployment verifies tokens from its
  authorization server, usually RS256 via JWKS. Swap the `Verifier` in
  `AuthOptions` to do that. `internal/auth` was not used: its
  `Authenticator` resolves identity from request-level headers (Cloudflare
  Access, Tailscale WhoIs) rather than bearer tokens.

Multi-instance HTTP deployments must share `MCP_STATE_SECRET`. That key
signs the MRTR `requestState`, and without a shared key a retry that lands
on another replica is rejected. Without it, each process uses a random key.

### Claude Code

`.mcp.json` at the repo root (project scope):

```json
{
  "mcpServers": {
    "golang-build-users": {
      "command": "go",
      "args": ["run", "./cmd/mcp", "-transport=stdio"]
    },
    "golang-build-users-http": {
      "type": "http",
      "url": "http://127.0.0.1:8081/mcp",
      "headers": { "Authorization": "Bearer ${MCP_TOKEN}" }
    }
  }
}
```

Or from the CLI: `claude mcp add golang-build-users -- /abs/path/to/mcp -transport=stdio`,
or `claude mcp add --transport http golang-build-users http://127.0.0.1:8081/mcp`.
Drop the `headers` entry when auth is off.

### Claude Desktop

`claude_desktop_config.json`. Desktop spawns stdio servers outside the repo,
so use a built binary with an absolute path:

```json
{
  "mcpServers": {
    "golang-build-users": {
      "command": "/abs/path/to/mcp",
      "args": ["-transport=stdio", "-log-level=warn"]
    }
  }
}
```

## Deprecated features (SEP-2577)

2026-07-28 deprecates roots, sampling, and logging. They keep working for at
least twelve months, but this server builds nothing new on them:

- **Logging:** the server does not advertise the `logging` capability and
  never sends `notifications/message`. Server-side logs go to stderr through
  slog (`LoggingMiddleware`). For stdio, stdout is reserved for the protocol.
- **Sampling:** no tool asks the client's LLM for a completion. A server
  that needs an LLM should call a provider API directly.
- **Roots:** paths and IDs travel as tool arguments and resource URIs. The
  CLI client declares only elicitation capabilities. It sets
  `ClientOptions.Capabilities` explicitly, which removes the SDK's default
  `roots` capability.
- **Ping:** removed from 2026-07-28 but still answered on legacy sessions.
  Nothing here relies on it, and `KeepAlive` is not used.

## Trade-offs and notes

- **Stateless vs stateful routing** is keyed on the `Mcp-Protocol-Version`
  header. A 2026-07-28 request without that header goes to the stateful
  handler. That handler rejects it with `-32022` and a list of supported
  versions, and the Go client then falls back to `initialize`. The SDK's
  HTTP client always sends the header.
- **The MRTR legacy fallback re-invokes the handler only once.** Handlers
  must finish after one round of input when the client is legacy, so
  `delete_user` and `connect_calendar` are both single-round.
- **`requestState` is a signed JWT.** It carries the tool, the target, and a
  10-minute expiry. No server-side storage is needed, and a state minted for
  one user cannot be replayed against another (`TestDeleteUserManualRoundTrip`).
- **Cache policy:**
  - List and discover results: `public`, 60s.
  - `server://info` and the logo: `public`, 5 minutes.
  - User resources: `private`, 0s. They change often, and subscribers are
    notified of changes.
  - The 2026-07-28 Go client caches list pages for `ttlMs`. List-changed
    notifications clear that cache (`TestClientHonoursListTTL`).
- **No `ReadTimeout`/`WriteTimeout` on `http.Server`.** They would cut off
  SSE responses and long tool calls. `ReadHeaderTimeout` (10s), the SDK's
  4 MiB body limit, and `IdleTimeout` limit abuse instead. On shutdown,
  normal requests get half the shutdown budget. Then the base context is
  cancelled to end long-lived streams (`subscriptions/listen`, legacy GET).
  If the budget runs out, the remaining connections are force-closed. This
  usually means a client reconnected just before the listener closed:
  net/http only reaps such new connections after 5s.
- **The `/consent` page is a stand-in for an OAuth provider.** The
  `ConsentStore` (`MemoryConsent`) tracks each flow in three steps:
  1. `connect_calendar` calls `Begin(id)` when it issues the URL
     elicitation.
  2. The page calls `Complete(id)`.
  3. The tool's retry calls `Consume(id)`.

  The rules that follow from this:
  - Unknown or expired IDs get a 404, and nothing is stored, so
    unauthenticated callers cannot grow the store.
  - Entries expire after `requestStateTTL` (10 minutes), measured on the
    injected `clock.Clock`. Eviction is lazy: an expired entry is removed
    when it is looked up, and all expired entries are swept when the
    10,000-entry cap is reached. Every operation stays O(1) and memory
    stays bounded.
  - A completed consent can be consumed once, so replaying the
    still-validly-signed `requestState` cannot connect again.
  - `MemoryConsent` is per process. Multi-instance deployments need a
    shared store, such as Valkey with a TTL.
  - Over stdio there is no HTTP server and no store: the URL points at
    `calendar.example.com`, and `accept` is taken at face value.
- **Completion and the report helpers read every user.** That is fine for
  the demo directory. A large table needs `LIKE`-prefix queries instead.

### SDK rough edges seen (go-sdk v1.8.0)

- **`ClientSession.Subscribe` and `subscriptions/listen` errors on
  2026-07-28.** `Subscribe` returns before the server registers the
  stream, and it does not report subscription errors such as an unknown
  URI. Both the tests and `cmd/mcpclient` watch for
  `notifications/subscriptions/acknowledged` through client receiving
  middleware, rather than racing the server. `cmd/mcpclient` also checks
  that each ack is for its own stream (by its `notifications` payload),
  because `Connect` opens its own list-changed listen stream whose ack can
  arrive later.
- **Notification handlers run concurrently with response delivery.** A
  `notifications/progress` sent before the result can reach the handler
  after `CallTool` returns, so the CLI waits for `progress == total`.
- **`ClientSession.Ping` on a 2026-07-28 session.** It sends no
  per-request `_meta`, so the server's per-request gating treats it as a
  legacy request and answers it. A raw 2026-07-28 `ping` that carries
  `_meta` is rejected with `-32601` over HTTP 404. The SDK protocol docs say
  that a session where both sides negotiated 2026-07-28 returns
  MethodNotFound.
- **Stateful handlers and 2026-07-28.** A stateful `StreamableHTTPHandler`
  alone cannot serve 2026-07-28 clients: the client silently downgrades to
  2025-11-25. Serving both generations takes two handlers and a router, as
  this repo does.
- **`*jsonrpc.Error` from typed tool handlers.** The SDK recognises it with
  a type assertion, not `errors.As`, so wrapping the error turns it into an
  `isError` result instead of a protocol error.
