package report_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/audit/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExportFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected report.ExportFormat
		wantErr  bool
	}{
		{"xlsx", report.FormatXLSX, false},
		{"excel", report.FormatXLSX, false},
		{"json", report.FormatJSON, false},
		{"csv", report.FormatCSV, false},
		{"markdown", report.FormatMarkdown, false},
		{"md", report.FormatMarkdown, false},
		{"html", report.FormatHTML, false},
		{"table", report.FormatTable, false},
		{"unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := report.ParseExportFormat(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestExportFormats(t *testing.T) {
	rep := sampleAuditReport()

	t.Run("JSON", func(t *testing.T) {
		var buf bytes.Buffer
		err := report.ExportAuditReport(rep, report.FormatJSON, &buf)
		require.NoError(t, err)

		var parsed audit.AuditReport
		err = json.Unmarshal(buf.Bytes(), &parsed)
		require.NoError(t, err)
		assert.Equal(t, rep.Title, parsed.Title)
		assert.Len(t, parsed.UserAccessFindings, 3)
	})

	t.Run("CSV", func(t *testing.T) {
		var buf bytes.Buffer
		err := report.ExportAuditReport(rep, report.FormatCSV, &buf)
		require.NoError(t, err)

		csvContent := buf.String()
		assert.Contains(t, csvContent, "Module,Project ID,Project Name")
		assert.Contains(t, csvContent, "user_access,101,payments-api")
		assert.Contains(t, csvContent, "protected_branches,101,payments-api")
		assert.Contains(t, csvContent, "protected_environments,101,payments-api")
	})

	t.Run("Markdown", func(t *testing.T) {
		var buf bytes.Buffer
		err := report.ExportAuditReport(rep, report.FormatMarkdown, &buf)
		require.NoError(t, err)

		mdContent := buf.String()
		assert.Contains(t, mdContent, "# GitLab Fleet Compliance & Security Audit Report")
		assert.Contains(t, mdContent, "## Executive Summary")
		assert.Contains(t, mdContent, "## 1. User Access & Expiration Audit (`user_access`)")
		assert.Contains(t, mdContent, "## 2. Protected Branches Compliance Audit (`protected_branches`)")
		assert.Contains(t, mdContent, "## 3. Protected Environments Deployment Audit (`protected_environments`)")
		assert.Contains(t, mdContent, "🔴 `CRITICAL`")
	})

	t.Run("HTML", func(t *testing.T) {
		var buf bytes.Buffer
		err := report.ExportAuditReport(rep, report.FormatHTML, &buf)
		require.NoError(t, err)

		htmlContent := buf.String()
		assert.Contains(t, htmlContent, "<!DOCTYPE html>")
		assert.Contains(t, htmlContent, "GitLab Fleet Compliance & Security Audit")
		assert.Contains(t, htmlContent, "badge-critical")
		assert.Contains(t, htmlContent, "badge-pass")
	})

	t.Run("Table", func(t *testing.T) {
		var buf bytes.Buffer
		err := report.ExportAuditReport(rep, report.FormatTable, &buf)
		require.NoError(t, err)

		tableContent := buf.String()
		assert.Contains(t, tableContent, "GITLAB FLEET COMPLIANCE & SECURITY AUDIT")
		assert.Contains(t, tableContent, "[USER ACCESS & EXPIRATION FINDINGS]")
		assert.Contains(t, tableContent, "[PROTECTED BRANCHES FINDINGS]")
		assert.Contains(t, tableContent, "[PROTECTED ENVIRONMENTS FINDINGS]")
		assert.Contains(t, tableContent, "payments-api")
	})
}
