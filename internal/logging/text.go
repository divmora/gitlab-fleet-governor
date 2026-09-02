package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// TextHandlerOptions configures the ColoredTextHandler.
type TextHandlerOptions struct {
	Level     slog.Level
	NoColor   bool
	AddSource bool
}

// ColoredTextHandler implements slog.Handler with clean, human-readable ANSI colored formatting.
type ColoredTextHandler struct {
	out    io.Writer
	mu     *sync.Mutex
	opts   TextHandlerOptions
	attrs  []slog.Attr
	groups []string
}

// NewColoredTextHandler creates a new ColoredTextHandler.
func NewColoredTextHandler(out io.Writer, opts TextHandlerOptions) *ColoredTextHandler {
	return &ColoredTextHandler{
		out:    out,
		mu:     &sync.Mutex{},
		opts:   opts,
		attrs:  make([]slog.Attr, 0),
		groups: make([]string, 0),
	}
}

// Enabled returns true if the specified level is at or above the handler's threshold.
func (h *ColoredTextHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level
}

// Handle formats and writes the slog.Record to the output writer.
func (h *ColoredTextHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Timestamp (dimmed)
	timeStr := r.Time.Format("15:04:05.000")
	if !h.opts.NoColor {
		timeStr = "\033[90m" + timeStr + "\033[0m"
	}

	// Level badge
	levelStr := formatLevelBadge(r.Level, h.opts.NoColor)

	// Source file:line if requested
	sourceStr := ""
	if h.opts.AddSource && r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		frame, _ := fs.Next()
		if frame.File != "" {
			src := fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line)
			if !h.opts.NoColor {
				sourceStr = " \033[90m(" + src + ")\033[0m"
			} else {
				sourceStr = " (" + src + ")"
			}
		}
	}

	// Message
	msg := r.Message
	if !h.opts.NoColor && r.Level >= slog.LevelError {
		msg = "\033[1;31m" + msg + "\033[0m"
	}

	line := fmt.Sprintf("%s %s%s %s", timeStr, levelStr, sourceStr, msg)

	// Format Handler-level attributes
	for _, attr := range h.attrs {
		line += " " + formatAttr(attr, h.opts.NoColor)
	}

	// Format Record-level attributes
	r.Attrs(func(attr slog.Attr) bool {
		line += " " + formatAttr(attr, h.opts.NoColor)
		return true
	})

	line += "\n"
	_, err := io.WriteString(h.out, line)
	return err
}

// WithAttrs returns a clone of the handler with additional attributes.
func (h *ColoredTextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)

	newGroups := make([]string, len(h.groups))
	copy(newGroups, h.groups)

	return &ColoredTextHandler{
		out:    h.out,
		mu:     h.mu,
		opts:   h.opts,
		attrs:  newAttrs,
		groups: newGroups,
	}
}

// WithGroup returns a clone of the handler with an added group name.
func (h *ColoredTextHandler) WithGroup(name string) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs))
	copy(newAttrs, h.attrs)

	newGroups := make([]string, len(h.groups), len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups = append(newGroups, name)

	return &ColoredTextHandler{
		out:    h.out,
		mu:     h.mu,
		opts:   h.opts,
		attrs:  newAttrs,
		groups: newGroups,
	}
}

func formatLevelBadge(level slog.Level, noColor bool) string {
	var badge, color string
	switch {
	case level < slog.LevelInfo:
		badge = "[DEBUG]"
		color = "\033[36m" // Cyan
	case level < slog.LevelWarn:
		badge = "[INFO ]"
		color = "\033[32m" // Green
	case level < slog.LevelError:
		badge = "[WARN ]"
		color = "\033[33m" // Yellow
	default:
		badge = "[ERROR]"
		color = "\033[1;31m" // Bold Red
	}

	if noColor {
		return badge
	}
	return color + badge + "\033[0m"
}

func formatAttr(attr slog.Attr, noColor bool) string {
	key := attr.Key
	val := attr.Value.Resolve()

	if noColor {
		return fmt.Sprintf("%s=%v", key, val)
	}

	keyColor := "\033[36m" // Cyan key
	reset := "\033[0m"

	switch val.Kind() {
	case slog.KindString:
		return fmt.Sprintf("%s%s%s=%s%q%s", keyColor, key, reset, "\033[37m", val.String(), reset)
	case slog.KindInt64, slog.KindUint64, slog.KindFloat64:
		return fmt.Sprintf("%s%s%s=%s%v%s", keyColor, key, reset, "\033[35m", val.Any(), reset)
	case slog.KindBool:
		return fmt.Sprintf("%s%s%s=%s%v%s", keyColor, key, reset, "\033[33m", val.Bool(), reset)
	case slog.KindDuration:
		return fmt.Sprintf("%s%s%s=%s%v%s", keyColor, key, reset, "\033[34m", val.Duration(), reset)
	case slog.KindTime:
		return fmt.Sprintf("%s%s%s=%s%s%s", keyColor, key, reset, "\033[34m", val.Time().Format(time.RFC3339), reset)
	default:
		return fmt.Sprintf("%s%s%s=%v", keyColor, key, reset, val.Any())
	}
}
