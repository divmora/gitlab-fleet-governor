package report_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
)

func createSampleReportData() *report.ReportData {
	now := time.Now()
	data := report.NewReportData(true, now.Add(-5*time.Second), now)
	data.TotalScanned = 100

	data.AddTarget(report.TargetReport{
		ID:           101,
		Path:         "platform/fleet-governor",
		ResourceType: "project",
		Status:       "CHANGED",
		HasDrift:     true,
		Duration:     250 * time.Millisecond,
		Operations: []report.OperationReport{
			{
				Name:         "push_rules",
				ResourceType: "project",
				ResourceID:   101,
				ResourcePath: "platform/fleet-governor",
				Action:       "UPDATE",
				Status:       "SUCCESS",
				HasChanges:   true,
				Duration:     50 * time.Millisecond,
				Diffs: []report.DiffReport{
					{
						Resource: "push_rule",
						Action:   "UPDATE",
						Fields: []report.FieldDiffReport{
							{
								Field:    "prevent_secrets",
								OldValue: false,
								NewValue: true,
								Action:   "UPDATE",
							},
						},
					},
				},
			},
		},
	})

	data.AddTarget(report.TargetReport{
		ID:           102,
		Path:         "platform/infra",
		ResourceType: "project",
		Status:       "UNCHANGED",
		HasDrift:     false,
		Duration:     150 * time.Millisecond,
		Operations: []report.OperationReport{
			{
				Name:         "push_rules",
				ResourceType: "project",
				ResourceID:   102,
				ResourcePath: "platform/infra",
				Action:       "NOOP",
				Status:       "NOOP",
				HasChanges:   false,
				Duration:     40 * time.Millisecond,
			},
		},
	})

	data.AddTarget(report.TargetReport{
		ID:           103,
		Path:         "platform/legacy",
		ResourceType: "project",
		Status:       "FAILED",
		HasDrift:     false,
		Duration:     80 * time.Millisecond,
		Error:        "403 Forbidden",
		Operations: []report.OperationReport{
			{
				Name:         "approval_rules",
				ResourceType: "project",
				ResourceID:   103,
				ResourcePath: "platform/legacy",
				Action:       "UPDATE",
				Status:       "FAILED",
				HasChanges:   false,
				Error:        "403 Forbidden",
				Duration:     30 * time.Millisecond,
			},
		},
	})

	return data
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected report.Format
		wantErr  bool
	}{
		{"table", report.FormatTable, false},
		{"TABLE", report.FormatTable, false},
		{"tbl", report.FormatTable, false},
		{"ascii", report.FormatTable, false},
		{"json", report.FormatJSON, false},
		{"js", report.FormatJSON, false},
		{"csv", report.FormatCSV, false},
		{"CSV", report.FormatCSV, false},
		{"markdown", report.FormatMarkdown, false},
		{"md", report.FormatMarkdown, false},
		{"gfm", report.FormatMarkdown, false},
		{"summary", report.FormatSummary, false},
		{"text", report.FormatSummary, false},
		{"unknown_format", "", true},
	}

	for _, tt := range tests {
		got, err := report.ParseFormat(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("ParseFormat(%q) expected error, got nil", tt.input)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("ParseFormat(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.expected {
			t.Errorf("ParseFormat(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestNewReporter(t *testing.T) {
	var buf bytes.Buffer
	formats := []report.Format{
		report.FormatTable,
		report.FormatJSON,
		report.FormatCSV,
		report.FormatMarkdown,
		report.FormatSummary,
	}

	for _, f := range formats {
		rep, err := report.NewReporter(f, &buf, report.WithColor(false), report.WithTitle("Custom Title"))
		if err != nil {
			t.Fatalf("NewReporter(%q) unexpected error: %v", f, err)
		}
		if rep.Format() != f {
			t.Errorf("rep.Format() = %q, expected %q", rep.Format(), f)
		}
	}

	// Nil writer check
	_, err := report.NewReporter(report.FormatTable, nil)
	if err == nil {
		t.Errorf("expected error on nil writer")
	}

	// Unknown format check
	_, err = report.NewReporter("invalid", &buf)
	if err == nil {
		t.Errorf("expected error on invalid format")
	}
}

func TestTableReporter_Render(t *testing.T) {
	data := createSampleReportData()
	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatTable, &buf, report.WithColor(true), report.WithDiffs(true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rep.Render(data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "TARGET FLEET BREAKDOWN") {
		t.Errorf("missing target fleet breakdown in table output")
	}
	if !strings.Contains(out, "platform/fleet-governor") {
		t.Errorf("missing project path in table output")
	}
	if !strings.Contains(out, "OPERATIONS RECONCILIATION SUMMARY") {
		t.Errorf("missing operations summary in table output")
	}

	// Nil data check
	if err := rep.Render(nil); err == nil {
		t.Errorf("expected error when rendering nil data")
	}

	// Empty targets check
	emptyData := report.NewReportData(false, time.Now(), time.Now())
	var emptyBuf bytes.Buffer
	emptyRep, _ := report.NewReporter(report.FormatTable, &emptyBuf, report.WithColor(false))
	if err := emptyRep.Render(emptyData); err != nil {
		t.Fatalf("unexpected error rendering empty report: %v", err)
	}
	if !strings.Contains(emptyBuf.String(), "No projects or groups matched") {
		t.Errorf("expected empty message in output")
	}
}

func TestJSONReporter_Render(t *testing.T) {
	data := createSampleReportData()
	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatJSON, &buf, report.WithIndent(true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rep.Render(data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	var parsed report.ReportData
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("JSON output invalid: %v\nJSON:\n%s", err, buf.String())
	}

	if parsed.TotalTargeted != 3 {
		t.Errorf("expected TotalTargeted=3, got %d", parsed.TotalTargeted)
	}
	if parsed.TotalChanged != 1 {
		t.Errorf("expected TotalChanged=1, got %d", parsed.TotalChanged)
	}
	if parsed.TotalFailed != 1 {
		t.Errorf("expected TotalFailed=1, got %d", parsed.TotalFailed)
	}
}

func TestCSVReporter_Render(t *testing.T) {
	data := createSampleReportData()
	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatCSV, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rep.Render(data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	r := csv.NewReader(&buf)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV read error: %v", err)
	}

	if len(records) < 4 {
		t.Fatalf("expected at least 4 CSV rows (header + records), got %d", len(records))
	}
	header := records[0]
	if header[0] != "resource_type" || header[2] != "resource_path" || header[7] != "field" {
		t.Errorf("unexpected CSV header: %v", header)
	}
}

func TestMarkdownReporter_Render(t *testing.T) {
	data := createSampleReportData()
	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatMarkdown, &buf, report.WithDiffs(true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rep.Render(data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "### 📊 Executive Summary") {
		t.Errorf("missing executive summary in markdown")
	}
	if !strings.Contains(out, "### 🎯 Target Results") {
		t.Errorf("missing target results in markdown")
	}
	if !strings.Contains(out, "### 🔍 Granular Policy Diffs") {
		t.Errorf("missing granular policy diffs in markdown")
	}
	if !strings.Contains(out, "platform/fleet-governor") {
		t.Errorf("missing project path in markdown")
	}
}

func TestSummaryReporter_Render(t *testing.T) {
	data := createSampleReportData()
	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatSummary, &buf, report.WithColor(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rep.Render(data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "GitLab Fleet Governor [DRY-RUN]") {
		t.Errorf("missing title prefix in summary output")
	}
	if !strings.Contains(out, "Targets: 3 matched") {
		t.Errorf("missing targets count in summary output")
	}
	if !strings.Contains(out, "Operations with drift:") || !strings.Contains(out, "push_rules") {
		t.Errorf("missing operations drift in summary output: %s", out)
	}
}

func TestFromExecutionResult(t *testing.T) {
	now := time.Now()
	execResult := &engine.ExecutionResult{
		Mode:        "plan",
		DryRun:      true,
		Success:     true,
		StartedAt:   now.Add(-2 * time.Second),
		CompletedAt: now,
		Duration:    2 * time.Second,
		Metrics: &engine.SummaryMetricsSnapshot{
			TotalScanned:   50,
			TotalTargeted:  2,
			TotalChanged:   1,
			TotalUnchanged: 1,
			TotalFailed:    0,
			OperationCounts: map[string]*engine.OperationSummary{
				"push_rules": {
					OperationName: "push_rules",
					Created:       1,
					Noop:          1,
				},
			},
		},
		TargetResults: []*engine.TargetResult{
			{
				TargetID:     201,
				TargetPath:   "group/proj-a",
				TargetName:   "proj-a",
				ResourceType: governance.ResourceTypeProject,
				DryRun:       true,
				Success:      true,
				HasChanges:   true,
				Duration:     100 * time.Millisecond,
				Operations: []*engine.OperationResult{
					{
						OperationName: "push_rules",
						ResourceType:  governance.ResourceTypeProject,
						ResourceID:    201,
						ResourcePath:  "group/proj-a",
						Action:        governance.ActionCreate,
						Status:        governance.StatusSuccess,
						HasChanges:    true,
						Success:       true,
						Duration:      30 * time.Millisecond,
						Diffs: []governance.Diff{
							{
								Resource: "push_rule",
								Action:   governance.ActionCreate,
								Fields: []governance.FieldDiff{
									{Field: "prevent_secrets", OldValue: nil, NewValue: true, Action: governance.ActionCreate},
								},
							},
						},
					},
				},
			},
		},
	}

	reportData := report.FromExecutionResult(execResult)
	if reportData.TotalScanned != 50 {
		t.Errorf("expected TotalScanned=50, got %d", reportData.TotalScanned)
	}
	if reportData.TotalTargeted != 2 {
		t.Errorf("expected TotalTargeted=2, got %d", reportData.TotalTargeted)
	}
	if !reportData.HasDrift() {
		t.Errorf("expected HasDrift=true")
	}
	if reportData.HasErrors() {
		t.Errorf("expected HasErrors=false")
	}
	if len(reportData.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(reportData.Targets))
	}
	if reportData.Targets[0].Path != "group/proj-a" {
		t.Errorf("unexpected target path: %s", reportData.Targets[0].Path)
	}

	// Nil ExecutionResult check
	nilReportData := report.FromExecutionResult(nil)
	if nilReportData == nil {
		t.Fatalf("expected non-nil report data for nil result")
	}
}

func TestReportData_Errors(t *testing.T) {
	data := report.NewReportData(false, time.Now(), time.Now())
	data.AddTarget(report.TargetReport{
		ID:       1,
		Path:     "failed/proj",
		Status:   "FAILED",
		HasDrift: false,
		Error:    errors.New("fatal").Error(),
	})

	if !data.HasErrors() {
		t.Errorf("expected HasErrors=true")
	}
}

func TestDefaultOptions_NoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	opts := report.DefaultOptions()
	if opts.Color {
		t.Errorf("expected Color=false when NO_COLOR is set")
	}

	t.Setenv("NO_COLOR", "")
	opts2 := report.DefaultOptions()
	if !opts2.Color {
		t.Errorf("expected Color=true when NO_COLOR is empty")
	}
}

func TestMarkdownReporter_ErrorsWithoutDrift(t *testing.T) {
	data := report.NewReportData(true, time.Now(), time.Now())
	data.AddTarget(report.TargetReport{
		ID:           103,
		Path:         "platform/legacy",
		ResourceType: "project",
		Status:       "FAILED",
		HasDrift:     false,
		Duration:     80 * time.Millisecond,
		Error:        "403 Forbidden",
		Operations: []report.OperationReport{
			{
				Name:         "approval_rules",
				ResourceType: "project",
				ResourceID:   103,
				ResourcePath: "platform/legacy",
				Action:       "UPDATE",
				Status:       "FAILED",
				HasChanges:   false,
				Error:        "403 Forbidden",
				Duration:     30 * time.Millisecond,
			},
		},
	})

	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatMarkdown, &buf, report.WithDiffs(true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rep.Render(data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "### 🔍 Granular Policy Diffs") {
		t.Errorf("expected granular diffs accordion for error target even without drift")
	}
	if !strings.Contains(out, "403 Forbidden") {
		t.Errorf("expected error message in granular diffs accordion")
	}
}

func TestCSVReporter_NilValues(t *testing.T) {
	data := report.NewReportData(true, time.Now(), time.Now())
	data.AddTarget(report.TargetReport{
		ID:           101,
		Path:         "platform/project",
		ResourceType: "project",
		Status:       "CHANGED",
		HasDrift:     true,
		Operations: []report.OperationReport{
			{
				Name:       "push_rules",
				Action:     "CREATE",
				Status:     "SUCCESS",
				HasChanges: true,
				Diffs: []report.DiffReport{
					{
						Resource: "push_rule",
						Action:   "CREATE",
						Fields: []report.FieldDiffReport{
							{
								Field:    "prevent_secrets",
								OldValue: nil,
								NewValue: true,
								Action:   "CREATE",
							},
						},
					},
				},
			},
		},
	})

	var buf bytes.Buffer
	rep, err := report.NewReporter(report.FormatCSV, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rep.Render(data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "<nil>") {
		t.Errorf("CSV output should not contain '<nil>', got:\n%s", out)
	}
}

