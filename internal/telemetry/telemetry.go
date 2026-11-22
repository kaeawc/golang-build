// Package telemetry wires OpenTelemetry traces and metrics. It is opt-in:
// a signal is exported only when an OTLP endpoint is configured for it,
// otherwise the global no-op provider stays in place and instrumented code
// (otelhttp, otelpgx, otelriver) costs next to nothing.
//
//	shutdown, err := telemetry.Setup(ctx, telemetry.Service{Name: "server"})
//	if err != nil { return err }
//	defer shutdown(context.Background())
//
// Like the OTel SDKs, everything is configured through the standard OTEL_*
// process environment variables (OTEL_EXPORTER_OTLP_ENDPOINT,
// OTEL_EXPORTER_OTLP_HEADERS, OTEL_RESOURCE_ATTRIBUTES, OTEL_TRACES_SAMPLER,
// ...); exporters speak OTLP over HTTP.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/kaeawc/golang-build/internal/env"
)

// Service identifies the process in exported telemetry. Empty fields are
// left out, and OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES override them.
type Service struct {
	Name        string
	Version     string
	Environment string
	// Logger receives SDK errors (failed exports, dropped data).
	// Defaults to slog.Default().
	Logger *slog.Logger
}

// Signals reports which signals r enables. A signal is on when the shared
// OTEL_EXPORTER_OTLP_ENDPOINT or its own OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT
// is set, OTEL_<SIGNAL>_EXPORTER is not "none", and OTEL_SDK_DISABLED is
// not "true".
func Signals(r env.Reader) (traces, metrics bool) {
	if strings.EqualFold(env.GetTrim(r, "OTEL_SDK_DISABLED"), "true") {
		return false, false
	}
	shared := env.GetTrim(r, "OTEL_EXPORTER_OTLP_ENDPOINT") != ""
	on := func(signal string) bool {
		if strings.EqualFold(env.GetTrim(r, "OTEL_"+signal+"_EXPORTER"), "none") {
			return false
		}
		return shared || env.GetTrim(r, "OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
	}
	return on("TRACES"), on("METRICS")
}

// Setup installs a global tracer provider and/or meter provider (with Go
// runtime metrics) for the signals the process environment enables, plus
// W3C trace-context propagation and SDK error reporting to svc.Logger. With
// nothing enabled it changes nothing. The returned function flushes and
// stops whatever was installed.
func Setup(ctx context.Context, svc Service) (func(context.Context) error, error) {
	traces, metrics := Signals(env.Default)
	if !traces && !metrics {
		return func(context.Context) error { return nil }, nil
	}

	log := svc.Logger
	if log == nil {
		log = slog.Default()
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("opentelemetry", "err", err)
	}))
	otel.SetLogger(logr.FromSlogHandler(log.Handler()))

	res, err := newResource(ctx, svc)
	if err != nil {
		return nil, err
	}

	var shutdowns []func(context.Context) error
	shutdown := func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdowns {
			errs = append(errs, fn(ctx))
		}
		return errors.Join(errs...)
	}
	if traces {
		stop, err := setupTraces(ctx, res)
		if err != nil {
			return nil, err
		}
		shutdowns = append(shutdowns, stop)
	}
	if metrics {
		stop, err := setupMetrics(ctx, res)
		if err != nil {
			return nil, errors.Join(err, shutdown(ctx))
		}
		shutdowns = append(shutdowns, stop)
	}
	return shutdown, nil
}

// setupTraces installs the global tracer provider and W3C propagation.
func setupTraces(ctx context.Context, res *resource.Resource) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// setupMetrics installs the global meter provider with Go runtime metrics.
func setupMetrics(ctx context.Context, res *resource.Resource) (func(context.Context) error, error) {
	exp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(30*time.Second))),
		sdkmetric.WithResource(res),
	)
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return nil, errors.Join(fmt.Errorf("telemetry: runtime metrics: %w", err), mp.Shutdown(ctx))
	}
	otel.SetMeterProvider(mp)
	return mp.Shutdown, nil
}

// newResource describes the process. Detectors listed later win, so
// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES override svc.
func newResource(ctx context.Context, svc Service) (*resource.Resource, error) {
	var attrs []attribute.KeyValue
	if svc.Name != "" {
		attrs = append(attrs, semconv.ServiceName(svc.Name))
	}
	if svc.Version != "" {
		attrs = append(attrs, semconv.ServiceVersion(svc.Version))
	}
	if svc.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(svc.Environment))
	}
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}
	return res, nil
}
