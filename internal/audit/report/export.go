package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
)

// ExportFormat specifies the target serial format for the audit report.
type ExportFormat string

const (
	FormatXLSX     ExportFormat = "xlsx"
	FormatJSON     ExportFormat = "json"
	FormatCSV      ExportFormat = "csv"
	FormatMarkdown ExportFormat = "markdown"
	FormatHTML     ExportFormat = "html"
	FormatTable    ExportFormat = "table"
)

// ParseExportFormat parses a user-provided format string.
func ParseExportFormat(f string) (ExportFormat, error) {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "xlsx", "excel":
		return FormatXLSX, nil
	case "json", "js":
		return FormatJSON, nil
	case "csv":
		return FormatCSV, nil
	case "markdown", "md", "gfm":
		return FormatMarkdown, nil
	case "html", "htm":
		return FormatHTML, nil
	case "table", "tbl", "text", "summary":
		return FormatTable, nil
	default:
		return "", fmt.Errorf("unsupported audit report format %q (valid: xlsx, json, csv, markdown, html, table)", f)
	}
}

// ExportAuditReport renders the audit report to out in the requested format.
func ExportAuditReport(report *audit.AuditReport, format ExportFormat, out io.Writer) error {
	if report == nil {
		return fmt.Errorf("audit report cannot be nil")
	}
	if out == nil {
		return fmt.Errorf("output writer cannot be nil")
	}

	switch format {
	case FormatXLSX:
		return GenerateXLSX(report, out)
	case FormatJSON:
		return exportJSON(report, out)
	case FormatCSV:
		return exportCSV(report, out)
	case FormatMarkdown:
		return exportMarkdown(report, out)
	case FormatHTML:
		return exportHTML(report, out)
	case FormatTable:
		return exportTable(report, out)
	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}
}

func exportJSON(report *audit.AuditReport, out io.Writer) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func exportCSV(report *audit.AuditReport, out io.Writer) error {
	w := csv.NewWriter(out)
	defer w.Flush()

	headers := []string{
		"Category",
		"Project ID",
		"Project Name",
		"Project Path",
		"Project State",
		"Project URL",
		"Entity Name",
		"Role / Branch / Env",
		"Account Type",
		"Status",
		"Violation Type",
		"Details",
		"Remediation",
	}
	if err := w.Write(headers); err != nil {
		return err
	}

	for _, f := range report.UserAccessFindings {
		row := []string{
			"user_access",
			strconv.Itoa(f.ProjectID),
			f.ProjectName,
			f.ProjectPath,
			f.ProjectStatus,
			f.ProjectWebURL,
			"@" + f.Username + " (" + f.Name + ")",
			f.AccessRoleName,
			f.AccountType,
			string(f.Severity),
			f.ViolationType,
			f.Details,
			f.Remediation,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	for _, f := range report.BotAccessFindings {
		row := []string{
			"bots_and_service_accounts",
			strconv.Itoa(f.ProjectID),
			f.ProjectName,
			f.ProjectPath,
			f.ProjectStatus,
			f.ProjectWebURL,
			"@" + f.Username + " (" + f.Name + ")",
			f.AccessRoleName,
			f.AccountType,
			string(f.Severity),
			f.ViolationType,
			f.Details,
			f.Remediation,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	for _, f := range report.ProtectedBranchFindings {
		row := []string{
			"protected_branches",
			strconv.Itoa(f.ProjectID),
			f.ProjectName,
			f.ProjectPath,
			f.ProjectStatus,
			f.ProjectWebURL,
			f.BranchName,
			f.PushAccessLevelsSummary,
			"N/A",
			string(f.Severity),
			strings.Join(f.Violations, "; "),
			f.Details,
			f.Remediation,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	for _, f := range report.ProtectedEnvFindings {
		row := []string{
			"protected_environments",
			strconv.Itoa(f.ProjectID),
			f.ProjectName,
			f.ProjectPath,
			f.ProjectStatus,
			f.ProjectWebURL,
			f.EnvironmentName,
			f.DeployAccessLevelsSummary,
			"N/A",
			string(f.Severity),
			strings.Join(f.Violations, "; "),
			f.Details,
			f.Remediation,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	return nil
}

func exportMarkdown(report *audit.AuditReport, out io.Writer) error {
	var sb strings.Builder
	sb.WriteString("# GitLab Fleet Compliance & Security Audit Report\n\n")
	sb.WriteString(fmt.Sprintf("**Generated At**: %s  \n", report.GeneratedAt.UTC().Format(time.RFC1123)))
	sb.WriteString(fmt.Sprintf("**Scan Duration**: %s  \n", report.DurationString))
	sb.WriteString(fmt.Sprintf("**Active Modules**: `%s`  \n\n", strings.Join(report.ActiveModules, "`, `")))

	sb.WriteString("## Executive Summary\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|---|---:|\n")
	sb.WriteString(fmt.Sprintf("| Total Repositories Scanned | %d |\n", report.Summary.TotalProjectsScanned))
	sb.WriteString(fmt.Sprintf("| Active Repositories | %d |\n", report.Summary.ActiveProjectsCount))
	sb.WriteString(fmt.Sprintf("| Archived Repositories | %d |\n", report.Summary.ArchivedProjectsCount))
	sb.WriteString(fmt.Sprintf("| Fully Compliant Repositories | %d |\n", report.Summary.CompliantProjectsCount))
	sb.WriteString(fmt.Sprintf("| Non-Compliant Repositories | %d |\n", report.Summary.NonCompliantProjectsCount))
	sb.WriteString(fmt.Sprintf("| Total Audit Violations | **%d** |\n", report.Summary.TotalViolations))
	sb.WriteString(fmt.Sprintf("| Critical Severity Violations | **%d** |\n", report.Summary.CriticalSeverityCount))
	sb.WriteString(fmt.Sprintf("| High Severity Violations | **%d** |\n", report.Summary.HighSeverityCount))
	sb.WriteString(fmt.Sprintf("| Medium Severity Violations | %d |\n", report.Summary.MediumSeverityCount))
	sb.WriteString(fmt.Sprintf("| Low Severity Violations | %d |\n", report.Summary.LowSeverityCount))
	sb.WriteString(fmt.Sprintf("| Human User Violations | %d |\n", report.Summary.HumanUserViolations))
	sb.WriteString(fmt.Sprintf("| Bot / Service Account Violations | %d |\n", report.Summary.BotUserViolations))
	sb.WriteString(fmt.Sprintf("| Unique Fleet Users Discovered | %d |\n\n", len(report.UserDirectory)))

	// Human User Access Section
	if len(report.UserAccessFindings) > 0 {
		sb.WriteString("## 1. Human User Access & Expiration Audit (`user_access`)\n\n")
		sb.WriteString("| Project | State | Username | Role | Expiration | Status | Details | Remediation |\n")
		sb.WriteString("|---|---|---|---|---|:---:|---|---|\n")
		for _, f := range report.UserAccessFindings {
			exp := "None (Indefinite)"
			if f.HasExpiration {
				exp = f.ExpiresAt
			}
			badge := formatBadge(f.Severity)
			sb.WriteString(fmt.Sprintf("| [%s](%s) | %s | @%s | %s | %s | %s | %s | %s |\n",
				escapeMD(f.ProjectPath), f.ProjectWebURL, f.ProjectStatus, f.Username, f.AccessRoleName, exp, badge, escapeMD(f.Details), escapeMD(f.Remediation)))
		}
		sb.WriteString("\n")
	}

	// Bots & Service Accounts Section
	if len(report.BotAccessFindings) > 0 {
		sb.WriteString("## 2. Bots & Service Accounts Audit\n\n")
		sb.WriteString("| Project | State | Bot / Account | Role | Expiration | Status | Details | Remediation |\n")
		sb.WriteString("|---|---|---|---|---|:---:|---|---|\n")
		for _, f := range report.BotAccessFindings {
			exp := "None (Indefinite)"
			if f.HasExpiration {
				exp = f.ExpiresAt
			}
			badge := formatBadge(f.Severity)
			sb.WriteString(fmt.Sprintf("| [%s](%s) | %s | @%s (%s) | %s | %s | %s | %s | %s |\n",
				escapeMD(f.ProjectPath), f.ProjectWebURL, f.ProjectStatus, f.Username, escapeMD(f.Name), f.AccessRoleName, exp, badge, escapeMD(f.Details), escapeMD(f.Remediation)))
		}
		sb.WriteString("\n")
	}

	// Protected Branches Section
	if len(report.ProtectedBranchFindings) > 0 {
		sb.WriteString("## 3. Protected Branches Compliance Audit (`protected_branches`)\n\n")
		sb.WriteString("| Project | State | Branch | Force Push | Code Owner | Push Access | Status | Details | Remediation |\n")
		sb.WriteString("|---|---|---|:---:|:---:|---|:---:|---|---|\n")
		for _, f := range report.ProtectedBranchFindings {
			fp := "No"
			if f.AllowForcePush {
				fp = "**YES**"
			}
			co := "Yes"
			if !f.CodeOwnerApprovalRequired {
				co = "**NO**"
			}
			badge := formatBadge(f.Severity)
			sb.WriteString(fmt.Sprintf("| [%s](%s) | %s | `%s` | %s | %s | %s | %s | %s | %s |\n",
				escapeMD(f.ProjectPath), f.ProjectWebURL, f.ProjectStatus, f.BranchName, fp, co, escapeMD(f.PushAccessLevelsSummary), badge, escapeMD(f.Details), escapeMD(f.Remediation)))
		}
		sb.WriteString("\n")
	}

	// Protected Environments Section
	if len(report.ProtectedEnvFindings) > 0 {
		sb.WriteString("## 4. Protected Environments Deployment Audit (`protected_environments`)\n\n")
		sb.WriteString("| Project | State | Environment | Prod? | Approvals Required | Deploy Access | Status | Details | Remediation |\n")
		sb.WriteString("|---|---|---|:---:|:---:|---|:---:|---|---|\n")
		for _, f := range report.ProtectedEnvFindings {
			prod := "No"
			if f.IsProduction {
				prod = "**Yes**"
			}
			badge := formatBadge(f.Severity)
			sb.WriteString(fmt.Sprintf("| [%s](%s) | %s | `%s` | %s | %d | %s | %s | %s | %s |\n",
				escapeMD(f.ProjectPath), f.ProjectWebURL, f.ProjectStatus, f.EnvironmentName, prod, f.RequiredApprovalCount, escapeMD(f.DeployAccessLevelsSummary), badge, escapeMD(f.Details), escapeMD(f.Remediation)))
		}
		sb.WriteString("\n")
	}

	// User Directory Section
	if len(report.UserDirectory) > 0 {
		sb.WriteString("## 5. Fleet User Directory\n\n")
		sb.WriteString("| ID | Username | Full Name | Account Type | State | Projects Access |\n")
		sb.WriteString("|---|---|---|---|---|---:|\n")
		for _, u := range report.UserDirectory {
			sb.WriteString(fmt.Sprintf("| %d | [@%s](%s) | %s | %s | %s | %d |\n",
				u.ID, u.Username, u.WebURL, escapeMD(u.Name), u.AccountType, u.State, u.ProjectsCount))
		}
		sb.WriteString("\n")
	}

	_, err := io.WriteString(out, sb.String())
	return err
}

func exportHTML(report *audit.AuditReport, out io.Writer) error {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>GitLab Fleet Compliance & Security Audit</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; margin: 30px; color: #24292e; background-color: #f6f8fa; }
  .container { max-width: 1400px; margin: 0 auto; background: #fff; padding: 30px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.12); }
  h1 { color: #1f497d; margin-top: 0; }
  h2 { color: #2c3e50; border-bottom: 2px solid #eaecef; padding-bottom: 8px; margin-top: 32px; }
  .meta { color: #586069; font-size: 14px; margin-bottom: 20px; }
  .badge { display: inline-block; padding: 3px 8px; font-size: 11px; font-weight: 700; border-radius: 12px; text-transform: uppercase; }
  .badge-critical { background-color: #fadbd8; color: #78281f; border: 1px solid #f5b7b1; }
  .badge-high { background-color: #fadbd8; color: #78281f; border: 1px solid #f5b7b1; }
  .badge-medium { background-color: #fcf3cf; color: #7d6608; border: 1px solid #f9e79f; }
  .badge-low { background-color: #fdebd0; color: #6e2c00; border: 1px solid #f5cba7; }
  .badge-pass { background-color: #d4efdf; color: #145a32; border: 1px solid #a9dfbf; }
  table { width: 100%; border-collapse: collapse; margin-top: 14px; margin-bottom: 24px; font-size: 13px; }
  th { background-color: #1f497d; color: #ffffff; text-align: left; padding: 10px; border: 1px solid #d1d5da; }
  td { padding: 8px 10px; border: 1px solid #e1e4e8; vertical-align: top; }
  tr:nth-child(even) { background-color: #fcfcfc; }
  a { color: #0366d6; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .stat-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 16px; margin: 20px 0; }
  .stat-card { background: #f6f8fa; border: 1px solid #e1e4e8; border-radius: 6px; padding: 16px; text-align: center; }
  .stat-val { font-size: 24px; font-weight: 700; color: #1f497d; margin-top: 6px; }
  .stat-val-danger { color: #cb2431; }
</style>
</head>
<body>
<div class="container">
<h1>GitLab Fleet Compliance & Security Audit</h1>
<div class="meta">Generated: ` + report.GeneratedAt.UTC().Format(time.RFC1123) + ` | Scan Duration: ` + report.DurationString + `</div>

<div class="stat-grid">
  <div class="stat-card"><div>Repositories Scanned</div><div class="stat-val">` + strconv.Itoa(report.Summary.TotalProjectsScanned) + `</div></div>
  <div class="stat-card"><div>Active Repositories</div><div class="stat-val">` + strconv.Itoa(report.Summary.ActiveProjectsCount) + `</div></div>
  <div class="stat-card"><div>Archived Repositories</div><div class="stat-val">` + strconv.Itoa(report.Summary.ArchivedProjectsCount) + `</div></div>
  <div class="stat-card"><div>Fully Compliant</div><div class="stat-val">` + strconv.Itoa(report.Summary.CompliantProjectsCount) + `</div></div>
  <div class="stat-card"><div>Total Violations</div><div class="stat-val ` + dangerClass(report.Summary.TotalViolations) + `">` + strconv.Itoa(report.Summary.TotalViolations) + `</div></div>
  <div class="stat-card"><div>Critical Severity</div><div class="stat-val ` + dangerClass(report.Summary.CriticalSeverityCount) + `">` + strconv.Itoa(report.Summary.CriticalSeverityCount) + `</div></div>
  <div class="stat-card"><div>High Severity</div><div class="stat-val ` + dangerClass(report.Summary.HighSeverityCount) + `">` + strconv.Itoa(report.Summary.HighSeverityCount) + `</div></div>
  <div class="stat-card"><div>Human Violations</div><div class="stat-val ` + dangerClass(report.Summary.HumanUserViolations) + `">` + strconv.Itoa(report.Summary.HumanUserViolations) + `</div></div>
  <div class="stat-card"><div>Bot / Token Violations</div><div class="stat-val">` + strconv.Itoa(report.Summary.BotUserViolations) + `</div></div>
</div>
`)

	if len(report.UserAccessFindings) > 0 {
		sb.WriteString(`<h2>Human User Access & Expiration Audit</h2>
<table>
  <thead>
    <tr><th>Project</th><th>State</th><th>Username</th><th>Role</th><th>Expiration</th><th>Status</th><th>Details</th><th>Remediation</th></tr>
  </thead>
  <tbody>`)
		for _, f := range report.UserAccessFindings {
			exp := "Indefinite"
			if f.HasExpiration {
				exp = f.ExpiresAt
			}
			sb.WriteString(fmt.Sprintf(`<tr>
  <td><a href="%s" target="_blank">%s</a></td>
  <td>%s</td>
  <td>@%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
</tr>`, f.ProjectWebURL, f.ProjectPath, f.ProjectStatus, f.Username, f.AccessRoleName, exp, htmlBadge(f.Severity), f.Details, f.Remediation))
		}
		sb.WriteString(`</tbody></table>`)
	}

	if len(report.BotAccessFindings) > 0 {
		sb.WriteString(`<h2>Bots & Service Accounts Audit</h2>
<table>
  <thead>
    <tr><th>Project</th><th>State</th><th>Bot / Account</th><th>Role</th><th>Expiration</th><th>Status</th><th>Details</th><th>Remediation</th></tr>
  </thead>
  <tbody>`)
		for _, f := range report.BotAccessFindings {
			exp := "Indefinite"
			if f.HasExpiration {
				exp = f.ExpiresAt
			}
			sb.WriteString(fmt.Sprintf(`<tr>
  <td><a href="%s" target="_blank">%s</a></td>
  <td>%s</td>
  <td>@%s (%s)</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
</tr>`, f.ProjectWebURL, f.ProjectPath, f.ProjectStatus, f.Username, f.Name, f.AccessRoleName, exp, htmlBadge(f.Severity), f.Details, f.Remediation))
		}
		sb.WriteString(`</tbody></table>`)
	}

	if len(report.ProtectedBranchFindings) > 0 {
		sb.WriteString(`<h2>Protected Branches Compliance Audit</h2>
<table>
  <thead>
    <tr><th>Project</th><th>State</th><th>Branch</th><th>Force Push</th><th>Code Owner</th><th>Push Access</th><th>Status</th><th>Details</th><th>Remediation</th></tr>
  </thead>
  <tbody>`)
		for _, f := range report.ProtectedBranchFindings {
			fp := "No"
			if f.AllowForcePush {
				fp = "<b>YES</b>"
			}
			co := "Yes"
			if !f.CodeOwnerApprovalRequired {
				co = "<b>NO</b>"
			}
			sb.WriteString(fmt.Sprintf(`<tr>
  <td><a href="%s" target="_blank">%s</a></td>
  <td>%s</td>
  <td><code>%s</code></td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
</tr>`, f.ProjectWebURL, f.ProjectPath, f.ProjectStatus, f.BranchName, fp, co, f.PushAccessLevelsSummary, htmlBadge(f.Severity), f.Details, f.Remediation))
		}
		sb.WriteString(`</tbody></table>`)
	}

	if len(report.ProtectedEnvFindings) > 0 {
		sb.WriteString(`<h2>Protected Environments Deployment Audit</h2>
<table>
  <thead>
    <tr><th>Project</th><th>State</th><th>Environment</th><th>Production?</th><th>Approvals Required</th><th>Deploy Access</th><th>Status</th><th>Details</th><th>Remediation</th></tr>
  </thead>
  <tbody>`)
		for _, f := range report.ProtectedEnvFindings {
			prod := "No"
			if f.IsProduction {
				prod = "<b>Yes</b>"
			}
			sb.WriteString(fmt.Sprintf(`<tr>
  <td><a href="%s" target="_blank">%s</a></td>
  <td>%s</td>
  <td><code>%s</code></td>
  <td>%s</td>
  <td>%d</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
  <td>%s</td>
</tr>`, f.ProjectWebURL, f.ProjectPath, f.ProjectStatus, f.EnvironmentName, prod, f.RequiredApprovalCount, f.DeployAccessLevelsSummary, htmlBadge(f.Severity), f.Details, f.Remediation))
		}
		sb.WriteString(`</tbody></table>`)
	}

	sb.WriteString(`</div></body></html>`)
	_, err := io.WriteString(out, sb.String())
	return err
}

func exportTable(report *audit.AuditReport, out io.Writer) error {
	var sb strings.Builder
	sb.WriteString("\n================================================================================\n")
	sb.WriteString("                  GITLAB FLEET COMPLIANCE & SECURITY AUDIT\n")
	sb.WriteString("================================================================================\n")
	sb.WriteString(fmt.Sprintf("Scan Completed : %s (Duration: %s)\n", report.GeneratedAt.UTC().Format(time.RFC1123), report.DurationString))
	sb.WriteString(fmt.Sprintf("Projects       : Scanned: %d (Active: %d, Archived: %d) | Compliant: %d | Non-Compliant: %d\n",
		report.Summary.TotalProjectsScanned, report.Summary.ActiveProjectsCount, report.Summary.ArchivedProjectsCount, report.Summary.CompliantProjectsCount, report.Summary.NonCompliantProjectsCount))
	sb.WriteString(fmt.Sprintf("Violations     : Total: %d (Critical: %d, High: %d, Medium: %d, Low: %d)\n",
		report.Summary.TotalViolations, report.Summary.CriticalSeverityCount, report.Summary.HighSeverityCount, report.Summary.MediumSeverityCount, report.Summary.LowSeverityCount))
	sb.WriteString(fmt.Sprintf("Account Types  : Human Violations: %d | Bot/Service Account Violations: %d\n",
		report.Summary.HumanUserViolations, report.Summary.BotUserViolations))
	sb.WriteString("================================================================================\n\n")

	if len(report.UserAccessFindings) > 0 {
		sb.WriteString("[HUMAN USER ACCESS & EXPIRATION FINDINGS]\n")
		sb.WriteString(fmt.Sprintf("%-28s %-16s %-16s %-12s %-10s %s\n", "PROJECT", "USER", "ROLE", "EXPIRATION", "STATUS", "DETAILS"))
		sb.WriteString(strings.Repeat("-", 105) + "\n")
		for _, f := range report.UserAccessFindings {
			exp := "Indefinite"
			if f.HasExpiration {
				exp = f.ExpiresAt
			}
			sb.WriteString(fmt.Sprintf("%-28s %-16s %-16s %-12s %-10s %s\n",
				truncate(f.ProjectPath, 27),
				truncate("@"+f.Username, 15),
				truncate(f.AccessRoleName, 15),
				truncate(exp, 11),
				f.Severity,
				truncate(f.Details, 40),
			))
		}
		sb.WriteString("\n")
	}

	if len(report.BotAccessFindings) > 0 {
		sb.WriteString("[BOTS & SERVICE ACCOUNTS ACCESS FINDINGS]\n")
		sb.WriteString(fmt.Sprintf("%-28s %-16s %-16s %-12s %-10s %s\n", "PROJECT", "BOT / ACCOUNT", "ROLE", "EXPIRATION", "STATUS", "DETAILS"))
		sb.WriteString(strings.Repeat("-", 105) + "\n")
		for _, f := range report.BotAccessFindings {
			exp := "Indefinite"
			if f.HasExpiration {
				exp = f.ExpiresAt
			}
			sb.WriteString(fmt.Sprintf("%-28s %-16s %-16s %-12s %-10s %s\n",
				truncate(f.ProjectPath, 27),
				truncate("@"+f.Username, 15),
				truncate(f.AccessRoleName, 15),
				truncate(exp, 11),
				f.Severity,
				truncate(f.Details, 40),
			))
		}
		sb.WriteString("\n")
	}

	if len(report.ProtectedBranchFindings) > 0 {
		sb.WriteString("[PROTECTED BRANCHES FINDINGS]\n")
		sb.WriteString(fmt.Sprintf("%-28s %-16s %-12s %-12s %-10s %s\n", "PROJECT", "BRANCH", "FORCE PUSH", "CODE OWNER", "STATUS", "DETAILS"))
		sb.WriteString(strings.Repeat("-", 105) + "\n")
		for _, f := range report.ProtectedBranchFindings {
			fp := "No"
			if f.AllowForcePush {
				fp = "YES"
			}
			co := "Yes"
			if !f.CodeOwnerApprovalRequired {
				co = "NO"
			}
			sb.WriteString(fmt.Sprintf("%-28s %-16s %-12s %-12s %-10s %s\n",
				truncate(f.ProjectPath, 27),
				truncate(f.BranchName, 15),
				fp,
				co,
				f.Severity,
				truncate(f.Details, 40),
			))
		}
		sb.WriteString("\n")
	}

	if len(report.ProtectedEnvFindings) > 0 {
		sb.WriteString("[PROTECTED ENVIRONMENTS FINDINGS]\n")
		sb.WriteString(fmt.Sprintf("%-28s %-16s %-12s %-12s %-10s %s\n", "PROJECT", "ENVIRONMENT", "PROD?", "APPROVALS", "STATUS", "DETAILS"))
		sb.WriteString(strings.Repeat("-", 105) + "\n")
		for _, f := range report.ProtectedEnvFindings {
			prod := "No"
			if f.IsProduction {
				prod = "Yes"
			}
			sb.WriteString(fmt.Sprintf("%-28s %-16s %-12s %-12d %-10s %s\n",
				truncate(f.ProjectPath, 27),
				truncate(f.EnvironmentName, 15),
				prod,
				f.RequiredApprovalCount,
				f.Severity,
				truncate(f.Details, 40),
			))
		}
		sb.WriteString("\n")
	}

	_, err := io.WriteString(out, sb.String())
	return err
}

func formatBadge(s audit.Severity) string {
	switch s {
	case audit.SeverityCritical:
		return "🔴 `CRITICAL`"
	case audit.SeverityHigh:
		return "🟠 `HIGH`"
	case audit.SeverityMedium:
		return "🟡 `MEDIUM`"
	case audit.SeverityLow:
		return "🔵 `LOW`"
	case audit.SeverityPass:
		return "🟢 `PASS`"
	default:
		return fmt.Sprintf("`%s`", s)
	}
}

func htmlBadge(s audit.Severity) string {
	switch s {
	case audit.SeverityCritical:
		return `<span class="badge badge-critical">Critical</span>`
	case audit.SeverityHigh:
		return `<span class="badge badge-high">High</span>`
	case audit.SeverityMedium:
		return `<span class="badge badge-medium">Medium</span>`
	case audit.SeverityLow:
		return `<span class="badge badge-low">Low</span>`
	case audit.SeverityPass:
		return `<span class="badge badge-pass">Pass</span>`
	default:
		return string(s)
	}
}

func dangerClass(count int) string {
	if count > 0 {
		return "stat-val-danger"
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func escapeMD(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
