package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Level denotes logging verbosity level.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Format denotes logging serialization format.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Config encapsulates configuration for structured logging.
type Config struct {
	Level     string    // "debug", "info", "warn", "error" (default: "info")
	Format    string    // "text", "json" (default: "text")
	Output    io.Writer // Destination writer (default: os.Stderr)
	NoColor   bool      // Disable ANSI color in text mode (default: false)
	AddSource bool      // Include source code file:line in log records
}

// DefaultConfig returns default logging configuration.
func DefaultConfig() Config {
	return Config{
		Level:     "info",
		Format:    "text",
		Output:    os.Stderr,
		NoColor:   false,
		AddSource: false,
	}
}

// ParseLevel parses a level string into a slog.Level.
func ParseLevel(lvl string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(lvl)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q (valid: debug, info, warn, error)", lvl)
	}
}

// ParseFormat parses a format string into a canonical Format.
func ParseFormat(fmtStr string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(fmtStr)) {
	case "text", "console", "terminal", "":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("invalid log format %q (valid: text, json)", fmtStr)
	}
}

// InitLogger constructs a *slog.Logger based on Config, sets it as the default slog logger, and returns it.
func InitLogger(cfg Config) *slog.Logger {
	logger := NewLogger(cfg)
	slog.SetDefault(logger)
	return logger
}

// NewLogger constructs a configured *slog.Logger.
func NewLogger(cfg Config) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}

	level, err := ParseLevel(cfg.Level)
	if err != nil {
		level = slog.LevelInfo
	}

	format, err := ParseFormat(cfg.Format)
	if err != nil {
		format = FormatText
	}

	// Auto-detect NO_COLOR environment variable
	noColor := cfg.NoColor || os.Getenv("NO_COLOR") != ""

	var baseHandler slog.Handler
	if format == FormatJSON {
		baseHandler = newJSONHandler(out, level, cfg.AddSource)
	} else {
		baseHandler = NewColoredTextHandler(out, TextHandlerOptions{
			Level:     level,
			NoColor:   noColor,
			AddSource: cfg.AddSource,
		})
	}

	// Wrap with contextual trace extractor handler
	handler := NewContextHandler(baseHandler)
	return slog.New(handler)
}
