package report

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/engine"
)

// Format represents a supported summary report presentation format.
type Format string

const (
	// FormatTable renders an ASCII/Unicode terminal table with ANSI color highlights.
	FormatTable Format = "table"
	// FormatJSON renders structured machine-readable JSON (compact or indented).
	FormatJSON Format = "json"
	// FormatCSV renders RFC 4180 standard comma-separated values.
	FormatCSV Format = "csv"
	// FormatMarkdown renders GitHub-Flavored Markdown tables for CI/CD summaries.
	FormatMarkdown Format = "markdown"
	// FormatSummary renders a concise high-density plain text metric summary.
	FormatSummary Format = "summary"
)

// ParseFormat converts a format string (case-insensitive) to a canonical Format.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "table", "tbl", "ascii":
		return FormatTable, nil
	case "json", "js":
		return FormatJSON, nil
	case "csv":
		return FormatCSV, nil
	case "markdown", "md", "gfm":
		return FormatMarkdown, nil
	case "summary", "text", "short", "txt":
		return FormatSummary, nil
	default:
		return "", fmt.Errorf("unsupported report format %q (valid: table, json, csv, markdown, summary)", s)
	}
}

// Reporter is the core interface for rendering fleet governance execution reports.
type Reporter interface {
	// Render formats the ReportData and writes it to the underlying output writer.
	Render(data *ReportData) error
	// Format returns the format type implemented by this reporter.
	Format() Format
}

// Options configures report rendering behavior.
type Options struct {
	Color        bool   // Enable ANSI color codes (for table and summary formats)
	IndentJSON   bool   // Indent JSON output with two spaces
	IncludeDiffs bool   // Include granular field-level diffs in report output
	Title        string // Optional custom title override
}

// Option is a functional configuration option for reporters.
type Option func(*Options)

// WithColor sets ANSI color formatting on or off.
func WithColor(enabled bool) Option {
	return func(o *Options) {
		o.Color = enabled
	}
}

// WithIndent sets JSON pretty-printing.
func WithIndent(indent bool) Option {
	return func(o *Options) {
		o.IndentJSON = indent
	}
}

// WithDiffs controls whether granular field diffs are included in output.
func WithDiffs(include bool) Option {
	return func(o *Options) {
		o.IncludeDiffs = include
	}
}

// WithTitle sets a custom report header title.
func WithTitle(title string) Option {
	return func(o *Options) {
		o.Title = title
	}
}

// DefaultOptions returns standard sensible rendering options.
func DefaultOptions() Options {
	color := true
	if os.Getenv("NO_COLOR") != "" {
		color = false
	}
	return Options{
		Color:        color,
		IndentJSON:   true,
		IncludeDiffs: true,
		Title:        "GitLab Fleet Governor Execution Report",
	}
}

// NewReporter creates a new Reporter implementation matching the requested format.
func NewReporter(format Format, out io.Writer, opts ...Option) (Reporter, error) {
	if out == nil {
		return nil, fmt.Errorf("output writer cannot be nil")
	}

	options := DefaultOptions()
	for _, opt := range opts {
		opt(&options)
	}

	switch format {
	case FormatTable:
		return NewTableReporter(out, options), nil
	case FormatJSON:
		return NewJSONReporter(out, options), nil
	case FormatCSV:
		return NewCSVReporter(out, options), nil
	case FormatMarkdown:
		return NewMarkdownReporter(out, options), nil
	case FormatSummary:
		return NewSummaryReporter(out, options), nil
	default:
		return nil, fmt.Errorf("unsupported report format %q", format)
	}
}

// ============================================================================
// Report Data Models (Canonical DTO)
// ============================================================================

// ReportData represents the canonical execution summary model passed to all reporters.
type ReportData struct {
	Title            string               `json:"title"`
	StartedAt        time.Time            `json:"started_at"`
	CompletedAt      time.Time            `json:"completed_at"`
	Duration         time.Duration        `json:"duration"`
	DurationString   string               `json:"duration_human"`
	DryRun           bool                 `json:"dry_run"`
	TotalScanned     int                  `json:"total_scanned"`
	TotalTargeted    int                  `json:"total_targeted"`
	TotalChanged     int                  `json:"total_changed"`
	TotalUnchanged   int                  `json:"total_unchanged"`
	TotalFailed      int                  `json:"total_failed"`
	OperationMetrics map[string]*OpMetric `json:"operation_metrics,omitempty"`
	Targets          []TargetReport       `json:"targets,omitempty"`
}

// OpMetric captures aggregate counts for a specific governance operation.
type OpMetric struct {
	Name      string `json:"name"`
	Changed   int    `json:"changed"`
	Unchanged int    `json:"unchanged"`
	Failed    int    `json:"failed"`
	Skipped   int    `json:"skipped"`
}

// TargetReport encapsulates the governance results for a single project or group.
type TargetReport struct {
	ID           int               `json:"id"`
	Path         string            `json:"path"`
	ResourceType string            `json:"resource_type"` // "project" or "group"
	Status       string            `json:"status"`        // "CHANGED", "UNCHANGED", "FAILED", "SKIPPED"
	HasDrift     bool              `json:"has_drift"`
	Duration     time.Duration     `json:"duration"`
	Error        string            `json:"error,omitempty"`
	Operations   []OperationReport `json:"operations,omitempty"`
}

// OperationReport captures the result of a single operation on a target resource.
type OperationReport struct {
	Name         string        `json:"name"`
	ResourceType string        `json:"resource_type"`
	ResourceID   int           `json:"resource_id"`
	ResourcePath string        `json:"resource_path"`
	Action       string        `json:"action"` // "CREATE", "UPDATE", "DELETE", "NOOP", "AUDIT", "SKIPPED"
	Status       string        `json:"status"` // "SUCCESS", "FAILED", "NOOP", "SKIPPED"
	HasChanges   bool          `json:"has_changes"`
	Diffs        []DiffReport  `json:"diffs,omitempty"`
	Error        string        `json:"error,omitempty"`
	Duration     time.Duration `json:"duration"`
}

// DiffReport captures a single resource entity diff.
type DiffReport struct {
	Resource string            `json:"resource"`
	Action   string            `json:"action"`
	Details  string            `json:"details,omitempty"`
	Fields   []FieldDiffReport `json:"fields,omitempty"`
}

// FieldDiffReport captures attribute-level differences.
type FieldDiffReport struct {
	Field    string `json:"field"`
	OldValue any    `json:"old_value"`
	NewValue any    `json:"new_value"`
	Action   string `json:"action"`
}

// NewReportData initializes a ReportData instance with pre-calculated duration.
func NewReportData(dryRun bool, startedAt, completedAt time.Time) *ReportData {
	duration := completedAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	return &ReportData{
		Title:            "GitLab Fleet Governor Execution Report",
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
		Duration:         duration,
		DurationString:   duration.Round(time.Millisecond).String(),
		DryRun:           dryRun,
		OperationMetrics: make(map[string]*OpMetric),
		Targets:          make([]TargetReport, 0),
	}
}

// AddTarget appends a target report and updates aggregate counters.
func (r *ReportData) AddTarget(target TargetReport) {
	r.Targets = append(r.Targets, target)
	r.TotalTargeted++

	switch target.Status {
	case "FAILED":
		r.TotalFailed++
	case "CHANGED":
		r.TotalChanged++
	case "UNCHANGED", "SUCCESS":
		if target.HasDrift {
			r.TotalChanged++
		} else {
			r.TotalUnchanged++
		}
	default:
		r.TotalUnchanged++
	}

	// Aggregate operation metrics
	for _, op := range target.Operations {
		metric, exists := r.OperationMetrics[op.Name]
		if !exists {
			metric = &OpMetric{Name: op.Name}
			r.OperationMetrics[op.Name] = metric
		}

		if op.Status == "FAILED" {
			metric.Failed++
		} else if op.Action == "SKIPPED" || op.Status == "SKIPPED" {
			metric.Skipped++
		} else if op.HasChanges || (op.Action != "NOOP" && op.Action != "SKIPPED") {
			metric.Changed++
		} else {
			metric.Unchanged++
		}
	}
}

// HasDrift returns true if any target experienced drift/changes.
func (r *ReportData) HasDrift() bool {
	return r.TotalChanged > 0
}

// HasErrors returns true if any target failed.
func (r *ReportData) HasErrors() bool {
	return r.TotalFailed > 0
}

// FromExecutionResult converts an engine.ExecutionResult into a canonical ReportData.
func FromExecutionResult(res *engine.ExecutionResult) *ReportData {
	if res == nil {
		return NewReportData(true, time.Now(), time.Now())
	}

	rd := NewReportData(res.DryRun, res.StartedAt, res.CompletedAt)
	if res.Metrics != nil {
		rd.TotalScanned = res.Metrics.TotalScanned
		rd.TotalTargeted = res.Metrics.TotalTargeted
		rd.TotalChanged = res.Metrics.TotalChanged
		rd.TotalUnchanged = res.Metrics.TotalUnchanged
		rd.TotalFailed = res.Metrics.TotalFailed

		for name, opSummary := range res.Metrics.OperationCounts {
			rd.OperationMetrics[name] = &OpMetric{
				Name:      name,
				Changed:   opSummary.Created + opSummary.Updated + opSummary.Deleted,
				Unchanged: opSummary.Noop,
				Failed:    opSummary.Failed,
				Skipped:   opSummary.Skipped,
			}
		}
	}

	for _, tr := range res.TargetResults {
		if tr == nil {
			continue
		}
		status := "UNCHANGED"
		if !tr.Success || tr.Error != nil {
			status = "FAILED"
		} else if tr.HasChanges {
			status = "CHANGED"
		}

		errStr := ""
		if tr.Error != nil {
			errStr = tr.Error.Error()
		}

		opReports := make([]OperationReport, 0, len(tr.Operations))
		for _, op := range tr.Operations {
			if op == nil {
				continue
			}
			opErrStr := ""
			if op.Error != nil {
				opErrStr = op.Error.Error()
			}

			diffReports := make([]DiffReport, 0, len(op.Diffs))
			for _, d := range op.Diffs {
				fieldReports := make([]FieldDiffReport, 0, len(d.Fields))
				for _, f := range d.Fields {
					fieldReports = append(fieldReports, FieldDiffReport{
						Field:    f.Field,
						OldValue: f.OldValue,
						NewValue: f.NewValue,
						Action:   string(f.Action),
					})
				}
				diffReports = append(diffReports, DiffReport{
					Resource: d.Resource,
					Action:   string(d.Action),
					Details:  d.Details,
					Fields:   fieldReports,
				})
			}

			opReports = append(opReports, OperationReport{
				Name:         op.OperationName,
				ResourceType: string(op.ResourceType),
				ResourceID:   op.ResourceID,
				ResourcePath: op.ResourcePath,
				Action:       string(op.Action),
				Status:       string(op.Status),
				HasChanges:   op.HasChanges,
				Diffs:        diffReports,
				Error:        opErrStr,
				Duration:     op.Duration,
			})
		}

		rd.Targets = append(rd.Targets, TargetReport{
			ID:           tr.TargetID,
			Path:         tr.TargetPath,
			ResourceType: string(tr.ResourceType),
			Status:       status,
			HasDrift:     tr.HasChanges,
			Duration:     tr.Duration,
			Error:        errStr,
			Operations:   opReports,
		})
	}

	return rd
}
