package report

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// TableReporter formats execution reports as an ASCII table with ANSI colors.
type TableReporter struct {
	out  io.Writer
	opts Options
}

// NewTableReporter constructs a TableReporter.
func NewTableReporter(out io.Writer, opts Options) *TableReporter {
	return &TableReporter{out: out, opts: opts}
}

// Format returns FormatTable.
func (r *TableReporter) Format() Format {
	return FormatTable
}

// Render writes the formatted ASCII table report to the writer.
func (r *TableReporter) Render(data *ReportData) error {
	if data == nil {
		return fmt.Errorf("report data cannot be nil")
	}

	c := newColorPalette(r.opts.Color)
	var sb strings.Builder

	// Header Banner
	sb.WriteString("\n")
	sb.WriteString(c.bold("╔══════════════════════════════════════════════════════════════════════════════════╗\n"))
	titleLine := fmt.Sprintf("║  %-76s  ║\n", r.opts.Title)
	sb.WriteString(c.bold(titleLine))
	sb.WriteString(c.bold("╠══════════════════════════════════════════════════════════════════════════════════╣\n"))

	modeStr := c.green("LIVE APPLY")
	if data.DryRun {
		modeStr = c.yellow("DRY-RUN SIMULATION (No mutations applied)")
	}
	sb.WriteString(fmt.Sprintf("║  Mode: %-71s ║\n", modeStr))
	sb.WriteString(fmt.Sprintf("║  Duration: %-15s Started: %-19s Finished: %-13s ║\n",
		data.Duration.Round(time.Millisecond).String(),
		data.StartedAt.Format("2006-01-02 15:04:05"),
		data.CompletedAt.Format("15:04:05"),
	))

	// KPI Metrics Bar
	metricsLine := fmt.Sprintf("Scanned: %d | Targeted: %d | Changed: %s | Compliant: %s | Failed: %s",
		data.TotalScanned,
		data.TotalTargeted,
		c.colorizeIf(data.TotalChanged > 0, c.yellow(fmt.Sprintf("%d", data.TotalChanged)), fmt.Sprintf("%d", data.TotalChanged)),
		c.green(fmt.Sprintf("%d", data.TotalUnchanged)),
		c.colorizeIf(data.TotalFailed > 0, c.red(fmt.Sprintf("%d", data.TotalFailed)), fmt.Sprintf("%d", data.TotalFailed)),
	)
	sb.WriteString(fmt.Sprintf("║  Metrics: %-70s ║\n", metricsLine))
	sb.WriteString(c.bold("╚══════════════════════════════════════════════════════════════════════════════════╝\n\n"))

	// Targets Table
	if len(data.Targets) == 0 {
		sb.WriteString(c.yellow("  No projects or groups matched target selector criteria.\n\n"))
		_, err := io.WriteString(r.out, sb.String())
		return err
	}

	sb.WriteString(c.bold("TARGET FLEET BREAKDOWN:\n"))
	sb.WriteString(c.bold("┌─────┬──────────────┬──────────────────────────────────────────────┬───────────┬────────────┐\n"))
	sb.WriteString(c.bold("│ TYPE│ ID           │ RESOURCE PATH                                │ STATUS    │ DURATION   │\n"))
	sb.WriteString(c.bold("├─────┼──────────────┼──────────────────────────────────────────────┼───────────┼────────────┤\n"))

	for _, t := range data.Targets {
		typeTag := "[P]"
		if t.ResourceType == "group" {
			typeTag = "[G]"
		}

		path := t.Path
		if len(path) > 44 {
			path = "..." + path[len(path)-41:]
		}

		statusBadge := c.green("COMPLIANT")
		if t.Status == "FAILED" {
			statusBadge = c.red("FAILED   ")
		} else if t.HasDrift || t.Status == "CHANGED" {
			if data.DryRun {
				statusBadge = c.yellow("DRIFT    ")
			} else {
				statusBadge = c.yellow("CHANGED  ")
			}
		}

		sb.WriteString(fmt.Sprintf("│ %-3s │ %-12d │ %-44s │ %-9s │ %-10s │\n",
			typeTag,
			t.ID,
			path,
			statusBadge,
			t.Duration.Round(time.Millisecond).String(),
		))

		// Render Diffs if requested and present
		if r.opts.IncludeDiffs && (t.HasDrift || t.Status == "FAILED" || len(t.Operations) > 0) {
			for _, op := range t.Operations {
				if op.HasChanges || op.Status == "FAILED" {
					actionColor := c.yellow(op.Action)
					if op.Status == "FAILED" {
						actionColor = c.red("FAILED")
					}
					sb.WriteString(fmt.Sprintf("│     ├─► %-22s [%s]", op.Name, actionColor))
					if op.Error != "" {
						sb.WriteString(fmt.Sprintf(" error: %s", c.red(op.Error)))
					}
					sb.WriteString("\n")

					for _, diff := range op.Diffs {
						if len(diff.Fields) > 0 {
							for _, f := range diff.Fields {
								diffLine := formatFieldDiff(f, c)
								sb.WriteString(fmt.Sprintf("│     │     %s\n", diffLine))
							}
						} else if diff.Details != "" {
							sb.WriteString(fmt.Sprintf("│     │     %s: %s\n", diff.Resource, diff.Details))
						}
					}
				}
			}
		}
	}

	sb.WriteString(c.bold("└─────┴──────────────┴──────────────────────────────────────────────┴───────────┴────────────┘\n\n"))

	// Operations Summary Section
	if len(data.OperationMetrics) > 0 {
		sb.WriteString(c.bold("OPERATIONS RECONCILIATION SUMMARY:\n"))
		sb.WriteString(c.bold("┌──────────────────────────────────┬───────────┬─────────────┬──────────┬──────────┐\n"))
		sb.WriteString(c.bold("│ OPERATION                        │ DRIFT/CHG │ COMPLIANT   │ FAILED   │ SKIPPED  │\n"))
		sb.WriteString(c.bold("├──────────────────────────────────┼───────────┼─────────────┼──────────┼──────────┤\n"))

		for _, name := range sortedOpNames(data.OperationMetrics) {
			m := data.OperationMetrics[name]
			chgStr := fmt.Sprintf("%d", m.Changed)
			if m.Changed > 0 {
				chgStr = c.yellow(chgStr)
			}
			failStr := fmt.Sprintf("%d", m.Failed)
			if m.Failed > 0 {
				failStr = c.red(failStr)
			}
			sb.WriteString(fmt.Sprintf("│ %-32s │ %-9s │ %-11d │ %-8s │ %-8d │\n",
				name, chgStr, m.Unchanged, failStr, m.Skipped))
		}
		sb.WriteString(c.bold("└──────────────────────────────────┴───────────┴─────────────┴──────────┴──────────┘\n\n"))
	}

	_, err := io.WriteString(r.out, sb.String())
	return err
}

func formatFieldDiff(f FieldDiffReport, c *colorPalette) string {
	switch f.Action {
	case "CREATE":
		return c.green(fmt.Sprintf("+ %s: %v", f.Field, f.NewValue))
	case "DELETE":
		return c.red(fmt.Sprintf("- %s: %v", f.Field, f.OldValue))
	case "UPDATE":
		return c.yellow(fmt.Sprintf("~ %s: %v -> %v", f.Field, f.OldValue, f.NewValue))
	case "AUDIT":
		return c.magenta(fmt.Sprintf("! %s: %v", f.Field, f.OldValue))
	default:
		return fmt.Sprintf("  %s: %v", f.Field, f.OldValue)
	}
}

func sortedOpNames(m map[string]*OpMetric) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

type colorPalette struct {
	enabled bool
}

func newColorPalette(enabled bool) *colorPalette {
	return &colorPalette{enabled: enabled}
}

func (c *colorPalette) bold(s string) string {
	if !c.enabled {
		return s
	}
	return "\033[1m" + s + "\033[0m"
}

func (c *colorPalette) green(s string) string {
	if !c.enabled {
		return s
	}
	return "\033[32m" + s + "\033[0m"
}

func (c *colorPalette) yellow(s string) string {
	if !c.enabled {
		return s
	}
	return "\033[33m" + s + "\033[0m"
}

func (c *colorPalette) red(s string) string {
	if !c.enabled {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

func (c *colorPalette) magenta(s string) string {
	if !c.enabled {
		return s
	}
	return "\033[35m" + s + "\033[0m"
}

func (c *colorPalette) colorizeIf(cond bool, colored, plain string) string {
	if cond && c.enabled {
		return colored
	}
	return plain
}
