package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kaeawc/golang-build/internal/env"
)

func TestEnabled(t *testing.T) {
	for name, tt := range map[string]struct {
		vars env.Map
		want bool
	}{
		"nothing set":        {env.Map{}, false},
		"endpoint":           {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"}, true},
		"traces endpoint":    {env.Map{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://c/v1/traces"}, true},
		"metrics endpoint":   {env.Map{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://c/v1/metrics"}, true},
		"sdk disabled":       {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c", "OTEL_SDK_DISABLED": "TRUE"}, false},
		"blank endpoint":     {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "  "}, false},
		"sdk disabled false": {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c", "OTEL_SDK_DISABLED": "false"}, true},
	} {
		if got := Enabled(tt.vars); got != tt.want {
			t.Errorf("%s: Enabled = %v, want %v", name, got, tt.want)
		}
	}
}

// restoreGlobals puts the otel globals back after a test that installs
// providers.
func restoreGlobals(t *testing.T) {
	tp, mp, prop := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(tp)
		otel.SetMeterProvider(mp)
		otel.SetTextMapPropagator(prop)
	})
}

func TestSetupDisabledLeavesGlobalsAlone(t *testing.T) {
	restoreGlobals(t)
	before := otel.GetTracerProvider()
	stop, err := Setup(t.Context(), env.Map{}, Service{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if otel.GetTracerProvider() != before {
		t.Error("disabled Setup replaced the global tracer provider")
	}
	if err := stop(t.Context()); err != nil {
		t.Errorf("no-op shutdown: %v", err)
	}
}

// TestSetupExportsOverOTLP runs Setup against a fake OTLP/HTTP receiver
// and checks that a span and the metrics reach it on shutdown.
func TestSetupExportsOverOTLP(t *testing.T) {
	restoreGlobals(t)
	var (
		mu    sync.Mutex
		paths = map[string]int{}
	)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	// The exporters read the process environment.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	stop, err := Setup(t.Context(), env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL}, Service{Name: "test", Environment: "development"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatalf("global tracer provider = %T, want the SDK provider", otel.GetTracerProvider())
	}
	if fields := otel.GetTextMapPropagator().Fields(); len(fields) == 0 {
		t.Error("no propagator installed")
	}
	_, span := otel.Tracer("test").Start(t.Context(), "work")
	span.End()
	if err := stop(t.Context()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if paths["/v1/traces"] == 0 || paths["/v1/metrics"] == 0 {
		t.Errorf("collector saw %v, want /v1/traces and /v1/metrics", paths)
	}
}

func TestLogHandlerAddsTraceContext(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(LogHandler(slog.NewJSONHandler(&buf, nil))).With("svc", "x").WithGroup("g")

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()

	log.InfoContext(ctx, "with span", "k", 1)
	log.InfoContext(context.Background(), "without span")

	dec := json.NewDecoder(&buf)
	var withSpan, withoutSpan map[string]any
	if err := dec.Decode(&withSpan); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&withoutSpan); err != nil {
		t.Fatal(err)
	}
	g, _ := withSpan["g"].(map[string]any)
	sc := span.SpanContext()
	if g["trace_id"] != sc.TraceID().String() || g["span_id"] != sc.SpanID().String() || withSpan["svc"] != "x" {
		t.Errorf("record with span = %v, want trace_id %s span_id %s", withSpan, sc.TraceID(), sc.SpanID())
	}
	if g2, _ := withoutSpan["g"].(map[string]any); g2 != nil && g2["trace_id"] != nil {
		t.Errorf("record without span has trace_id: %v", withoutSpan)
	}
}
