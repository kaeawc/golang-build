package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRealDoesGetAndReadsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("server saw method %q", r.Method)
		}
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()

	c := NewReal(time.Second)
	resp, err := c.Do(context.Background(), Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != 202 {
		t.Errorf("status = %d, want 202", resp.Status)
	}
	if string(resp.Body) != "hello" {
		t.Errorf("body = %q, want hello", resp.Body)
	}
	if resp.Headers.Get("X-Test") != "yes" {
		t.Errorf("missing X-Test header")
	}
}

func TestRealHonorsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	c := NewReal(time.Second)
	_, err := c.Do(ctx, Request{URL: srv.URL})
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

func TestFakeMatchesAndRecords(t *testing.T) {
	f := NewFake()
	f.On(MatchExact(http.MethodPost, "https://api.example/users"), 201, []byte(`{"id":1}`))

	resp, err := f.Do(context.Background(), Request{Method: http.MethodPost, URL: "https://api.example/users"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != 201 || string(resp.Body) != `{"id":1}` {
		t.Errorf("got %+v", resp)
	}
	if len(f.Calls) != 1 {
		t.Errorf("expected 1 call recorded, got %d", len(f.Calls))
	}
}

func TestFakeNoMatch(t *testing.T) {
	f := NewFake()
	f.On(MatchExact("GET", "https://x"), 200, nil)

	_, err := f.Do(context.Background(), Request{URL: "https://y"})
	if err == nil {
		t.Fatal("expected error for unmatched request")
	}
}

func TestFakeStubError(t *testing.T) {
	f := NewFake()
	want := errors.New("network down")
	f.Stub(Stub{Match: MatchPrefix("https://"), Err: want})

	_, err := f.Do(context.Background(), Request{URL: "https://anything"})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestFakeStubTimes(t *testing.T) {
	f := NewFake()
	f.Stub(Stub{Match: MatchPrefix("https://"), Response: Response{Status: 200}, Times: 2})
	f.Stub(Stub{Match: MatchPrefix("https://"), Response: Response{Status: 503}})

	r1, _ := f.Do(context.Background(), Request{URL: "https://x"})
	r2, _ := f.Do(context.Background(), Request{URL: "https://x"})
	r3, _ := f.Do(context.Background(), Request{URL: "https://x"})

	if r1.Status != 200 || r2.Status != 200 || r3.Status != 503 {
		t.Errorf("statuses = %d %d %d, want 200 200 503", r1.Status, r2.Status, r3.Status)
	}
}

func TestMatchPrefix(t *testing.T) {
	m := MatchPrefix("https://api.example/")
	if !m(Request{URL: "https://api.example/users"}) {
		t.Error("expected match")
	}
	if m(Request{URL: "https://other.example/"}) {
		t.Error("unexpected match")
	}
}

func TestCallsFor(t *testing.T) {
	f := NewFake()
	f.On(MatchPrefix("https://"), 200, nil)

	_, _ = f.Do(context.Background(), Request{URL: "https://a"})
	_, _ = f.Do(context.Background(), Request{URL: "https://b"})
	_, _ = f.Do(context.Background(), Request{URL: "https://a/x"})

	hits := f.CallsFor(MatchPrefix("https://a"))
	if len(hits) != 2 {
		t.Errorf("got %d calls for prefix https://a, want 2", len(hits))
	}
}
