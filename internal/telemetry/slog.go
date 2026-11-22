package telemetry

import (
	"context"
	"log/slog"
	"slices"

	"go.opentelemetry.io/otel/trace"
)

// LogHandler wraps h so every record logged with a context that carries
// a valid span gets top-level trace_id and span_id attributes (also on
// loggers derived with WithGroup), letting log lines be joined to traces.
// Log with the *Context methods (InfoContext, LogAttrs, ...) for the IDs
// to appear.
func LogHandler(h slog.Handler) slog.Handler { return &traceHandler{root: h, h: h} }

type traceHandler struct {
	root slog.Handler // h before any WithGroup, for re-deriving grouped handlers
	h    slog.Handler // root with ops applied
	ops  []func(slog.Handler) slog.Handler
	// grouped is true once a WithGroup op exists; until then trace
	// attributes can go straight onto the record.
	grouped bool
}

func (t *traceHandler) Enabled(ctx context.Context, l slog.Level) bool { return t.h.Enabled(ctx, l) }

func (t *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return t.h.Handle(ctx, r)
	}
	ids := []slog.Attr{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	}
	if !t.grouped {
		r = r.Clone()
		r.AddAttrs(ids...)
		return t.h.Handle(ctx, r)
	}
	// Record attributes land inside the open groups, so put the IDs on a
	// handler derived from the ungrouped root and replay the groups.
	h := t.root.WithAttrs(ids)
	for _, op := range t.ops {
		h = op(h)
	}
	return h.Handle(ctx, r)
}

func (t *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return t.with(func(h slog.Handler) slog.Handler { return h.WithAttrs(attrs) }, false)
}

func (t *traceHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return t
	}
	return t.with(func(h slog.Handler) slog.Handler { return h.WithGroup(name) }, true)
}

func (t *traceHandler) with(op func(slog.Handler) slog.Handler, group bool) slog.Handler {
	if !t.grouped && !group {
		// No groups yet: fold attrs into the root so Handle's fast path
		// and any later replay both see them.
		root := op(t.root)
		return &traceHandler{root: root, h: root}
	}
	ops := append(slices.Clip(t.ops), op)
	return &traceHandler{root: t.root, h: op(t.h), ops: ops, grouped: true}
}
