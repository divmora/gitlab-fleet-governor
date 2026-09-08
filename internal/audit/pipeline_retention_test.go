package audit_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	auditreport "github.com/divmora/gitlab-fleet-governor/internal/audit/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestPipelineRetentionAuditor_ConfiguredRetention_WithStalePipelines(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Set 90 days retention (7776000 seconds)
	server.State().SetPipelineRetention(targetProj.ID, 7776000)

	// Add stale pipeline created 180 days ago (90 days overdue -> SeverityHigh)
	tOld := time.Now().Add(-180 * 24 * time.Hour)
	server.State().AddPipeline(targetProj.ID, &gogitlab.PipelineInfo{
		ID:        501,
		Ref:       "main",
		Status:    "success",
		CreatedAt: &tOld,
		UpdatedAt: &tOld,
	})

	aud := audit.NewPipelineRetentionAuditor()
	findings, err := aud.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 1)

	f := findings[0]
	assert.Equal(t, targetProj.ID, f.ProjectID)
	assert.True(t, f.HasRetentionConfigured)
	assert.Equal(t, 7776000, f.RetentionSeconds)
	assert.Equal(t, 90, f.RetentionDays)
	assert.Equal(t, 501, f.OldestPipelineID)
	assert.Equal(t, "Retention Cleanup Failure", f.ViolationType)
	assert.Equal(t, audit.SeverityHigh, f.Severity)
	assert.Contains(t, f.Details, "stale pipeline(s) older than 90d still exist")
	assert.Contains(t, f.Remediation, "GitLab background pruning worker is failing or queued")
}

func TestPipelineRetentionAuditor_ConfiguredRetention_Clean(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Set 90 days retention
	server.State().SetPipelineRetention(targetProj.ID, 7776000)

	// Add fresh pipeline created 2 days ago
	tFresh := time.Now().Add(-48 * time.Hour)
	server.State().AddPipeline(targetProj.ID, &gogitlab.PipelineInfo{
		ID:        502,
		Ref:       "main",
		Status:    "success",
		CreatedAt: &tFresh,
		UpdatedAt: &tFresh,
	})

	aud := audit.NewPipelineRetentionAuditor()
	findings, err := aud.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 1)

	f := findings[0]
	assert.Equal(t, targetProj.ID, f.ProjectID)
	assert.True(t, f.HasRetentionConfigured)
	assert.Equal(t, audit.SeverityPass, f.Severity)
	assert.Equal(t, "None", f.ViolationType)
	assert.Contains(t, f.Details, "all stale pipelines pruned successfully")
}

func TestPipelineRetentionAuditor_DisabledRetention_WithOldPipelines(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Retention disabled (0 seconds)
	server.State().SetPipelineRetention(targetProj.ID, 0)

	// Add pipeline created 120 days ago
	tOld := time.Now().Add(-120 * 24 * time.Hour)
	server.State().AddPipeline(targetProj.ID, &gogitlab.PipelineInfo{
		ID:        601,
		Ref:       "feat-branch",
		Status:    "failed",
		CreatedAt: &tOld,
		UpdatedAt: &tOld,
	})

	aud := audit.NewPipelineRetentionAuditor()
	findings, err := aud.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 1)

	f := findings[0]
	assert.Equal(t, targetProj.ID, f.ProjectID)
	assert.False(t, f.HasRetentionConfigured)
	assert.Equal(t, 0, f.RetentionSeconds)
	assert.Equal(t, 601, f.OldestPipelineID)
	assert.Equal(t, "Pipeline Retention Disabled", f.ViolationType)
	assert.Equal(t, audit.SeverityLow, f.Severity)
	assert.Contains(t, f.Details, "Pipeline deletion is disabled (0s); found 1 pipeline(s) older than 90 days")
}

func TestPipelineRetentionAuditor_DisabledRetention_Clean(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Retention disabled (0 seconds), only recent pipeline
	server.State().SetPipelineRetention(targetProj.ID, 0)
	tRecent := time.Now().Add(-24 * time.Hour)
	server.State().AddPipeline(targetProj.ID, &gogitlab.PipelineInfo{
		ID:        602,
		Ref:       "main",
		Status:    "success",
		CreatedAt: &tRecent,
		UpdatedAt: &tRecent,
	})

	aud := audit.NewPipelineRetentionAuditor()
	findings, err := aud.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 1)

	f := findings[0]
	assert.Equal(t, audit.SeverityPass, f.Severity)
	assert.Equal(t, "None", f.ViolationType)
}

func TestPipelineRetentionAuditor_ArchivedDeprioritization(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Mark project as archived
	targetProj.Archived = true
	if targetProj.Raw != nil {
		targetProj.Raw.Archived = true
	}

	// 90 days retention with stale pipeline created 200 days ago (normally SeverityHigh)
	server.State().SetPipelineRetention(targetProj.ID, 7776000)
	tOld := time.Now().Add(-200 * 24 * time.Hour)
	server.State().AddPipeline(targetProj.ID, &gogitlab.PipelineInfo{
		ID:        701,
		Ref:       "main",
		Status:    "success",
		CreatedAt: &tOld,
		UpdatedAt: &tOld,
	})

	aud := audit.NewPipelineRetentionAuditor()
	findings, err := aud.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 1)

	f := findings[0]
	assert.Equal(t, "Archived", f.ProjectStatus)
	// Base SeverityHigh should be deprioritized to SeverityLow for archived repos
	assert.Equal(t, audit.SeverityLow, f.Severity)
}

func TestAuditor_PipelineRetention_FullIntegrationAndExports(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Configure retention + stale pipeline
	server.State().SetPipelineRetention(targetProj.ID, 7776000)
	tOld := time.Now().Add(-150 * 24 * time.Hour)
	server.State().AddPipeline(targetProj.ID, &gogitlab.PipelineInfo{
		ID:        801,
		Ref:       "master",
		Status:    "success",
		CreatedAt: &tOld,
		UpdatedAt: &tOld,
	})

	auditor, err := audit.NewAuditor(client,
		audit.WithAuditorModules([]string{"pipeline_retention"}),
	)
	require.NoError(t, err)

	report, err := auditor.Execute(context.Background())
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, 1, report.Summary.TotalProjectsScanned)
	assert.Equal(t, 1, report.Summary.PipelineRetentionViolations)
	assert.Equal(t, 1, report.Summary.TotalViolations)
	assert.Len(t, report.PipelineRetentionFindings, 1)

	// 1. Test Excel generation with Pipeline Retention sheet
	var xlsxBuf bytes.Buffer
	err = auditreport.ExportAuditReport(report, auditreport.FormatXLSX, &xlsxBuf)
	require.NoError(t, err)
	assert.Greater(t, xlsxBuf.Len(), 1000)

	// 2. Test Markdown export
	var mdBuf bytes.Buffer
	err = auditreport.ExportAuditReport(report, auditreport.FormatMarkdown, &mdBuf)
	require.NoError(t, err)
	mdStr := mdBuf.String()
	assert.Contains(t, mdStr, "## 5. Pipeline Retention & Cleanup Audit (`pipeline_retention`)")
	assert.Contains(t, mdStr, "Pipeline Retention Violations")
	assert.Contains(t, mdStr, "Retention Cleanup Failure")

	// 3. Test HTML export
	var htmlBuf bytes.Buffer
	err = auditreport.ExportAuditReport(report, auditreport.FormatHTML, &htmlBuf)
	require.NoError(t, err)
	htmlStr := htmlBuf.String()
	assert.Contains(t, htmlStr, "Pipeline Retention & Cleanup Audit")
	assert.Contains(t, htmlStr, "Retention Violations")

	// 4. Test CSV export
	var csvBuf bytes.Buffer
	err = auditreport.ExportAuditReport(report, auditreport.FormatCSV, &csvBuf)
	require.NoError(t, err)
	csvStr := csvBuf.String()
	assert.Contains(t, csvStr, "Pipeline Retention")
	assert.Contains(t, csvStr, "Retention Cleanup Failure")

	// 5. Test Table export
	var tblBuf bytes.Buffer
	err = auditreport.ExportAuditReport(report, auditreport.FormatTable, &tblBuf)
	require.NoError(t, err)
	tblStr := tblBuf.String()
	assert.Contains(t, tblStr, "[PIPELINE RETENTION & CLEANUP FINDINGS]")
	assert.Contains(t, tblStr, "Retention & Cleanup Violations: 1")
}
