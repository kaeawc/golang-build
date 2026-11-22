// Package telemetry wires OpenTelemetry traces and metrics. It is opt-in:
// unless an OTLP endpoint is configured, Setup leaves the global no-op
// providers in place, so instrumented code (otelhttp, otelpgx, otelriver)
// costs next to nothing.
//
//	shutdown, err := telemetry.Setup(ctx, env.Default, telemetry.Service{Name: "server"})
//	if err != nil { return err }
//	defer shutdown(context.Background())
//
// Exporters speak OTLP over HTTP and read the standard OTEL_* variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS,
// OTEL_RESOURCE_ATTRIBUTES, ...).
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/kaeawc/golang-build/internal/env"
)

// Service identifies the process in exported telemetry.
type Service struct {
	Name        string
	Version     string
	Environment string
}

// Enabled reports whether r configures an OTLP endpoint and does not
// disable the SDK (OTEL_SDK_DISABLED=true).
func Enabled(r env.Reader) bool {
	if strings.EqualFold(env.GetTrim(r, "OTEL_SDK_DISABLED"), "true") {
		return false
	}
	for _, name := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	} {
		if env.GetTrim(r, name) != "" {
			return true
		}
	}
	return false
}

// Setup installs global tracer and meter providers exporting over OTLP,
// W3C trace-context propagation, and Go runtime metrics, when Enabled(r).
// Otherwise it changes nothing. The returned function flushes and stops
// the providers (a no-op when disabled). The exporters themselves read
// their settings from the process environment.
func Setup(ctx context.Context, r env.Reader, svc Service) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if !Enabled(r) {
		return noop, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(svc.Name),
		semconv.ServiceVersion(svc.Version),
		semconv.DeploymentEnvironmentNameKey.String(svc.Environment),
	))
	if err != nil {
		return noop, fmt.Errorf("telemetry: resource: %w", err)
	}

	traceExp, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, fmt.Errorf("telemetry: trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	metricExp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return noop, errors.Join(fmt.Errorf("telemetry: metric exporter: %w", err), tp.Shutdown(ctx))
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(30*time.Second))),
		sdkmetric.WithResource(res),
	)
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return noop, errors.Join(fmt.Errorf("telemetry: runtime metrics: %w", err), tp.Shutdown(ctx), mp.Shutdown(ctx))
	}

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		// Traces first: their batch is usually what's still in flight.
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}
