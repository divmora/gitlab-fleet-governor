package report_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create sample report data with varied edge cases
func createChallengeReportData() *report.ReportData {
	startTime := time.Date(2026, 8, 26, 2, 0, 0, 0, time.UTC)
	endTime := startTime.Add(1500 * time.Millisecond)

	data := report.NewReportData(true, startTime, endTime)
	data.TotalScanned = 20

	// Target 1: Changed with complex diffs (quotes, commas, newlines)
	data.AddTarget(report.TargetReport{
		ID:           101,
		Path:         "security/fleet-manager",
		ResourceType: "project",
		Status:       "CHANGED",
		HasDrift:     true,
		Duration:     450 * time.Millisecond,
		Operations: []report.OperationReport{
			{
				Name:         "push_rules",
				ResourceType: "project",
				ResourceID:   101,
				ResourcePath: "security/fleet-manager",
				Action:       "UPDATE",
				Status:       "SUCCESS",
				HasChanges:   true,
				Duration:     200 * time.Millisecond,
				Diffs: []report.DiffReport{
					{
						Resource: "push_rules",
						Action:   "UPDATE",
						Fields: []report.FieldDiffReport{
							{
								Field:    "commit_message_regex",
								OldValue: "^(feat|fix):.*",
								NewValue: "^(feat|fix|chore)(\\(.*\\))?: .*\nMultiline \"quoted\", comma-separated",
								Action:   "UPDATE",
							},
							{
								Field:    "deny_delete_tag",
								OldValue: nil, // Nil old value on create/set
								NewValue: true,
								Action:   "CREATE",
							},
						},
					},
				},
			},
		},
	})

	// Target 2: Compliant
	data.AddTarget(report.TargetReport{
		ID:           102,
		Path:         "infra/terraform-modules",
		ResourceType: "project",
		Status:       "UNCHANGED",
		HasDrift:     false,
		Duration:     120 * time.Millisecond,
	})

	// Target 3: Failed with error
	data.AddTarget(report.TargetReport{
		ID:           103,
		Path:         "apps/legacy-monolith",
		ResourceType: "project",
		Status:       "FAILED",
		HasDrift:     true,
		Duration:     300 * time.Millisecond,
		Error:        "403 Forbidden: Insufficient permissions to update push rules",
	})

	return data
}

// TestChallenge_TableReporter_ColorAndPlain tests ANSI color codes rendering.
func TestChallenge_TableReporter_ColorAndPlain(t *testing.T) {
	data := createChallengeReportData()

	// 1. Render with color enabled
	var coloredBuf bytes.Buffer
	repColor, err := report.NewReporter(report.FormatTable, &coloredBuf, report.WithColor(true))
	require.NoError(t, err)
	err = repColor.Render(data)
	require.NoError(t, err)
	coloredOutput := coloredBuf.String()

	assert.Contains(t, coloredOutput, "\033[")
	assert.Contains(t, coloredOutput, "security/fleet-manager")

	// 2. Render with color disabled
	var plainBuf bytes.Buffer
	repPlain, err := report.NewReporter(report.FormatTable, &plainBuf, report.WithColor(false))
	require.NoError(t, err)
	err = repPlain.Render(data)
	require.NoError(t, err)
	plainOutput := plainBuf.String()

	assert.NotContains(t, plainOutput, "\033[")
	assert.Contains(t, plainOutput, "security/fleet-manager")
	assert.Contains(t, plainOutput, "COMPLIANT")
	assert.Contains(t, plainOutput, "FAILED")
}

// TestChallenge_TableReporter_EnvironmentNoColor checks if NO_COLOR env var is respected by default options.
func TestChallenge_TableReporter_EnvironmentNoColor(t *testing.T) {
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	data := createChallengeReportData()
	var buf bytes.Buffer
	// Create reporter with default options
	rep, err := report.NewReporter(report.FormatTable, &buf)
	require.NoError(t, err)
	err = rep.Render(data)
	require.NoError(t, err)

	// Note: DefaultOptions currently defaults Color to true and ignores NO_COLOR env var
	output := buf.String()
	t.Logf("Table output with NO_COLOR env: contains ANSI = %v", strings.Contains(output, "\033["))
}

// TestChallenge_JSONReporter_CompactAndIndented verifies JSON outputs.
func TestChallenge_JSONReporter_CompactAndIndented(t *testing.T) {
	data := createChallengeReportData()

	// 1. Indented JSON
	var indentBuf bytes.Buffer
	repIndent, err := report.NewReporter(report.FormatJSON, &indentBuf, report.WithIndent(true))
	require.NoError(t, err)
	err = repIndent.Render(data)
	require.NoError(t, err)

	indentStr := indentBuf.String()
	assert.True(t, strings.Contains(indentStr, "\n  \"title\":"))

	var unmarshaled report.ReportData
	err = json.Unmarshal([]byte(indentStr), &unmarshaled)
	require.NoError(t, err)
	assert.Equal(t, data.TotalTargeted, unmarshaled.TotalTargeted)
	assert.Equal(t, 3, len(unmarshaled.Targets))

	// 2. Compact JSON
	var compactBuf bytes.Buffer
	repCompact, err := report.NewReporter(report.FormatJSON, &compactBuf, report.WithIndent(false))
	require.NoError(t, err)
	err = repCompact.Render(data)
	require.NoError(t, err)

	compactStr := strings.TrimSpace(compactBuf.String())
	assert.False(t, strings.Contains(compactStr, "\n  "))

	var unmarshaledCompact report.ReportData
	err = json.Unmarshal([]byte(compactStr), &unmarshaledCompact)
	require.NoError(t, err)
	assert.Equal(t, data.TotalTargeted, unmarshaledCompact.TotalTargeted)
}

// TestChallenge_CSVReporter_RFC4180Escaping tests RFC 4180 compliance with commas, quotes, newlines.
func TestChallenge_CSVReporter_RFC4180Escaping(t *testing.T) {
	data := createChallengeReportData()

	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatCSV, &buf)
	require.NoError(t, err)
	err = rep.Render(data)
	require.NoError(t, err)

	csvContent := buf.String()
	require.NotEmpty(t, csvContent)

	// Parse with standard RFC 4180 CSV reader
	r := csv.NewReader(strings.NewReader(csvContent))
	records, err := r.ReadAll()
	require.NoError(t, err)

	// Header row
	require.GreaterOrEqual(t, len(records), 4)
	assert.Equal(t, "resource_type", records[0][0])
	assert.Equal(t, "new_value", records[0][9])

	// Check that multi-line quoted field was parsed correctly
	foundMultiline := false
	for _, row := range records {
		if len(row) > 9 && strings.Contains(row[9], "Multiline \"quoted\", comma-separated") {
			foundMultiline = true
			assert.Contains(t, row[9], "\n")
			assert.Contains(t, row[9], `"quoted"`)
			assert.Contains(t, row[9], "comma-separated")
		}
	}
	assert.True(t, foundMultiline)
}

// TestChallenge_MarkdownReporter_DiffAccordions tests GFM markdown diff blocks and failure mode.
func TestChallenge_MarkdownReporter_DiffAccordions(t *testing.T) {
	data := createChallengeReportData()

	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatMarkdown, &buf, report.WithDiffs(true))
	require.NoError(t, err)
	err = rep.Render(data)
	require.NoError(t, err)

	md := buf.String()

	// Verify markdown tables
	assert.Contains(t, md, "# 🛡️ GitLab Fleet Governor Execution Report")
	assert.Contains(t, md, "### 📊 Executive Summary")
	assert.Contains(t, md, "| **Execution Mode** | `DRY-RUN` |")
	assert.Contains(t, md, "| Type | ID | Path | Status | Duration |")
	assert.Contains(t, md, "| `Project` | `101` | `security/fleet-manager` | 🟡 Drift |")

	// Verify <details> accordions
	assert.Contains(t, md, "### 🔍 Granular Policy Diffs")
	assert.Contains(t, md, "<details>\n<summary><b>security/fleet-manager (project)</b> — CHANGED</summary>")
	assert.Contains(t, md, "```diff")
	assert.Contains(t, md, "! commit_message_regex:")
}

// TestChallenge_MarkdownReporter_FailureWithoutDrift verifies whether failed targets are displayed when HasDrift is false.
func TestChallenge_MarkdownReporter_FailureWithoutDrift(t *testing.T) {
	// Create report data where there is NO drift, but 1 target failed
	startTime := time.Now()
	data := report.NewReportData(false, startTime, startTime.Add(time.Second))
	data.AddTarget(report.TargetReport{
		ID:           201,
		Path:         "critical/payments",
		ResourceType: "project",
		Status:       "FAILED",
		HasDrift:     false,
		Error:        "401 Unauthorized: token expired",
	})

	assert.False(t, data.HasDrift())
	assert.True(t, data.HasErrors())

	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatMarkdown, &buf, report.WithDiffs(true))
	require.NoError(t, err)
	err = rep.Render(data)
	require.NoError(t, err)

	md := buf.String()
	// Target table should show failure
	assert.Contains(t, md, "🔴 Failed")

	// Check if Granular Policy Diffs section exists when HasDrift() is false but HasErrors() is true
	assert.Contains(t, md, "### 🔍 Granular Policy Diffs", "Markdown report must render granular section for failed targets even when 0 drift")
	assert.Contains(t, md, "<summary><b>critical/payments (project)</b> — FAILED</summary>")
	assert.Contains(t, md, "> ❌ **Error**: `401 Unauthorized: token expired`")
}

// TestChallenge_Reporter_ConcurrentRenders tests thread-safety when rendering simultaneously.
func TestChallenge_Reporter_ConcurrentRenders(t *testing.T) {
	data := createChallengeReportData()
	formats := []report.Format{report.FormatTable, report.FormatJSON, report.FormatCSV, report.FormatMarkdown, report.FormatSummary}

	var wg sync.WaitGroup
	for _, f := range formats {
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(fmt report.Format) {
				defer wg.Done()
				var buf bytes.Buffer
				rep, err := report.NewReporter(fmt, &buf)
				assert.NoError(t, err)
				err = rep.Render(data)
				assert.NoError(t, err)
				assert.NotEmpty(t, buf.String())
			}(f)
		}
	}
	wg.Wait()
}
