package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kaeawc/golang-build/internal/env"
)

func TestSignals(t *testing.T) {
	for name, tt := range map[string]struct {
		vars            env.Map
		traces, metrics bool
	}{
		"nothing set":           {env.Map{}, false, false},
		"shared endpoint":       {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4318"}, true, true},
		"traces endpoint only":  {env.Map{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://c/v1/traces"}, true, false},
		"metrics endpoint only": {env.Map{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://c/v1/metrics"}, false, true},
		"metrics exporter none": {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c", "OTEL_METRICS_EXPORTER": "none"}, true, false},
		"traces exporter none":  {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c", "OTEL_TRACES_EXPORTER": "NONE"}, false, true},
		"sdk disabled":          {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c", "OTEL_SDK_DISABLED": "TRUE"}, false, false},
		"sdk disabled false":    {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c", "OTEL_SDK_DISABLED": "false"}, true, true},
		"blank endpoint":        {env.Map{"OTEL_EXPORTER_OTLP_ENDPOINT": "  "}, false, false},
	} {
		traces, metrics := Signals(tt.vars)
		if traces != tt.traces || metrics != tt.metrics {
			t.Errorf("%s: Signals = %v/%v, want %v/%v", name, traces, metrics, tt.traces, tt.metrics)
		}
	}
}

// clearOTelEnv unsets the variables Setup reads, restores the otel globals
// afterwards, and returns a capture of the paths a fake OTLP receiver saw.
func clearOTelEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_SDK_DISABLED", "OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES",
	} {
		t.Setenv(k, "")
	}
	tp, mp, prop, eh := otel.GetTracerProvider(), otel.GetMeterProvider(), otel.GetTextMapPropagator(), otel.GetErrorHandler()
	t.Cleanup(func() {
		otel.SetTracerProvider(tp)
		otel.SetMeterProvider(mp)
		otel.SetTextMapPropagator(prop)
		otel.SetErrorHandler(eh)
	})
}

type fakeCollector struct {
	*httptest.Server
	mu    sync.Mutex
	paths map[string]int
}

func newFakeCollector(t *testing.T) *fakeCollector {
	c := &fakeCollector{paths: map[string]int{}}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.paths[r.URL.Path]++
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *fakeCollector) saw(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paths[path] > 0
}

func TestSetupDisabledLeavesGlobalsAlone(t *testing.T) {
	clearOTelEnv(t)
	before := otel.GetTracerProvider()
	stop, err := Setup(t.Context(), Service{Name: "test"})
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
	clearOTelEnv(t)
	collector := newFakeCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	stop, err := Setup(t.Context(), Service{Name: "test", Environment: "development"})
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
	if !collector.saw("/v1/traces") || !collector.saw("/v1/metrics") {
		t.Errorf("collector saw %v, want /v1/traces and /v1/metrics", collector.paths)
	}
}

// With only a traces endpoint, no meter provider is installed (it would
// otherwise export to the localhost default and fail every interval).
func TestSetupTracesOnly(t *testing.T) {
	clearOTelEnv(t)
	collector := newFakeCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.URL+"/v1/traces")

	stop, err := Setup(t.Context(), Service{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider); ok {
		t.Error("meter provider installed with only a traces endpoint")
	}
	_, span := otel.Tracer("test").Start(t.Context(), "work")
	span.End()
	if err := stop(t.Context()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !collector.saw("/v1/traces") || collector.saw("/v1/metrics") {
		t.Errorf("collector saw %v, want only /v1/traces", collector.paths)
	}
}

func TestSetupRoutesSDKErrorsToLogger(t *testing.T) {
	clearOTelEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", newFakeCollector(t).URL+"/v1/traces")
	var buf bytes.Buffer
	stop, err := Setup(t.Context(), Service{Name: "test", Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stop(context.Background()) }()

	otel.Handle(errors.New("export failed"))
	if !strings.Contains(buf.String(), `"msg":"opentelemetry"`) || !strings.Contains(buf.String(), "export failed") {
		t.Errorf("SDK error not logged through the logger: %q", buf.String())
	}
}

func TestResourceEnvOverridesService(t *testing.T) {
	clearOTelEnv(t)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=staging,service.version=1.2")
	t.Setenv("OTEL_SERVICE_NAME", "from-env")

	res, err := newResource(t.Context(), Service{Name: "from-code"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[attribute.Key]string{}
	for _, kv := range res.Attributes() {
		got[kv.Key] = kv.Value.String()
	}
	if got["service.name"] != "from-env" || got["service.version"] != "1.2" || got["deployment.environment.name"] != "staging" {
		t.Errorf("resource = %v", got)
	}
}

func TestLogHandlerAddsTraceContext(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()
	sc := span.SpanContext()

	decode := func(buf *bytes.Buffer) map[string]any {
		t.Helper()
		var rec map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
			t.Fatalf("decode %q: %v", buf.String(), err)
		}
		buf.Reset()
		return rec
	}

	var buf bytes.Buffer
	log := slog.New(LogHandler(slog.NewJSONHandler(&buf, nil))).With("svc", "x")

	log.InfoContext(ctx, "flat", "k", 1)
	rec := decode(&buf)
	if rec["trace_id"] != sc.TraceID().String() || rec["span_id"] != sc.SpanID().String() || rec["svc"] != "x" {
		t.Errorf("flat record = %v", rec)
	}

	log.InfoContext(context.Background(), "no span")
	if rec := decode(&buf); rec["trace_id"] != nil {
		t.Errorf("record without a span has trace_id: %v", rec)
	}

	// The IDs stay top-level on grouped loggers, where backends look for
	// them, while grouped attrs stay in their group.
	log.WithGroup("req").With("id", 7).InfoContext(ctx, "grouped", "k", 2)
	rec = decode(&buf)
	group, _ := rec["req"].(map[string]any)
	if rec["trace_id"] != sc.TraceID().String() || rec["svc"] != "x" || group["id"] != float64(7) || group["k"] != float64(2) || group["trace_id"] != nil {
		t.Errorf("grouped record = %v", rec)
	}
}
