package report_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/audit/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func sampleAuditReport() *audit.AuditReport {
	now := time.Now()
	r := &audit.AuditReport{
		Title:          "Test Fleet Compliance Report",
		GeneratedAt:    now,
		Duration:       2 * time.Second,
		DurationString: "2s",
		ActiveModules:  audit.AllModuleNames(),
		UserAccessFindings: []audit.UserAccessFinding{
			{
				ProjectID:      101,
				ProjectName:    "payments-api",
				ProjectPath:    "fintech/payments-api",
				ProjectWebURL:  "https://gitlab.example.com/fintech/payments-api",
				UserID:         1,
				Username:       "alice",
				Name:           "Alice Admin",
				Email:          "alice@example.com",
				AccessLevel:    50,
				AccessRoleName: "Owner (50)",
				IsDirect:       true,
				Severity:       audit.SeverityPass,
				ViolationType:  "COMPLIANT",
				Details:        "Compliant owner access",
				Remediation:    "No action required",
			},
			{
				ProjectID:      101,
				ProjectName:    "payments-api",
				ProjectPath:    "fintech/payments-api",
				ProjectWebURL:  "https://gitlab.example.com/fintech/payments-api",
				UserID:         2,
				Username:       "bob",
				Name:           "Bob Maintainer",
				Email:          "bob@example.com",
				AccessLevel:    40,
				AccessRoleName: "Maintainer (40)",
				IsDirect:       true,
				Severity:       audit.SeverityCritical,
				ViolationType:  "INDEFINITE_MAINTAINER_ACCESS",
				Details:        "High-privilege Maintainer has indefinite access",
				Remediation:    "Enforce expiration date",
			},
			{
				ProjectID:      102,
				ProjectName:    "core-auth",
				ProjectPath:    "fintech/core-auth",
				ProjectWebURL:  "https://gitlab.example.com/fintech/core-auth",
				UserID:         3,
				Username:       "carol",
				Name:           "Carol Dev",
				Email:          "carol@example.com",
				AccessLevel:    30,
				AccessRoleName: "Developer (30)",
				IsDirect:       false,
				ExpiresAt:      "2026-12-31",
				HasExpiration:  true,
				Severity:       audit.SeverityPass,
				ViolationType:  "COMPLIANT",
				Details:        "Compliant inherited member",
				Remediation:    "No action required",
			},
		},
		ProtectedBranchFindings: []audit.ProtectedBranchFinding{
			{
				ProjectID:                 101,
				ProjectName:               "payments-api",
				ProjectPath:               "fintech/payments-api",
				ProjectWebURL:             "https://gitlab.example.com/fintech/payments-api",
				BranchName:                "main",
				IsDefaultBranch:           true,
				IsProtected:               true,
				AllowForcePush:            true,
				CodeOwnerApprovalRequired: false,
				PushAccessLevelsSummary:   "Developer (30)",
				DirectUserPushGrants:      []string{"User ID 42"},
				Severity:                  audit.SeverityCritical,
				Violations:                []string{"Force push enabled", "Missing Code Owner approval"},
				Details:                   "Force push enabled; Missing Code Owner approval",
				Remediation:               "Disable force push and require Code Owner approvals",
			},
		},
		ProtectedEnvFindings: []audit.ProtectedEnvironmentFinding{
			{
				ProjectID:                 101,
				ProjectName:               "payments-api",
				ProjectPath:               "fintech/payments-api",
				ProjectWebURL:             "https://gitlab.example.com/fintech/payments-api",
				EnvironmentName:           "production",
				IsProduction:              true,
				RequiredApprovalCount:     0,
				DeployAccessLevelsSummary: "Maintainer (40)",
				Severity:                  audit.SeverityCritical,
				Violations:                []string{"Zero approvals required on production"},
				Details:                   "Production environment requires zero deployment approvals",
				Remediation:               "Require at least 1 or 2 approvals for production deploys",
			},
		},
	}
	r.Summary.TotalProjectsScanned = 2
	r.ComputeSummary()
	return r
}

func TestGenerateXLSX(t *testing.T) {
	rep := sampleAuditReport()

	var buf bytes.Buffer
	err := report.GenerateXLSX(rep, &buf)
	require.NoError(t, err)
	require.Greater(t, buf.Len(), 1000)

	// Verify generated XLSX using excelize reader
	xlFile, err := excelize.OpenReader(&buf)
	require.NoError(t, err)
	defer func() { _ = xlFile.Close() }()

	sheetList := xlFile.GetSheetList()
	assert.Contains(t, sheetList, "Executive Summary")
	assert.Contains(t, sheetList, "User Access")
	assert.Contains(t, sheetList, "Protected Branch Access")
	assert.Contains(t, sheetList, "Protected Environments Access")

	// Verify Executive Summary Content
	titleVal, err := xlFile.GetCellValue("Executive Summary", "B2")
	require.NoError(t, err)
	assert.Contains(t, titleVal, "GitLab Fleet Compliance & Security Audit")

	// Verify User Access Sheet headers and rows
	h1, err := xlFile.GetCellValue("User Access", "A1")
	require.NoError(t, err)
	assert.Equal(t, "Project ID", h1)

	userVal, err := xlFile.GetCellValue("User Access", "D2")
	require.NoError(t, err)
	assert.Equal(t, "@alice", userVal)

	statusVal, err := xlFile.GetCellValue("User Access", "K3")
	require.NoError(t, err)
	assert.Equal(t, "CRITICAL", statusVal)

	// Verify row merging on Project ID (col A rows 2 and 3 for project 101)
	mergedCells, err := xlFile.GetMergeCells("User Access")
	require.NoError(t, err)
	hasMergedProject := false
	for _, m := range mergedCells {
		topCell := m.GetStartAxis()
		bottomCell := m.GetEndAxis()
		if topCell == "A2" && bottomCell == "A3" {
			hasMergedProject = true
			break
		}
	}
	assert.True(t, hasMergedProject, "Expected project metadata cells A2:A3 to be merged for project 101")

	// Verify Protected Branch Access Content
	branchVal, err := xlFile.GetCellValue("Protected Branch Access", "D2")
	require.NoError(t, err)
	assert.Equal(t, "main", branchVal)

	forcePushVal, err := xlFile.GetCellValue("Protected Branch Access", "G2")
	require.NoError(t, err)
	assert.Contains(t, forcePushVal, "YES")

	// Verify Protected Environments Access Content
	envVal, err := xlFile.GetCellValue("Protected Environments Access", "D2")
	require.NoError(t, err)
	assert.Equal(t, "production", envVal)
}
