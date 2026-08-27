package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChallenge_Logging_ConcurrentRace verifies thread-safety under heavy concurrent load.
func TestChallenge_Logging_ConcurrentRace(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{
		Level:     "debug",
		Format:    "text",
		Output:    &buf,
		NoColor:   false,
		AddSource: true,
	})

	const numGoroutines = 50
	const msgsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gID int) {
			defer wg.Done()
			ctx := logging.WithTrace(context.Background(), fmt.Sprintf("trace-%d", gID))
			ctx = logging.WithProject(ctx, 100+gID, fmt.Sprintf("group/project-%d", gID))
			ctx = logging.WithGroup(ctx, 200+gID, fmt.Sprintf("group-%d", gID))
			ctx = logging.WithOperation(ctx, "push_rules")
			ctx = logging.WithAttempt(ctx, 1)
			ctx = logging.WithAttrs(ctx, slog.String("worker", fmt.Sprintf("w-%d", gID)))

			subLogger := logger.With("sub_id", gID).WithGroup("sub_group")

			for i := 0; i < msgsPerGoroutine; i++ {
				subLogger.Log(ctx, slog.LevelInfo, "concurrent test message",
					slog.Int("iteration", i),
					slog.Duration("dur", time.Millisecond*time.Duration(i)),
					slog.Time("now", time.Now()),
					slog.Bool("valid", true),
					slog.Any("custom", struct{ A string }{A: "test"}),
				)
			}
		}(g)
	}

	wg.Wait()
	assert.NotEmpty(t, buf.String())
}

// TestChallenge_Logging_JSONFormatCompliance checks JSON formatting, UTC timestamps, and level keys.
func TestChallenge_Logging_JSONFormatCompliance(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{
		Level:     "debug",
		Format:    "json",
		Output:    &buf,
		AddSource: false,
	})

	ctx := context.Background()
	ctx = logging.WithTrace(ctx, "trace-xyz-999")
	ctx = logging.WithProject(ctx, 42, "security/fleet")
	ctx = logging.WithOperation(ctx, "approval_rules")

	logger.Log(ctx, slog.LevelWarn, "security policy drift detected", slog.String("field", "approvals_before_merge"))

	line := strings.TrimSpace(buf.String())
	require.NotEmpty(t, line)

	var logMap map[string]any
	err := json.Unmarshal([]byte(line), &logMap)
	require.NoError(t, err, "JSON log output must be valid JSON: %s", line)

	assert.Equal(t, "WARN", logMap[slog.LevelKey], "Level should be capitalized WARN")
	assert.Equal(t, "security policy drift detected", logMap[slog.MessageKey])
	assert.Equal(t, "trace-xyz-999", logMap["trace_id"])
	assert.Equal(t, float64(42), logMap["project_id"])
	assert.Equal(t, "security/fleet", logMap["project_path"])
	assert.Equal(t, "approval_rules", logMap["operation"])
	assert.Equal(t, "approvals_before_merge", logMap["field"])

	// Validate time is RFC3339 UTC
	timeStr, ok := logMap[slog.TimeKey].(string)
	require.True(t, ok, "time must be string")
	parsedTime, err := time.Parse(time.RFC3339Nano, timeStr)
	require.NoError(t, err, "time must parse as RFC3339Nano")
	assert.Equal(t, time.UTC, parsedTime.Location(), "timestamp should be in UTC")
}

// TestChallenge_Logging_NoColorEnvironmentVariable checks NO_COLOR support.
func TestChallenge_Logging_NoColorEnvironmentVariable(t *testing.T) {
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{
		Level:   "info",
		Format:  "text",
		Output:  &buf,
		NoColor: false, // Explicit false, but env NO_COLOR=1 should override
	})

	logger.Info("test without color")
	output := buf.String()

	assert.NotContains(t, output, "\033[", "Output must not contain ANSI escape codes when NO_COLOR is set in env")
	assert.Contains(t, output, "[INFO ]")
	assert.Contains(t, output, "test without color")
}

// TestChallenge_Logging_AllLogLevels tests all log levels (Debug, Info, Warn, Error).
func TestChallenge_Logging_AllLogLevels(t *testing.T) {
	levels := []struct {
		lvlStr string
		level  slog.Level
		badge  string
	}{
		{"debug", slog.LevelDebug, "[DEBUG]"},
		{"info", slog.LevelInfo, "[INFO ]"},
		{"warn", slog.LevelWarn, "[WARN ]"},
		{"error", slog.LevelError, "[ERROR]"},
	}

	for _, l := range levels {
		t.Run(l.lvlStr, func(t *testing.T) {
			var buf bytes.Buffer
			logger := logging.NewLogger(logging.Config{
				Level:   l.lvlStr,
				Format:  "text",
				Output:  &buf,
				NoColor: true,
			})

			logger.Log(context.Background(), l.level, "level message")
			assert.Contains(t, buf.String(), l.badge)
			assert.Contains(t, buf.String(), "level message")
		})
	}
}
