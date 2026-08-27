package logging

import (
	"io"
	"log/slog"
	"time"
)

// newJSONHandler creates a standard slog.JSONHandler with standardized timestamp formatting.
func newJSONHandler(out io.Writer, level slog.Level, addSource bool) slog.Handler {
	return slog.NewJSONHandler(out, &slog.HandlerOptions{
		Level:     level,
		AddSource: addSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Normalize timestamp to ISO-8601 UTC
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, t.UTC().Format(time.RFC3339Nano))
				}
			}
			// Capitalize log level name
			if a.Key == slog.LevelKey {
				if lvl, ok := a.Value.Any().(slog.Level); ok {
					return slog.String(slog.LevelKey, lvl.String())
				}
			}
			return a
		},
	})
}
