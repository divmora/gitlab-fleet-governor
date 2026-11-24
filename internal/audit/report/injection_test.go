package report_test

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	report "github.com/divmora/gitlab-fleet-governor/internal/audit/report"
)

const xss = `<script>alert(1)</script>`

// hostileReport carries attacker-controlled GitLab data (display names,
// branch names, URLs) into every findings table.
func hostileReport() *audit.AuditReport {
	evilURL := "javascript:alert(document.domain)"
	r := &audit.AuditReport{
		Title: "t",
		UserAccessFindings: []audit.UserAccessFinding{{
			ProjectID: 1, ProjectPath: "g/p", ProjectWebURL: evilURL, Username: "mallory",
			Name: xss, AccessRoleName: xss, Severity: audit.SeverityHigh, Details: xss, Remediation: xss,
		}},
		BotAccessFindings: []audit.UserAccessFinding{{
			ProjectID: 1, ProjectPath: "g/p", ProjectWebURL: evilURL, Username: xss, Name: "=HYPERLINK(\"http://evil\")",
			IsBot: true, Severity: audit.SeverityHigh, Details: xss,
		}},
		ProtectedBranchFindings: []audit.ProtectedBranchFinding{{
			ProjectID: 1, ProjectPath: "g/p", ProjectWebURL: evilURL, BranchName: xss, PushAccessLevelsSummary: xss,
			Severity: audit.Severity(xss), Details: xss, Remediation: "+cmd|' /C calc'!A0",
		}},
		ProtectedEnvFindings:      []audit.ProtectedEnvironmentFinding{{ProjectID: 1, ProjectPath: "g/p", ProjectWebURL: evilURL, EnvironmentName: xss, DeployAccessLevelsSummary: xss, Severity: audit.SeverityHigh, Details: xss}},
		PipelineRetentionFindings: []audit.PipelineRetentionFinding{{ProjectID: 1, ProjectPath: "g/p", ProjectWebURL: evilURL, Severity: audit.SeverityHigh, Details: xss, Remediation: "@SUM(1+1)"}},
		RepositoryFileFindings:    []audit.RepositoryFileFinding{{ProjectID: 1, ProjectPath: "g/p", ProjectWebURL: evilURL, FilePath: xss, Severity: audit.SeverityHigh, Details: xss}},
		ProtectedTagFindings:      []audit.ProtectedTagFinding{{ProjectID: 1, ProjectPath: "g/p", ProjectWebURL: evilURL, TagNamePattern: xss, CreateAccessLevelsSummary: xss, Severity: audit.SeverityHigh, Details: xss}},
		UserDirectory:             []*audit.UserInfo{{ID: 9, Username: "mallory", Name: xss, WebURL: "javascript:alert(2)"}},
	}
	r.ComputeSummary()
	return r
}

func render(t *testing.T, f report.ExportFormat) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, report.ExportAuditReport(hostileReport(), f, &buf))
	return buf.String()
}

func TestHTMLReport_EscapesGitLabData(t *testing.T) {
	page := render(t, report.FormatHTML)
	assert.NotContains(t, page, "<script>alert(1)</script>")
	assert.Contains(t, page, "&lt;script&gt;alert(1)&lt;/script&gt;")
	assert.NotContains(t, page, "javascript:")
	assert.Contains(t, page, `href="#"`)
	assert.Contains(t, page, `rel="noopener noreferrer"`)
	assert.Contains(t, page, "<b>", "fixed formatting markup is kept")
}

func TestHTMLReport_KeepsWebLinks(t *testing.T) {
	r := hostileReport()
	r.ProtectedTagFindings[0].ProjectWebURL = "https://gitlab.example.com/g/p?a=1&b=2"
	var buf bytes.Buffer
	require.NoError(t, report.ExportAuditReport(r, report.FormatHTML, &buf))
	assert.Contains(t, buf.String(), `href="https://gitlab.example.com/g/p?a=1&amp;b=2"`)
}

func TestCSVReport_NeutralisesFormulas(t *testing.T) {
	rows, err := csv.NewReader(strings.NewReader(render(t, report.FormatCSV))).ReadAll()
	require.NoError(t, err)
	var cells []string
	for _, row := range rows[1:] {
		cells = append(cells, row...)
	}
	for _, c := range cells {
		if c == "" {
			continue
		}
		assert.NotContains(t, "=+@\t\r", c[:1], "cell %q would be evaluated as a formula", c)
	}
	joined := strings.Join(cells, "\n")
	assert.Contains(t, joined, `'+cmd|' /C calc'!A0`)
	assert.Contains(t, joined, "'@SUM(1+1)")
}

func TestMarkdownReport_DropsUnsafeLinks(t *testing.T) {
	md := render(t, report.FormatMarkdown)
	assert.NotContains(t, md, "javascript:")
}

func TestXLSXReport_DropsUnsafeHyperlinks(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.ExportAuditReport(hostileReport(), report.FormatXLSX, &buf))
	wb, err := excelize.OpenReader(&buf)
	require.NoError(t, err)
	defer func() { _ = wb.Close() }()
	for _, sheet := range wb.GetSheetList() {
		rows, err := wb.GetRows(sheet)
		require.NoError(t, err)
		for r, row := range rows {
			for c := range row {
				cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
				ok, link, err := wb.GetCellHyperLink(sheet, cell)
				require.NoError(t, err)
				if ok {
					assert.NotContains(t, link, "javascript:", "%s!%s", sheet, cell)
				}
			}
		}
	}
}
