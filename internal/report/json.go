package report

import (
	"encoding/json"
	"fmt"
	"io"
)

// JSONReporter formats execution reports as compact or indented JSON.
type JSONReporter struct {
	out  io.Writer
	opts Options
}

// NewJSONReporter constructs a JSONReporter.
func NewJSONReporter(out io.Writer, opts Options) *JSONReporter {
	return &JSONReporter{out: out, opts: opts}
}

// Format returns FormatJSON.
func (r *JSONReporter) Format() Format {
	return FormatJSON
}

// Render encodes ReportData to JSON and writes to the underlying writer.
func (r *JSONReporter) Render(data *ReportData) error {
	if data == nil {
		return fmt.Errorf("report data cannot be nil")
	}

	enc := json.NewEncoder(r.out)
	if r.opts.IndentJSON {
		enc.SetIndent("", "  ")
	}
	enc.SetEscapeHTML(false)

	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("failed to encode JSON report: %w", err)
	}
	return nil
}
