package report

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// MarkdownReporter formats execution reports as GitHub-Flavored Markdown.
type MarkdownReporter struct {
	out  io.Writer
	opts Options
}

// NewMarkdownReporter constructs a MarkdownReporter.
func NewMarkdownReporter(out io.Writer, opts Options) *MarkdownReporter {
	return &MarkdownReporter{out: out, opts: opts}
}

// Format returns FormatMarkdown.
func (r *MarkdownReporter) Format() Format {
	return FormatMarkdown
}

// Render writes GFM markdown summary tables to the writer.
func (r *MarkdownReporter) Render(data *ReportData) error {
	if data == nil {
		return fmt.Errorf("report data cannot be nil")
	}

	var sb strings.Builder

	// Main Title
	sb.WriteString(fmt.Sprintf("# 🛡️ %s\n\n", r.opts.Title))

	// Status Callout Box
	if data.DryRun {
		sb.WriteString("> ⚠️ **Execution Mode**: **DRY-RUN SIMULATION** (No live mutations applied).\n\n")
	} else if data.HasErrors() {
		sb.WriteString("> ❌ **Execution Mode**: **LIVE APPLY** completed with errors.\n\n")
	} else {
		sb.WriteString("> ✅ **Execution Mode**: **LIVE APPLY** completed successfully.\n\n")
	}

	// Execution Metadata Table
	sb.WriteString("### 📊 Executive Summary\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|---|---|\n")
	modeText := "LIVE APPLY"
	if data.DryRun {
		modeText = "DRY-RUN"
	}
	sb.WriteString(fmt.Sprintf("| **Execution Mode** | `%s` |\n", modeText))
	sb.WriteString(fmt.Sprintf("| **Started At** | `%s` |\n", data.StartedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("| **Completed At** | `%s` |\n", data.CompletedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("| **Total Duration** | `%s` |\n", data.Duration.Round(time.Millisecond).String()))
	sb.WriteString(fmt.Sprintf("| **Total Scanned** | `%d` |\n", data.TotalScanned))
	sb.WriteString(fmt.Sprintf("| **Targeted Fleet** | `%d` |\n", data.TotalTargeted))
	sb.WriteString(fmt.Sprintf("| **Drift / Changed** | `%d` |\n", data.TotalChanged))
	sb.WriteString(fmt.Sprintf("| **Compliant / Unchanged** | `%d` |\n", data.TotalUnchanged))
	sb.WriteString(fmt.Sprintf("| **Failed / Errors** | `%d` |\n\n", data.TotalFailed))

	// Operations Summary Table
	if len(data.OperationMetrics) > 0 {
		sb.WriteString("### ⚙️ Operations Breakdown\n\n")
		sb.WriteString("| Operation | Drift / Changed | Compliant | Failed | Skipped |\n")
		sb.WriteString("|---|:---:|:---:|:---:|:---:|\n")
		for _, name := range sortedOpNames(data.OperationMetrics) {
			m := data.OperationMetrics[name]
			sb.WriteString(fmt.Sprintf("| `%s` | %d | %d | %d | %d |\n",
				name, m.Changed, m.Unchanged, m.Failed, m.Skipped))
		}
		sb.WriteString("\n")
	}

	// Targets Breakdown Table
	if len(data.Targets) > 0 {
		sb.WriteString("### 🎯 Target Results\n\n")
		sb.WriteString("| Type | ID | Path | Status | Duration |\n")
		sb.WriteString("|:---:|:---:|---|:---:|:---:|\n")

		for _, t := range data.Targets {
			typeBadge := "`Project`"
			if t.ResourceType == "group" {
				typeBadge = "`Group`"
			}

			statusBadge := "🟢 Compliant"
			if t.Status == "FAILED" {
				statusBadge = "🔴 Failed"
			} else if t.HasDrift || t.Status == "CHANGED" {
				if data.DryRun {
					statusBadge = "🟡 Drift"
				} else {
					statusBadge = "🟡 Changed"
				}
			}

			sb.WriteString(fmt.Sprintf("| %s | `%d` | `%s` | %s | `%s` |\n",
				typeBadge, t.ID, t.Path, statusBadge, t.Duration.Round(time.Millisecond).String()))
		}
		sb.WriteString("\n")

		// Granular Diffs Accordion
		if r.opts.IncludeDiffs && (data.HasDrift() || data.HasErrors()) {
			sb.WriteString("### 🔍 Granular Policy Diffs\n\n")
			for _, t := range data.Targets {
				if !t.HasDrift && t.Status != "FAILED" {
					continue
				}

				sb.WriteString(fmt.Sprintf("<details>\n<summary><b>%s (%s)</b> — %s</summary>\n\n",
					t.Path, t.ResourceType, t.Status))

				if t.Error != "" {
					sb.WriteString(fmt.Sprintf("> ❌ **Error**: `%s`\n\n", t.Error))
				}

				for _, op := range t.Operations {
					if !op.HasChanges && op.Status != "FAILED" {
						continue
					}

					sb.WriteString(fmt.Sprintf("#### Operation: `%s` (`%s`)\n\n", op.Name, op.Action))
					if len(op.Diffs) > 0 {
						sb.WriteString("```diff\n")
						for _, d := range op.Diffs {
							for _, f := range d.Fields {
								switch f.Action {
								case "CREATE":
									sb.WriteString(fmt.Sprintf("+ %s: %v\n", f.Field, f.NewValue))
								case "DELETE":
									sb.WriteString(fmt.Sprintf("- %s: %v\n", f.Field, f.OldValue))
								case "UPDATE":
									sb.WriteString(fmt.Sprintf("! %s: %v -> %v\n", f.Field, f.OldValue, f.NewValue))
								default:
									sb.WriteString(fmt.Sprintf("  %s: %v\n", f.Field, f.OldValue))
								}
							}
						}
						sb.WriteString("```\n\n")
					}
				}

				sb.WriteString("</details>\n\n")
			}
		}
	}

	_, err := io.WriteString(r.out, sb.String())
	return err
}
