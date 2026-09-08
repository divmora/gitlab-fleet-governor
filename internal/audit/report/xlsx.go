package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/xuri/excelize/v2"
)

// XLSXReportGenerator renders formatted multi-sheet Excel workbooks from AuditReport.
type XLSXReportGenerator struct {
	file *excelize.File

	headerStyle    int
	subHeaderStyle int
	sectionStyle   int
	metricKeyStyle int
	metricValStyle int

	dataCellStyle   int
	centerCellStyle int
	hyperlinkStyle  int
	passStatusStyle int
	highStatusStyle int
	medStatusStyle  int
	warnStatusStyle int
}

// GenerateXLSX creates an Excel workbook representing the audit report and writes it to w.
func GenerateXLSX(report *audit.AuditReport, w io.Writer) error {
	gen := &XLSXReportGenerator{
		file: excelize.NewFile(),
	}
	defer func() {
		_ = gen.file.Close()
	}()

	if err := gen.initStyles(); err != nil {
		return fmt.Errorf("failed to initialize excel styles: %w", err)
	}

	// 1. Executive Summary Sheet
	if err := gen.buildExecutiveSummarySheet(report); err != nil {
		return fmt.Errorf("failed to build summary sheet: %w", err)
	}

	// 2. User Access Sheet
	if err := gen.buildUserAccessSheet(report); err != nil {
		return fmt.Errorf("failed to build user access sheet: %w", err)
	}

	// 3. Protected Branch Access Sheet
	if err := gen.buildProtectedBranchesSheet(report); err != nil {
		return fmt.Errorf("failed to build protected branches sheet: %w", err)
	}

	// 4. Protected Environments Access Sheet
	if err := gen.buildProtectedEnvironmentsSheet(report); err != nil {
		return fmt.Errorf("failed to build protected environments sheet: %w", err)
	}

	// Remove default "Sheet1" created by excelize
	_ = gen.file.DeleteSheet("Sheet1")

	// Set active sheet to Executive Summary
	idx, err := gen.file.GetSheetIndex("Executive Summary")
	if err == nil && idx >= 0 {
		gen.file.SetActiveSheet(idx)
	}

	if _, err := gen.file.WriteTo(w); err != nil {
		return fmt.Errorf("failed to write excel workbook to output stream: %w", err)
	}

	return nil
}

func (g *XLSXReportGenerator) initStyles() error {
	thinBorder := []excelize.Border{
		{Type: "left", Color: "D3D3D3", Style: 1},
		{Type: "right", Color: "D3D3D3", Style: 1},
		{Type: "top", Color: "D3D3D3", Style: 1},
		{Type: "bottom", Color: "D3D3D3", Style: 1},
	}

	// Header Style (Navy Blue #1F497D with white bold font)
	var err error
	g.headerStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11, Family: "Segoe UI"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#1F497D"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Sub-Header Style
	g.subHeaderStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "1F497D", Size: 11, Family: "Segoe UI"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#DCE6F1"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center", WrapText: true},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Section Title Style
	g.sectionStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "1F497D", Size: 14, Family: "Segoe UI"},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
	})
	if err != nil {
		return err
	}

	// Metric Key Style
	g.metricKeyStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "333333", Size: 10, Family: "Segoe UI"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#F2F4F4"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center"},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Metric Value Style
	g.metricValStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "111111", Size: 10, Family: "Segoe UI"},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Data Cell Style (Regular left-aligned wrapped text)
	g.dataCellStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Color: "222222", Size: 10, Family: "Segoe UI"},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center", WrapText: true},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Center-aligned Data Cell Style
	g.centerCellStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Color: "222222", Size: 10, Family: "Segoe UI"},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Hyperlink Cell Style (Blue underlined)
	g.hyperlinkStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Color: "1265BE", Size: 10, Underline: "single", Family: "Segoe UI"},
		Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center", WrapText: true},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Semantic Pass Status (Green Fill #D4EFDF with Dark Green Text #145A32)
	g.passStatusStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "145A32", Size: 10, Family: "Segoe UI"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#D4EFDF"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Semantic Critical / High Status (Red Fill #FADBD8 with Dark Red Text #78281F)
	g.highStatusStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "78281F", Size: 10, Family: "Segoe UI"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#FADBD8"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Semantic Medium Status (Yellow Fill #FCF3CF with Amber Text #7D6608)
	g.medStatusStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "7D6608", Size: 10, Family: "Segoe UI"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#FCF3CF"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	// Semantic Low / Warning Status
	g.warnStatusStyle, err = g.file.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "6E2C00", Size: 10, Family: "Segoe UI"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#FDEBD0"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border:    thinBorder,
	})
	if err != nil {
		return err
	}

	return nil
}

func (g *XLSXReportGenerator) buildExecutiveSummarySheet(report *audit.AuditReport) error {
	sheet := "Executive Summary"
	_, err := g.file.NewSheet(sheet)
	if err != nil {
		return err
	}

	// Title Block
	_ = g.file.SetCellValue(sheet, "B2", "GitLab Fleet Compliance & Security Audit")
	_ = g.file.SetCellStyle(sheet, "B2", "B2", g.sectionStyle)

	_ = g.file.SetCellValue(sheet, "B3", fmt.Sprintf("Report Generated: %s | Scan Duration: %s",
		report.GeneratedAt.UTC().Format(time.RFC1123), report.DurationString))

	// Overview KPIs Table
	startRow := 5
	_ = g.file.SetCellValue(sheet, fmt.Sprintf("B%d", startRow), "Fleet Governance Summary")
	_ = g.file.SetCellValue(sheet, fmt.Sprintf("C%d", startRow), "Metric Value")
	_ = g.file.SetCellStyle(sheet, fmt.Sprintf("B%d", startRow), fmt.Sprintf("C%d", startRow), g.headerStyle)

	metrics := []struct {
		Label string
		Value any
	}{
		{"Total Target Repositories Scanned", report.Summary.TotalProjectsScanned},
		{"Fully Compliant Repositories", report.Summary.CompliantProjectsCount},
		{"Non-Compliant Repositories (Drift/Risk Detected)", report.Summary.NonCompliantProjectsCount},
		{"Total Audit Violations Detected", report.Summary.TotalViolations},
		{"Critical Severity Violations", report.Summary.CriticalSeverityCount},
		{"High Severity Violations", report.Summary.HighSeverityCount},
		{"Medium Severity Violations", report.Summary.MediumSeverityCount},
		{"Low Severity Violations", report.Summary.LowSeverityCount},
	}

	for i, m := range metrics {
		row := startRow + 1 + i
		_ = g.file.SetCellValue(sheet, fmt.Sprintf("B%d", row), m.Label)
		_ = g.file.SetCellValue(sheet, fmt.Sprintf("C%d", row), m.Value)
		_ = g.file.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), g.metricKeyStyle)

		valStyle := g.metricValStyle
		if m.Label == "Critical Severity Violations" && report.Summary.CriticalSeverityCount > 0 {
			valStyle = g.highStatusStyle
		} else if m.Label == "High Severity Violations" && report.Summary.HighSeverityCount > 0 {
			valStyle = g.highStatusStyle
		} else if m.Label == "Fully Compliant Repositories" && report.Summary.CompliantProjectsCount > 0 {
			valStyle = g.passStatusStyle
		}
		_ = g.file.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), valStyle)
	}

	// Module Breakdown Table
	modRow := startRow + len(metrics) + 2
	_ = g.file.SetCellValue(sheet, fmt.Sprintf("B%d", modRow), "Audit Module")
	_ = g.file.SetCellValue(sheet, fmt.Sprintf("C%d", modRow), "Violations Found")
	_ = g.file.SetCellStyle(sheet, fmt.Sprintf("B%d", modRow), fmt.Sprintf("C%d", modRow), g.headerStyle)

	modMetrics := []struct {
		Module string
		Count  int
	}{
		{"User Access & Expiration Auditor (user_access)", report.Summary.UserAccessViolations},
		{"Protected Branches Compliance Auditor (protected_branches)", report.Summary.ProtectedBranchViolations},
		{"Protected Environments Deployment Auditor (protected_environments)", report.Summary.ProtectedEnvViolations},
	}

	for i, mm := range modMetrics {
		row := modRow + 1 + i
		_ = g.file.SetCellValue(sheet, fmt.Sprintf("B%d", row), mm.Module)
		_ = g.file.SetCellValue(sheet, fmt.Sprintf("C%d", row), mm.Count)
		_ = g.file.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), g.metricKeyStyle)
		valStyle := g.passStatusStyle
		if mm.Count > 0 {
			valStyle = g.highStatusStyle
		}
		_ = g.file.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), valStyle)
	}

	_ = g.file.SetColWidth(sheet, "A", "A", 4)
	_ = g.file.SetColWidth(sheet, "B", "B", 65)
	_ = g.file.SetColWidth(sheet, "C", "C", 25)

	return nil
}

func (g *XLSXReportGenerator) buildUserAccessSheet(report *audit.AuditReport) error {
	sheet := "User Access"
	_, err := g.file.NewSheet(sheet)
	if err != nil {
		return err
	}

	headers := []string{
		"Project ID",
		"Project Name",
		"Project URL",
		"Username",
		"Member Name",
		"Email",
		"Access Role",
		"Access Level",
		"Direct?",
		"Expiration Date",
		"Status",
		"Violation Type",
		"Details",
		"Remediation",
	}

	for colIdx, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		_ = g.file.SetCellValue(sheet, cell, h)
	}
	lastHeaderCell, _ := excelize.CoordinatesToCellName(len(headers), 1)
	_ = g.file.SetCellStyle(sheet, "A1", lastHeaderCell, g.headerStyle)

	// Populate findings
	type projectSpan struct {
		ProjectID int
		StartRow  int
		EndRow    int
	}
	var spans []projectSpan
	var currentSpan *projectSpan

	maxColLengths := make([]int, len(headers))
	for i, h := range headers {
		maxColLengths[i] = len(h)
	}

	row := 2
	for _, f := range report.UserAccessFindings {
		if currentSpan == nil || currentSpan.ProjectID != f.ProjectID {
			if currentSpan != nil {
				spans = append(spans, *currentSpan)
			}
			currentSpan = &projectSpan{
				ProjectID: f.ProjectID,
				StartRow:  row,
				EndRow:    row,
			}
		} else {
			currentSpan.EndRow = row
		}

		directStr := "Inherited"
		if f.IsDirect {
			directStr = "Direct"
		}
		expStr := "Indefinite (None)"
		if f.HasExpiration {
			expStr = f.ExpiresAt
		}

		values := []any{
			f.ProjectID,
			f.ProjectName,
			f.ProjectWebURL,
			"@" + f.Username,
			f.Name,
			f.Email,
			f.AccessRoleName,
			f.AccessLevel,
			directStr,
			expStr,
			string(f.Severity),
			f.ViolationType,
			f.Details,
			f.Remediation,
		}

		for colIdx, val := range values {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, row)
			_ = g.file.SetCellValue(sheet, cell, val)

			strVal := fmt.Sprintf("%v", val)
			for _, line := range strings.Split(strVal, "\n") {
				if len(line) > maxColLengths[colIdx] {
					maxColLengths[colIdx] = len(line)
				}
			}

			// Styling per column
			switch colIdx {
			case 0, 7, 8: // ID, Level, Direct
				_ = g.file.SetCellStyle(sheet, cell, cell, g.centerCellStyle)
			case 2: // Hyperlink
				if f.ProjectWebURL != "" {
					_ = g.file.SetCellHyperLink(sheet, cell, f.ProjectWebURL, "External")
					_ = g.file.SetCellStyle(sheet, cell, cell, g.hyperlinkStyle)
				} else {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.dataCellStyle)
				}
			case 10: // Status
				_ = g.file.SetCellStyle(sheet, cell, cell, g.severityStyle(f.Severity))
			default:
				_ = g.file.SetCellStyle(sheet, cell, cell, g.dataCellStyle)
			}
		}

		row++
	}

	if currentSpan != nil {
		spans = append(spans, *currentSpan)
	}

	// Automatic Merging of contiguous duplicate project metadata rows (Project ID, Name, URL)
	for _, span := range spans {
		if span.StartRow < span.EndRow {
			for col := 1; col <= 3; col++ {
				topCell, _ := excelize.CoordinatesToCellName(col, span.StartRow)
				bottomCell, _ := excelize.CoordinatesToCellName(col, span.EndRow)
				_ = g.file.MergeCell(sheet, topCell, bottomCell)
			}
		}
	}

	g.applyColumnWidths(sheet, maxColLengths)
	return nil
}

func (g *XLSXReportGenerator) buildProtectedBranchesSheet(report *audit.AuditReport) error {
	sheet := "Protected Branch Access"
	_, err := g.file.NewSheet(sheet)
	if err != nil {
		return err
	}

	headers := []string{
		"Project ID",
		"Project Name",
		"Project URL",
		"Branch Name",
		"Default?",
		"Protected?",
		"Force Push Allowed?",
		"Code Owner Approval Required?",
		"Push Access Summary",
		"Direct User Push Grants",
		"Merge Access Summary",
		"Status",
		"Violations / Details",
		"Remediation",
	}

	for colIdx, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		_ = g.file.SetCellValue(sheet, cell, h)
	}
	lastHeaderCell, _ := excelize.CoordinatesToCellName(len(headers), 1)
	_ = g.file.SetCellStyle(sheet, "A1", lastHeaderCell, g.headerStyle)

	type projectSpan struct {
		ProjectID int
		StartRow  int
		EndRow    int
	}
	var spans []projectSpan
	var currentSpan *projectSpan

	maxColLengths := make([]int, len(headers))
	for i, h := range headers {
		maxColLengths[i] = len(h)
	}

	row := 2
	for _, f := range report.ProtectedBranchFindings {
		if currentSpan == nil || currentSpan.ProjectID != f.ProjectID {
			if currentSpan != nil {
				spans = append(spans, *currentSpan)
			}
			currentSpan = &projectSpan{
				ProjectID: f.ProjectID,
				StartRow:  row,
				EndRow:    row,
			}
		} else {
			currentSpan.EndRow = row
		}

		directUsersStr := "None"
		if len(f.DirectUserPushGrants) > 0 {
			directUsersStr = strings.Join(f.DirectUserPushGrants, ", ")
		}

		forcePushStr := "No"
		if f.AllowForcePush {
			forcePushStr = "YES (Violation)"
		}

		codeOwnerStr := "YES (Enforced)"
		if !f.CodeOwnerApprovalRequired {
			codeOwnerStr = "NO (Missing)"
		}

		values := []any{
			f.ProjectID,
			f.ProjectName,
			f.ProjectWebURL,
			f.BranchName,
			boolToYesNo(f.IsDefaultBranch),
			boolToYesNo(f.IsProtected),
			forcePushStr,
			codeOwnerStr,
			f.PushAccessLevelsSummary,
			directUsersStr,
			f.MergeAccessLevelsSummary,
			string(f.Severity),
			f.Details,
			f.Remediation,
		}

		for colIdx, val := range values {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, row)
			_ = g.file.SetCellValue(sheet, cell, val)

			strVal := fmt.Sprintf("%v", val)
			for _, line := range strings.Split(strVal, "\n") {
				if len(line) > maxColLengths[colIdx] {
					maxColLengths[colIdx] = len(line)
				}
			}

			switch colIdx {
			case 0, 4, 5:
				_ = g.file.SetCellStyle(sheet, cell, cell, g.centerCellStyle)
			case 2: // URL
				if f.ProjectWebURL != "" {
					_ = g.file.SetCellHyperLink(sheet, cell, f.ProjectWebURL, "External")
					_ = g.file.SetCellStyle(sheet, cell, cell, g.hyperlinkStyle)
				} else {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.dataCellStyle)
				}
			case 6: // Force push
				if f.AllowForcePush {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.highStatusStyle)
				} else {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.passStatusStyle)
				}
			case 7: // Code Owner
				if !f.CodeOwnerApprovalRequired {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.highStatusStyle)
				} else {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.passStatusStyle)
				}
			case 9: // Direct user push
				if len(f.DirectUserPushGrants) > 0 {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.highStatusStyle)
				} else {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.centerCellStyle)
				}
			case 11: // Status
				_ = g.file.SetCellStyle(sheet, cell, cell, g.severityStyle(f.Severity))
			default:
				_ = g.file.SetCellStyle(sheet, cell, cell, g.dataCellStyle)
			}
		}

		row++
	}

	if currentSpan != nil {
		spans = append(spans, *currentSpan)
	}

	// Contiguous row merging
	for _, span := range spans {
		if span.StartRow < span.EndRow {
			for col := 1; col <= 3; col++ {
				topCell, _ := excelize.CoordinatesToCellName(col, span.StartRow)
				bottomCell, _ := excelize.CoordinatesToCellName(col, span.EndRow)
				_ = g.file.MergeCell(sheet, topCell, bottomCell)
			}
		}
	}

	g.applyColumnWidths(sheet, maxColLengths)
	return nil
}

func (g *XLSXReportGenerator) buildProtectedEnvironmentsSheet(report *audit.AuditReport) error {
	sheet := "Protected Environments Access"
	_, err := g.file.NewSheet(sheet)
	if err != nil {
		return err
	}

	headers := []string{
		"Project ID",
		"Project Name",
		"Project URL",
		"Environment Name",
		"Production Tier?",
		"Required Approvals",
		"Deploy Access Summary",
		"Direct User Deploy Grants",
		"Direct Group Deploy Grants",
		"Status",
		"Violations / Details",
		"Remediation",
	}

	for colIdx, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		_ = g.file.SetCellValue(sheet, cell, h)
	}
	lastHeaderCell, _ := excelize.CoordinatesToCellName(len(headers), 1)
	_ = g.file.SetCellStyle(sheet, "A1", lastHeaderCell, g.headerStyle)

	type projectSpan struct {
		ProjectID int
		StartRow  int
		EndRow    int
	}
	var spans []projectSpan
	var currentSpan *projectSpan

	maxColLengths := make([]int, len(headers))
	for i, h := range headers {
		maxColLengths[i] = len(h)
	}

	row := 2
	for _, f := range report.ProtectedEnvFindings {
		if currentSpan == nil || currentSpan.ProjectID != f.ProjectID {
			if currentSpan != nil {
				spans = append(spans, *currentSpan)
			}
			currentSpan = &projectSpan{
				ProjectID: f.ProjectID,
				StartRow:  row,
				EndRow:    row,
			}
		} else {
			currentSpan.EndRow = row
		}

		directUsersStr := "None"
		if len(f.DirectUserDeployGrants) > 0 {
			directUsersStr = strings.Join(f.DirectUserDeployGrants, ", ")
		}
		directGroupsStr := "None"
		if len(f.DirectGroupDeployGrants) > 0 {
			directGroupsStr = strings.Join(f.DirectGroupDeployGrants, ", ")
		}

		values := []any{
			f.ProjectID,
			f.ProjectName,
			f.ProjectWebURL,
			f.EnvironmentName,
			boolToYesNo(f.IsProduction),
			f.RequiredApprovalCount,
			f.DeployAccessLevelsSummary,
			directUsersStr,
			directGroupsStr,
			string(f.Severity),
			f.Details,
			f.Remediation,
		}

		for colIdx, val := range values {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, row)
			_ = g.file.SetCellValue(sheet, cell, val)

			strVal := fmt.Sprintf("%v", val)
			for _, line := range strings.Split(strVal, "\n") {
				if len(line) > maxColLengths[colIdx] {
					maxColLengths[colIdx] = len(line)
				}
			}

			switch colIdx {
			case 0, 4, 5:
				_ = g.file.SetCellStyle(sheet, cell, cell, g.centerCellStyle)
			case 2: // URL
				if f.ProjectWebURL != "" {
					_ = g.file.SetCellHyperLink(sheet, cell, f.ProjectWebURL, "External")
					_ = g.file.SetCellStyle(sheet, cell, cell, g.hyperlinkStyle)
				} else {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.dataCellStyle)
				}
			case 7: // Direct user deploy
				if len(f.DirectUserDeployGrants) > 0 {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.highStatusStyle)
				} else {
					_ = g.file.SetCellStyle(sheet, cell, cell, g.centerCellStyle)
				}
			case 9: // Status
				_ = g.file.SetCellStyle(sheet, cell, cell, g.severityStyle(f.Severity))
			default:
				_ = g.file.SetCellStyle(sheet, cell, cell, g.dataCellStyle)
			}
		}

		row++
	}

	if currentSpan != nil {
		spans = append(spans, *currentSpan)
	}

	// Contiguous row merging
	for _, span := range spans {
		if span.StartRow < span.EndRow {
			for col := 1; col <= 3; col++ {
				topCell, _ := excelize.CoordinatesToCellName(col, span.StartRow)
				bottomCell, _ := excelize.CoordinatesToCellName(col, span.EndRow)
				_ = g.file.MergeCell(sheet, topCell, bottomCell)
			}
		}
	}

	g.applyColumnWidths(sheet, maxColLengths)
	return nil
}

func (g *XLSXReportGenerator) severityStyle(s audit.Severity) int {
	switch s {
	case audit.SeverityCritical, audit.SeverityHigh:
		return g.highStatusStyle
	case audit.SeverityMedium:
		return g.medStatusStyle
	case audit.SeverityLow, audit.SeverityInfo:
		return g.warnStatusStyle
	case audit.SeverityPass:
		return g.passStatusStyle
	default:
		return g.centerCellStyle
	}
}

func (g *XLSXReportGenerator) applyColumnWidths(sheet string, maxLens []int) {
	for i, length := range maxLens {
		colName, _ := excelize.CoordinatesToCellName(i+1, 1)
		colLetter := strings.TrimRight(colName, "1")

		width := float64(length + 3)
		if width < 12 {
			width = 12
		}
		if width > 60 {
			width = 60
		}

		_ = g.file.SetColWidth(sheet, colLetter, colLetter, width)
	}
}

func boolToYesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
