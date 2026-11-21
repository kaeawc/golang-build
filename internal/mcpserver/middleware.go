package mcpserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LoggingMiddleware returns receiving middleware that logs every MCP request
// with its method, duration, outcome, negotiated protocol version, and (for
// tools/call) the tool name and whether it produced a tool-level error.
//
// For stdio servers the logger must write to stderr: stdout is the protocol
// channel.
func LoggingMiddleware(log *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			start := time.Now()
			res, err := next(ctx, method, req)
			attrs := []slog.Attr{
				slog.String("method", method),
				slog.Duration("duration", time.Since(start)),
			}
			if s := req.GetSession(); s != nil && s.ID() != "" {
				attrs = append(attrs, slog.String("session", s.ID()))
			}
			if pv, ok := req.(interface{ ProtocolVersion() string }); ok && pv.ProtocolVersion() != "" {
				attrs = append(attrs, slog.String("protocol", pv.ProtocolVersion()))
			}
			attrs = append(attrs, callAttrs(req, res)...)
			level := slog.LevelInfo
			if err != nil {
				level = slog.LevelWarn
				attrs = append(attrs, slog.String("err", err.Error()))
			}
			log.LogAttrs(ctx, level, "mcp request", attrs...)
			return res, err
		}
	}
}

func callAttrs(req mcp.Request, res mcp.Result) []slog.Attr {
	var attrs []slog.Attr
	if ct, ok := req.(*mcp.CallToolRequest); ok && ct.Params != nil {
		attrs = append(attrs, slog.String("tool", ct.Params.Name))
	}
	if r, ok := res.(*mcp.CallToolResult); ok && r != nil {
		attrs = append(attrs, slog.Bool("is_error", r.IsError))
		if len(r.InputRequests) > 0 {
			attrs = append(attrs, slog.Int("input_requests", len(r.InputRequests)))
		}
	}
	return attrs
}
