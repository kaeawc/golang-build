// Package httpx provides a Client interface over net/http with a Real impl
// for production and a Fake for tests that script responses by URL pattern.
//
// Inject Client into anything that makes outbound HTTP calls. Tests substitute
// Fake; production uses Real backed by *http.Client. Pair with retry.Executor
// when callers need retries with backoff.
package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Request is a transport-agnostic outbound request.
type Request struct {
	Method  string // empty == GET
	URL     string
	Headers http.Header
	Body    []byte
}

// Response carries the response status, headers, and body.
type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
}

// Client is the outbound HTTP interface.
type Client interface {
	Do(ctx context.Context, req Request) (Response, error)
}

// Real is a Client backed by *http.Client. The zero value uses
// http.DefaultClient with a 30s timeout.
type Real struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// NewReal returns a Real with a sensible default timeout.
func NewReal(timeout time.Duration) *Real {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Real{HTTP: &http.Client{Timeout: timeout}, Timeout: timeout}
}

// Do executes req. Non-2xx responses are returned without an error so the
// caller decides their failure policy.
func (r *Real) Do(ctx context.Context, req Request) (Response, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	hreq, err := http.NewRequestWithContext(ctx, method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return Response{}, fmt.Errorf("httpx: build request: %w", err)
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			hreq.Header.Add(k, v)
		}
	}

	client := r.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return Response{}, fmt.Errorf("httpx: %s %s: %w", method, req.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("httpx: read body: %w", err)
	}
	return Response{
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    body,
	}, nil
}

// Match selects which request a stubbed response applies to.
type Match func(req Request) bool

// MatchExact returns a Match that fires on exact (method, url) equality.
func MatchExact(method, url string) Match {
	if method == "" {
		method = http.MethodGet
	}
	return func(r Request) bool {
		rm := r.Method
		if rm == "" {
			rm = http.MethodGet
		}
		return rm == method && r.URL == url
	}
}

// MatchPrefix returns a Match that fires on URL prefix (any method).
func MatchPrefix(prefix string) Match {
	return func(r Request) bool { return strings.HasPrefix(r.URL, prefix) }
}

// Stub is a scripted response or an error.
type Stub struct {
	Match    Match
	Response Response
	Err      error
	// Times caps how many requests this stub answers. Zero == unlimited.
	Times int
	used  int
}

// Fake is a scripted Client. Stubs are matched in registration order; the
// first match wins. Every call is recorded in Calls for assertion.
type Fake struct {
	mu    sync.Mutex
	stubs []*Stub
	Calls []Request
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{} }

// Stub registers a scripted response. Returns the *Stub for chaining (e.g.
// to inspect remaining call budget after the test).
func (f *Fake) Stub(s Stub) *Stub {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := s
	f.stubs = append(f.stubs, &cp)
	return &cp
}

// On is a convenience wrapper for the common (match, status, body) case.
func (f *Fake) On(match Match, status int, body []byte) *Stub {
	return f.Stub(Stub{Match: match, Response: Response{Status: status, Body: body}})
}

// Do returns a scripted response or an error if no stub matches.
func (f *Fake) Do(_ context.Context, req Request) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, req)
	for _, s := range f.stubs {
		if s.Match == nil || !s.Match(req) {
			continue
		}
		if s.Times > 0 && s.used >= s.Times {
			continue
		}
		s.used++
		if s.Err != nil {
			return Response{}, s.Err
		}
		return s.Response, nil
	}
	return Response{}, fmt.Errorf("httpx.Fake: no stub for %s %s", methodOrGet(req), req.URL)
}

// CallsFor returns the subset of recorded calls matching m.
func (f *Fake) CallsFor(m Match) []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Request
	for _, c := range f.Calls {
		if m(c) {
			out = append(out, c)
		}
	}
	return out
}

func methodOrGet(r Request) string {
	if r.Method == "" {
		return http.MethodGet
	}
	return r.Method
}

// ErrNoStub is returned by Fake.Do when no stub matches a request. Callers
// can errors.Is-check this.
var ErrNoStub = errors.New("httpx.Fake: no matching stub")
