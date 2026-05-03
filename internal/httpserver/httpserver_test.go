package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// runServer starts the server in a goroutine bound to a free port (":0") with
// signal handling disabled, returning the server, its base URL, and a stop
// function that cancels and waits for Run to return.
func runServer(t *testing.T, h http.Handler, opts ...func(*Config)) (*Server, string, func() error) {
	t.Helper()
	cfg := Config{
		Addr:            "127.0.0.1:0",
		Handler:         h,
		ShutdownTimeout: 2 * time.Second,
		Signals:         []os.Signal{},
	}
	for _, fn := range opts {
		fn(&cfg)
	}
	srv := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	// Wait for the listener to bind (up to ~500ms).
	deadline := time.Now().Add(500 * time.Millisecond)
	for srv.Addr() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.Addr() == nil {
		cancel()
		t.Fatal("server never bound a listener")
	}
	url := "http://" + srv.Addr().String()
	stop := func() error {
		cancel()
		select {
		case err := <-runErr:
			return err
		case <-time.After(3 * time.Second):
			return errors.New("Run did not return within 3s")
		}
	}
	return srv, url, stop
}

// importing os here to satisfy compiler — stub out via init in tests below.
var _ = http.MethodGet // keep imports minimal

func TestServesAndShutsDown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	_, url, stop := runServer(t, mux)

	resp, err := http.Get(url + "/ok")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok\n" {
		t.Errorf("got status=%d body=%q", resp.StatusCode, body)
	}

	if err := stop(); err != nil {
		t.Errorf("Run returned %v on graceful shutdown", err)
	}
}

func TestRunReturnsListenError(t *testing.T) {
	// First server takes the port.
	mux := http.NewServeMux()
	srv1, url, stop := runServer(t, mux)
	defer stop()

	// Second server tries the same port.
	srv2 := New(Config{Addr: srv1.Addr().String(), Handler: mux, Signals: []os.Signal{}})
	err := srv2.Run(context.Background())
	if err == nil {
		t.Fatal("expected listen error binding to occupied port")
	}
	_ = url
}

func TestNilHandler(t *testing.T) {
	srv := New(Config{Addr: "127.0.0.1:0", Signals: []os.Signal{}})
	err := srv.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for nil Handler")
	}
}

func TestOnShutdownRuns(t *testing.T) {
	var ran atomic.Bool
	mux := http.NewServeMux()
	_, _, stop := runServer(t, mux, func(c *Config) {
		c.OnShutdown = func(context.Context) error {
			ran.Store(true)
			return nil
		}
	})
	if err := stop(); err != nil {
		t.Errorf("stop: %v", err)
	}
	if !ran.Load() {
		t.Error("OnShutdown did not run")
	}
}

func TestOnShutdownErrorIsReturned(t *testing.T) {
	mux := http.NewServeMux()
	want := errors.New("flush failed")
	_, _, stop := runServer(t, mux, func(c *Config) {
		c.OnShutdown = func(context.Context) error { return want }
	})
	err := stop()
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want it to wrap %v", err, want)
	}
}

func TestAddrBeforeRunIsNil(t *testing.T) {
	srv := New(Config{Addr: "127.0.0.1:0", Handler: http.NewServeMux()})
	if a := srv.Addr(); a != nil {
		t.Errorf("Addr() before Run = %v, want nil", a)
	}
}

func TestShutdownDrainsInflight(t *testing.T) {
	started := make(chan struct{})
	hold := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-hold
		w.WriteHeader(204)
	})
	_, url, stop := runServer(t, mux, func(c *Config) {
		c.ShutdownTimeout = 5 * time.Second
	})

	respCh := make(chan int, 1)
	go func() {
		resp, err := http.Get(url + "/slow")
		if err != nil {
			respCh <- -1
			return
		}
		resp.Body.Close()
		respCh <- resp.StatusCode
	}()

	<-started
	// Trigger shutdown while the request is in flight.
	go func() { _ = stop() }()
	close(hold)

	select {
	case status := <-respCh:
		if status != 204 {
			t.Errorf("inflight request got %d, want 204", status)
		}
	case <-time.After(3 * time.Second):
		t.Error("inflight request did not complete during shutdown drain")
	}
}

func TestDefaultsApplied(t *testing.T) {
	srv := New(Config{Addr: ":0", Handler: http.NewServeMux()})
	if srv.cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("default ShutdownTimeout = %v, want 10s", srv.cfg.ShutdownTimeout)
	}
	if srv.cfg.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("default ReadHeaderTimeout = %v, want 10s", srv.cfg.ReadHeaderTimeout)
	}
}
