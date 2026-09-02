package logging

import (
	"context"
	"log/slog"
)

// Context key types to avoid collisions across packages.
type contextKey string

const (
	keyLogger      contextKey = "gov_logger"
	keyTraceID     contextKey = "gov_trace_id"
	keyProjectID   contextKey = "gov_project_id"
	keyProjectPath contextKey = "gov_project_path"
	keyGroupID     contextKey = "gov_group_id"
	keyGroupPath   contextKey = "gov_group_path"
	keyOperation   contextKey = "gov_operation"
	keyAttempt     contextKey = "gov_attempt"
	keyCustomAttrs contextKey = "gov_custom_attrs"
)

// WithLogger attaches a *slog.Logger to the context.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, keyLogger, logger)
}

// FromContext extracts the logger from context, or returns slog.Default() if none is found.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if logger, ok := ctx.Value(keyLogger).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}

// WithTrace attaches a correlation trace ID to the context.
func WithTrace(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, keyTraceID, traceID)
}

// WithProject attaches GitLab project ID and project path to the context.
func WithProject(ctx context.Context, id int, path string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, keyProjectID, id)
	return context.WithValue(ctx, keyProjectPath, path)
}

// WithGroup attaches GitLab group ID and group full path to the context.
func WithGroup(ctx context.Context, id int, fullPath string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, keyGroupID, id)
	return context.WithValue(ctx, keyGroupPath, fullPath)
}

// WithOperation attaches the current governance operation name to the context.
func WithOperation(ctx context.Context, operation string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, keyOperation, operation)
}

// WithAttempt attaches the retry attempt number to the context.
func WithAttempt(ctx context.Context, attempt int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, keyAttempt, attempt)
}

// WithAttrs attaches custom slog attributes to the context.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	existing, _ := ctx.Value(keyCustomAttrs).([]slog.Attr)
	combined := make([]slog.Attr, len(existing), len(existing)+len(attrs))
	copy(combined, existing)
	combined = append(combined, attrs...)
	return context.WithValue(ctx, keyCustomAttrs, combined)
}

// ContextHandler wraps an underlying slog.Handler to automatically inject
// trace attributes present in context.Context into every emitted log record.
type ContextHandler struct {
	handler slog.Handler
}

// NewContextHandler constructs a ContextHandler wrapping the provided handler.
func NewContextHandler(h slog.Handler) slog.Handler {
	return &ContextHandler{handler: h}
}

// Enabled delegates to the underlying handler.
func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle extracts trace attributes from ctx and appends them to the record before forwarding.
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		if traceID, ok := ctx.Value(keyTraceID).(string); ok && traceID != "" {
			r.AddAttrs(slog.String("trace_id", traceID))
		}
		if projectID, ok := ctx.Value(keyProjectID).(int); ok && projectID > 0 {
			r.AddAttrs(slog.Int("project_id", projectID))
		}
		if projectPath, ok := ctx.Value(keyProjectPath).(string); ok && projectPath != "" {
			r.AddAttrs(slog.String("project_path", projectPath))
		}
		if groupID, ok := ctx.Value(keyGroupID).(int); ok && groupID > 0 {
			r.AddAttrs(slog.Int("group_id", groupID))
		}
		if groupPath, ok := ctx.Value(keyGroupPath).(string); ok && groupPath != "" {
			r.AddAttrs(slog.String("group_path", groupPath))
		}
		if op, ok := ctx.Value(keyOperation).(string); ok && op != "" {
			r.AddAttrs(slog.String("operation", op))
		}
		if attempt, ok := ctx.Value(keyAttempt).(int); ok && attempt > 0 {
			r.AddAttrs(slog.Int("attempt", attempt))
		}
		if customAttrs, ok := ctx.Value(keyCustomAttrs).([]slog.Attr); ok && len(customAttrs) > 0 {
			r.AddAttrs(customAttrs...)
		}
	}
	return h.handler.Handle(ctx, r)
}

// WithAttrs delegates to the underlying handler.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{handler: h.handler.WithAttrs(attrs)}
}

// WithGroup delegates to the underlying handler.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{handler: h.handler.WithGroup(name)}
}
