package report

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// SummaryReporter formats execution reports as a concise plain text block.
type SummaryReporter struct {
	out  io.Writer
	opts Options
}

// NewSummaryReporter constructs a SummaryReporter.
func NewSummaryReporter(out io.Writer, opts Options) *SummaryReporter {
	return &SummaryReporter{out: out, opts: opts}
}

// Format returns FormatSummary.
func (r *SummaryReporter) Format() Format {
	return FormatSummary
}

// Render writes a concise metric summary string to the writer.
func (r *SummaryReporter) Render(data *ReportData) error {
	if data == nil {
		return fmt.Errorf("report data cannot be nil")
	}

	c := newColorPalette(r.opts.Color)
	var sb strings.Builder

	mode := "APPLY"
	if data.DryRun {
		mode = "DRY-RUN"
	}

	status := c.green("SUCCESS")
	if data.HasErrors() {
		status = c.red("FAILED")
	} else if data.HasDrift() {
		status = c.yellow("DRIFT_DETECTED")
	}

	sb.WriteString(fmt.Sprintf("GitLab Fleet Governor [%s] %s in %s\n",
		mode, status, data.Duration.Round(time.Millisecond).String()))
	sb.WriteString(fmt.Sprintf("Targets: %d matched (%d scanned) | Changed: %d | Compliant: %d | Failed: %d\n",
		data.TotalTargeted, data.TotalScanned, data.TotalChanged, data.TotalUnchanged, data.TotalFailed))

	if len(data.OperationMetrics) > 0 {
		var opSummaries []string
		for _, name := range sortedOpNames(data.OperationMetrics) {
			m := data.OperationMetrics[name]
			if m.Changed > 0 || m.Failed > 0 {
				opSummaries = append(opSummaries, fmt.Sprintf("%s (%d drift, %d fail)", name, m.Changed, m.Failed))
			}
		}
		if len(opSummaries) > 0 {
			sb.WriteString(fmt.Sprintf("Operations with drift: %s\n", strings.Join(opSummaries, ", ")))
		} else {
			sb.WriteString("Operations: all in sync\n")
		}
	}

	_, err := io.WriteString(r.out, sb.String())
	return err
}
