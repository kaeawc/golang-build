package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// LogHandler wraps h so every record logged with a context that carries
// a valid span gets trace_id and span_id attributes, letting log lines
// be joined to traces. Log with the *Context methods (InfoContext,
// LogAttrs, ...) for the IDs to appear.
func LogHandler(h slog.Handler) slog.Handler { return traceHandler{h} }

type traceHandler struct{ slog.Handler }

func (t traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return t.Handler.Handle(ctx, r)
}

func (t traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{t.Handler.WithAttrs(attrs)}
}

func (t traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{t.Handler.WithGroup(name)}
}
