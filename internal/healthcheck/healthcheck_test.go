package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmptyRegistryIsHealthy(t *testing.T) {
	r := NewRegistry()
	if rep := r.CheckLiveness(context.Background()); rep.Status != "ok" {
		t.Errorf("liveness with no probes: %+v", rep)
	}
	if rep := r.CheckReadiness(context.Background()); rep.Status != "ok" {
		t.Errorf("readiness with no probes: %+v", rep)
	}
}

func TestProbePassesAndFails(t *testing.T) {
	r := NewRegistry()
	r.RegisterReadiness("db", func(context.Context) error { return nil })
	r.RegisterReadiness("cache", func(context.Context) error { return errors.New("conn refused") })

	rep := r.CheckReadiness(context.Background())
	if rep.Status != "fail" {
		t.Errorf("Status = %q, want fail", rep.Status)
	}
	if len(rep.Checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(rep.Checks))
	}
	// Probes are sorted by name: cache, db.
	if rep.Checks[0].Name != "cache" || rep.Checks[0].OK {
		t.Errorf("checks[0] = %+v", rep.Checks[0])
	}
	if rep.Checks[1].Name != "db" || !rep.Checks[1].OK {
		t.Errorf("checks[1] = %+v", rep.Checks[1])
	}
	if !strings.Contains(rep.Checks[0].Error, "conn refused") {
		t.Errorf("error = %q", rep.Checks[0].Error)
	}
}

func TestLivenessAndReadinessAreIndependent(t *testing.T) {
	r := NewRegistry()
	r.RegisterLiveness("alive", func(context.Context) error { return nil })
	r.RegisterReadiness("ready", func(context.Context) error { return errors.New("warming up") })

	if rep := r.CheckLiveness(context.Background()); rep.Status != "ok" {
		t.Errorf("liveness should pass: %+v", rep)
	}
	if rep := r.CheckReadiness(context.Background()); rep.Status != "fail" {
		t.Errorf("readiness should fail: %+v", rep)
	}
}

func TestSetShuttingDownFailsReadiness(t *testing.T) {
	r := NewRegistry()
	r.RegisterReadiness("db", func(context.Context) error { return nil })

	if !r.IsShuttingDown() && r.CheckReadiness(context.Background()).Status != "ok" {
		t.Fatal("pre-shutdown readiness should pass")
	}

	r.SetShuttingDown()
	if !r.IsShuttingDown() {
		t.Error("IsShuttingDown should be true")
	}
	rep := r.CheckReadiness(context.Background())
	if rep.Status != "fail" {
		t.Errorf("post-shutdown Status = %q, want fail", rep.Status)
	}
	if !rep.ShuttingDown {
		t.Error("ShuttingDown flag should be set")
	}
	if len(rep.Checks) != 0 {
		t.Errorf("probes should be skipped during shutdown, got %d checks", len(rep.Checks))
	}

	// Liveness should still pass — process is alive even while draining.
	if rep := r.CheckLiveness(context.Background()); rep.Status != "ok" {
		t.Errorf("liveness during shutdown: %+v", rep)
	}
}

func TestProbeTimeout(t *testing.T) {
	r := NewRegistry(WithProbeTimeout(20 * time.Millisecond))
	r.RegisterReadiness("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	rep := r.CheckReadiness(context.Background())
	if rep.Status != "fail" {
		t.Errorf("Status = %q, want fail", rep.Status)
	}
	if !strings.Contains(rep.Checks[0].Error, "deadline") {
		t.Errorf("error = %q, want deadline-related", rep.Checks[0].Error)
	}
}

func TestProbePanicIsContained(t *testing.T) {
	r := NewRegistry()
	r.RegisterReadiness("buggy", func(context.Context) error { panic("boom") })

	rep := r.CheckReadiness(context.Background())
	if rep.Status != "fail" {
		t.Errorf("Status = %q, want fail", rep.Status)
	}
	if !strings.Contains(rep.Checks[0].Error, "panic") {
		t.Errorf("error = %q, want to mention panic", rep.Checks[0].Error)
	}
}

func TestUnregister(t *testing.T) {
	r := NewRegistry()
	r.RegisterLiveness("a", func(context.Context) error { return nil })
	r.RegisterReadiness("a", func(context.Context) error { return nil })

	if !r.Unregister("a") {
		t.Error("Unregister should report removal")
	}
	if r.Unregister("a") {
		t.Error("Unregister of absent name should return false")
	}
	rep := r.CheckLiveness(context.Background())
	if len(rep.Checks) != 0 {
		t.Errorf("after Unregister, got %d checks, want 0", len(rep.Checks))
	}
}

func TestHandlerStatusCodes(t *testing.T) {
	r := NewRegistry()
	r.RegisterReadiness("db", func(context.Context) error { return nil })

	srv := httptest.NewServer(r.ReadinessHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var rep Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Status != "ok" {
		t.Errorf("body Status = %q", rep.Status)
	}
}

func TestHandler503OnFailure(t *testing.T) {
	r := NewRegistry()
	r.RegisterReadiness("db", func(context.Context) error { return errors.New("down") })

	srv := httptest.NewServer(r.ReadinessHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestRegisterReplacesByName(t *testing.T) {
	r := NewRegistry()
	r.RegisterReadiness("db", func(context.Context) error { return errors.New("v1") })
	r.RegisterReadiness("db", func(context.Context) error { return nil })

	rep := r.CheckReadiness(context.Background())
	if rep.Status != "ok" {
		t.Errorf("Status = %q, want ok (second register should win)", rep.Status)
	}
}

func TestProbesRunInParallel(t *testing.T) {
	r := NewRegistry(WithProbeTimeout(time.Second))
	const n = 4
	const probeDelay = 80 * time.Millisecond
	for i := 0; i < n; i++ {
		name := string(rune('a' + i))
		r.RegisterReadiness(name, func(context.Context) error {
			time.Sleep(probeDelay)
			return nil
		})
	}

	start := time.Now()
	rep := r.CheckReadiness(context.Background())
	elapsed := time.Since(start)

	if rep.Status != StatusOK {
		t.Errorf("Status = %q, want %q", rep.Status, StatusOK)
	}
	// Sequential would take n*probeDelay = 320ms; parallel should be ~probeDelay.
	if elapsed >= 2*probeDelay {
		t.Errorf("elapsed %v ≥ %v — probes did not run in parallel", elapsed, 2*probeDelay)
	}
}

func TestConcurrentRegisterAndCheck(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	var checks atomic.Int32

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			r.RegisterReadiness(string(rune('a'+(i%26))), func(context.Context) error { return nil })
		}(i)
		go func() {
			defer wg.Done()
			r.CheckReadiness(context.Background())
			checks.Add(1)
		}()
	}
	wg.Wait()
	if checks.Load() != 50 {
		t.Errorf("checks ran %d times, want 50", checks.Load())
	}
}
