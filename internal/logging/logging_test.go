package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/logging"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
		wantErr  bool
	}{
		{"debug", slog.LevelDebug, false},
		{"DEBUG", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{"", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"invalid", slog.LevelInfo, true},
	}

	for _, tt := range tests {
		got, err := logging.ParseLevel(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("ParseLevel(%q) expected error, got nil", tt.input)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("ParseLevel(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.expected {
			t.Errorf("ParseLevel(%q) = %v, expected %v", tt.input, got, tt.expected)
		}
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected logging.Format
		wantErr  bool
	}{
		{"text", logging.FormatText, false},
		{"console", logging.FormatText, false},
		{"terminal", logging.FormatText, false},
		{"", logging.FormatText, false},
		{"json", logging.FormatJSON, false},
		{"JSON", logging.FormatJSON, false},
		{"xml", "", true},
	}

	for _, tt := range tests {
		got, err := logging.ParseFormat(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("ParseFormat(%q) expected error, got nil", tt.input)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("ParseFormat(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.expected {
			t.Errorf("ParseFormat(%q) = %v, expected %v", tt.input, got, tt.expected)
		}
	}
}

func TestColoredTextHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{
		Level:     "debug",
		Format:    "text",
		Output:    &buf,
		NoColor:   true,
		AddSource: true,
	})

	logger.Info("reconciling project", slog.String("project", "platform/core"), slog.Int("id", 101), slog.Bool("dry_run", true), slog.Duration("duration", 50*time.Millisecond))
	out := buf.String()

	if !strings.Contains(out, "[INFO ]") {
		t.Errorf("expected level badge [INFO ] in output, got: %s", out)
	}
	if !strings.Contains(out, "reconciling project") {
		t.Errorf("expected message in output, got: %s", out)
	}
	if !strings.Contains(out, "project=\"platform/core\"") && !strings.Contains(out, "project=platform/core") {
		t.Errorf("expected attribute project in output, got: %s", out)
	}
	if !strings.Contains(out, "id=101") {
		t.Errorf("expected attribute id in output, got: %s", out)
	}

	// Test with color
	var colorBuf bytes.Buffer
	colorLogger := logging.NewLogger(logging.Config{
		Level:   "error",
		Format:  "text",
		Output:  &colorBuf,
		NoColor: false,
	})
	colorLogger.Error("critical error occurred", slog.String("err", "something broke"))
	if !strings.Contains(colorBuf.String(), "critical error occurred") {
		t.Errorf("expected colored error log")
	}
}

func TestJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})

	logger.Warn("rate limit warning", slog.Int("retry_count", 2), slog.String("resource", "push_rule"))

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("JSON output invalid: %v\nJSON:\n%s", err, buf.String())
	}

	if parsed["level"] != "WARN" {
		t.Errorf("expected level 'WARN', got %v", parsed["level"])
	}
	if parsed["msg"] != "rate limit warning" {
		t.Errorf("expected msg 'rate limit warning', got %v", parsed["msg"])
	}
	if parsed["retry_count"] != float64(2) {
		t.Errorf("expected retry_count 2, got %v", parsed["retry_count"])
	}
}

func TestContextHandler_TracePropagation(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})

	ctx := context.Background()
	ctx = logging.WithTrace(ctx, "trace-abc-123")
	ctx = logging.WithProject(ctx, 42, "platform/web-app")
	ctx = logging.WithGroup(ctx, 10, "platform")
	ctx = logging.WithOperation(ctx, "push_rules")
	ctx = logging.WithAttempt(ctx, 1)
	ctx = logging.WithAttrs(ctx, slog.String("custom_tag", "soc2"))

	logger.InfoContext(ctx, "operation planned successfully")

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}

	if parsed["trace_id"] != "trace-abc-123" {
		t.Errorf("expected trace_id 'trace-abc-123', got %v", parsed["trace_id"])
	}
	if parsed["project_id"] != float64(42) {
		t.Errorf("expected project_id 42, got %v", parsed["project_id"])
	}
	if parsed["project_path"] != "platform/web-app" {
		t.Errorf("expected project_path 'platform/web-app', got %v", parsed["project_path"])
	}
	if parsed["group_id"] != float64(10) {
		t.Errorf("expected group_id 10, got %v", parsed["group_id"])
	}
	if parsed["group_path"] != "platform" {
		t.Errorf("expected group_path 'platform', got %v", parsed["group_path"])
	}
	if parsed["operation"] != "push_rules" {
		t.Errorf("expected operation 'push_rules', got %v", parsed["operation"])
	}
	if parsed["attempt"] != float64(1) {
		t.Errorf("expected attempt 1, got %v", parsed["attempt"])
	}
	if parsed["custom_tag"] != "soc2" {
		t.Errorf("expected custom_tag 'soc2', got %v", parsed["custom_tag"])
	}
}

func TestWithLogger_And_FromContext(t *testing.T) {
	var buf bytes.Buffer
	customLogger := logging.NewLogger(logging.Config{
		Level:  "info",
		Format: "text",
		Output: &buf,
	})

	ctx := logging.WithLogger(context.Background(), customLogger)
	extracted := logging.FromContext(ctx)
	if extracted != customLogger {
		t.Errorf("FromContext did not return the expected custom logger")
	}

	defaultExtracted := logging.FromContext(context.Background())
	if defaultExtracted == nil {
		t.Errorf("expected non-nil default logger from empty context")
	}

	nilCtxExtracted := logging.FromContext(nil)
	if nilCtxExtracted == nil {
		t.Errorf("expected non-nil default logger from nil context")
	}
}

func TestInitLogger(t *testing.T) {
	cfg := logging.DefaultConfig()
	logger := logging.InitLogger(cfg)
	if logger == nil {
		t.Fatalf("expected non-nil logger from InitLogger")
	}
	if slog.Default() != logger {
		t.Errorf("InitLogger did not set slog.Default()")
	}
}

func TestWithAttrs_And_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{
		Level:   "info",
		Format:  "text",
		Output:  &buf,
		NoColor: true,
	})

	subLogger := logger.With(slog.String("component", "engine")).WithGroup("metrics")
	subLogger.Info("metric computed", slog.Int("count", 10))

	out := buf.String()
	if !strings.Contains(out, "component=\"engine\"") && !strings.Contains(out, "component=engine") {
		t.Errorf("missing handler-level attribute in output: %s", out)
	}
}
