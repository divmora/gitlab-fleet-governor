package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// CSVReporter formats execution reports as RFC 4180 standard CSV.
type CSVReporter struct {
	out  io.Writer
	opts Options
}

// NewCSVReporter constructs a CSVReporter.
func NewCSVReporter(out io.Writer, opts Options) *CSVReporter {
	return &CSVReporter{out: out, opts: opts}
}

// Format returns FormatCSV.
func (r *CSVReporter) Format() Format {
	return FormatCSV
}

// Render writes RFC 4180 CSV rows to the writer.
func (r *CSVReporter) Render(data *ReportData) error {
	if data == nil {
		return fmt.Errorf("report data cannot be nil")
	}

	w := csv.NewWriter(r.out)
	defer w.Flush()

	// Header row
	header := []string{
		"resource_type",
		"resource_id",
		"resource_path",
		"operation",
		"action",
		"status",
		"has_drift",
		"field",
		"old_value",
		"new_value",
		"error",
		"duration_ms",
	}
	if err := w.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	for _, t := range data.Targets {
		if len(t.Operations) == 0 {
			// Write target row without operations
			row := []string{
				t.ResourceType,
				strconv.Itoa(t.ID),
				t.Path,
				"",
				"",
				t.Status,
				strconv.FormatBool(t.HasDrift),
				"",
				"",
				"",
				t.Error,
				strconv.FormatInt(t.Duration.Milliseconds(), 10),
			}
			if err := w.Write(row); err != nil {
				return err
			}
			continue
		}

		for _, op := range t.Operations {
			if len(op.Diffs) == 0 {
				row := []string{
					t.ResourceType,
					strconv.Itoa(t.ID),
					t.Path,
					op.Name,
					op.Action,
					op.Status,
					strconv.FormatBool(op.HasChanges),
					"",
					"",
					"",
					op.Error,
					strconv.FormatInt(op.Duration.Milliseconds(), 10),
				}
				if err := w.Write(row); err != nil {
					return err
				}
				continue
			}

			for _, diff := range op.Diffs {
				if len(diff.Fields) == 0 {
					row := []string{
						t.ResourceType,
						strconv.Itoa(t.ID),
						t.Path,
						op.Name,
						op.Action,
						op.Status,
						strconv.FormatBool(op.HasChanges),
						diff.Resource,
						"",
						"",
						op.Error,
						strconv.FormatInt(op.Duration.Milliseconds(), 10),
					}
					if err := w.Write(row); err != nil {
						return err
					}
					continue
				}

				for _, f := range diff.Fields {
					oldVal := ""
					if f.OldValue != nil {
						oldVal = fmt.Sprintf("%v", f.OldValue)
					}
					newVal := ""
					if f.NewValue != nil {
						newVal = fmt.Sprintf("%v", f.NewValue)
					}
					row := []string{
						t.ResourceType,
						strconv.Itoa(t.ID),
						t.Path,
						op.Name,
						string(f.Action),
						op.Status,
						"true",
						f.Field,
						oldVal,
						newVal,
						op.Error,
						strconv.FormatInt(op.Duration.Milliseconds(), 10),
					}
					if err := w.Write(row); err != nil {
						return err
					}
				}
			}
		}
	}

	w.Flush()
	return w.Error()
}
