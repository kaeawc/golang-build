package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/mcpserver"
)

const (
	transportStdio = "stdio"
	transportHTTP  = "http"
	backendMemory  = "memory"
	backendPG      = "postgres"
)

// config is the fully resolved runtime configuration. Flags win over
// environment variables, which win over defaults. Secrets are read from the
// environment only, never from flags, so they do not leak into ps output.
type config struct {
	transport       string
	addr            string
	backend         string
	databaseURL     string
	resourceURL     string
	authServers     []string
	trustedOrigins  []string
	authSecret      string
	stateSecret     string
	insecureBind    bool
	logLevel        slog.Level
	logJSON         bool
	shutdownTimeout time.Duration

	mintToken    bool
	tokenSubject string
	tokenScopes  []string
	tokenTTL     time.Duration
}

func parseConfig(args []string, r env.Reader, stderr io.Writer) (config, error) {
	var c config
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: mcp [flags]\n\nServes the golang-build users directory over the Model Context Protocol.\n\nEnvironment: MCP_TRANSPORT, MCP_ADDR, MCP_BACKEND, DATABASE_URL, MCP_RESOURCE_URL,\nMCP_AUTHORIZATION_SERVERS, MCP_TRUSTED_ORIGINS, MCP_AUTH_SECRET (enables bearer auth),\nMCP_STATE_SECRET (shared requestState key for multi-instance HTTP), MCP_LOG_LEVEL.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.StringVar(&c.transport, "transport", envOr(r, "MCP_TRANSPORT", transportStdio), "transport: stdio or http")
	fs.StringVar(&c.addr, "addr", envOr(r, "MCP_ADDR", "127.0.0.1:8081"), "HTTP listen address")
	fs.StringVar(&c.backend, "backend", envOr(r, "MCP_BACKEND", backendMemory), "user store: memory (seeded fake) or postgres (DATABASE_URL)")
	fs.StringVar(&c.resourceURL, "resource-url", env.GetTrim(r, "MCP_RESOURCE_URL"), "canonical public URL of the MCP endpoint (token audience); default http://<addr>/mcp")
	authServers := fs.String("authorization-servers", env.GetTrim(r, "MCP_AUTHORIZATION_SERVERS"), "comma-separated authorization server issuer URLs for protected resource metadata")
	origins := fs.String("trusted-origins", env.GetTrim(r, "MCP_TRUSTED_ORIGINS"), "comma-separated browser origins allowed to call the HTTP endpoint cross-origin")
	fs.BoolVar(&c.insecureBind, "insecure-bind", false, "allow a non-loopback -addr without MCP_AUTH_SECRET (not recommended)")
	level := fs.String("log-level", envOr(r, "MCP_LOG_LEVEL", "info"), "log level: debug, info, warn, error")
	fs.BoolVar(&c.logJSON, "log-json", false, "log as JSON instead of text (always to stderr)")
	fs.DurationVar(&c.shutdownTimeout, "shutdown-timeout", 10*time.Second, "graceful HTTP shutdown budget")
	fs.BoolVar(&c.mintToken, "mint-token", false, "print a development bearer token signed with MCP_AUTH_SECRET and exit")
	fs.StringVar(&c.tokenSubject, "token-subject", "dev-user", "subject (user ID) for -mint-token")
	scopes := fs.String("token-scopes", mcpserver.ScopeRead+","+mcpserver.ScopeWrite, "comma-separated scopes for -mint-token")
	fs.DurationVar(&c.tokenTTL, "token-ttl", time.Hour, "lifetime for -mint-token")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() > 0 {
		return c, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	c.databaseURL = env.GetTrim(r, "DATABASE_URL")
	c.authSecret = env.Get(r, "MCP_AUTH_SECRET")
	c.stateSecret = env.Get(r, "MCP_STATE_SECRET")
	c.authServers = splitList(*authServers)
	c.trustedOrigins = splitList(*origins)
	c.tokenScopes = splitList(*scopes)
	if err := c.logLevel.UnmarshalText([]byte(*level)); err != nil {
		return c, fmt.Errorf("-log-level: %w", err)
	}
	if c.resourceURL == "" {
		c.resourceURL = "http://" + c.addr + mcpserver.PathMCP
	}
	return c, c.validate()
}

func (c config) validate() error {
	if err := c.validateModes(); err != nil {
		return err
	}
	if c.mintToken && c.authSecret == "" {
		return errors.New("-mint-token requires MCP_AUTH_SECRET")
	}
	if u, err := url.Parse(c.resourceURL); err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("-resource-url %q must be an absolute URL", c.resourceURL)
	}
	if c.exposedWithoutAuth() {
		return fmt.Errorf("refusing to serve unauthenticated MCP on non-loopback address %q: set MCP_AUTH_SECRET or pass -insecure-bind", c.addr)
	}
	return nil
}

// exposedWithoutAuth reports an HTTP listener reachable beyond loopback
// with no bearer-token protection and no explicit opt-out.
func (c config) exposedWithoutAuth() bool {
	return c.transport == transportHTTP && c.authSecret == "" && !c.insecureBind && !isLoopback(c.addr)
}

func (c config) validateModes() error {
	if c.transport != transportStdio && c.transport != transportHTTP {
		return fmt.Errorf("-transport must be %s or %s, got %q", transportStdio, transportHTTP, c.transport)
	}
	switch c.backend {
	case backendMemory:
		return nil
	case backendPG:
		if c.databaseURL == "" {
			return errors.New("-backend=postgres requires DATABASE_URL")
		}
		return nil
	default:
		return fmt.Errorf("-backend must be %s or %s, got %q", backendMemory, backendPG, c.backend)
	}
}

// consentBaseURL derives the origin hosting /consent from the resource URL.
func (c config) consentBaseURL() string {
	u, err := url.Parse(c.resourceURL)
	if err != nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: mcpserver.PathConsent}).String()
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envOr(r env.Reader, name, def string) string {
	if v := env.GetTrim(r, name); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
