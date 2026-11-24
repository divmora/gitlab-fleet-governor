package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
)

func multiInstanceResult() *engine.ExecutionResult {
	return &engine.ExecutionResult{
		Success: true,
		TargetResults: []*engine.TargetResult{
			{Instance: "saas", TargetID: 1, TargetPath: "team/app", ResourceType: governance.ResourceTypeProject, Success: true},
			{Instance: "onprem", TargetID: 1, TargetPath: "team/app", ResourceType: governance.ResourceTypeProject, Success: true},
		},
		Instances: []*engine.InstanceSummary{
			{Name: "saas", BaseURL: "https://gitlab.com/api/v4", Success: true, Metrics: &engine.SummaryMetricsSnapshot{TotalScanned: 1}},
			{Name: "onprem", BaseURL: "https://gitlab.corp.example/api/v4", Error: "boom"},
		},
		Metrics: &engine.SummaryMetricsSnapshot{},
	}
}

func render(t *testing.T, f report.Format, rd *report.ReportData) string {
	t.Helper()
	var buf bytes.Buffer
	r, err := report.NewReporter(f, &buf)
	require.NoError(t, err)
	require.NoError(t, r.Render(rd))
	return buf.String()
}

func TestReport_MultiInstance(t *testing.T) {
	rd := report.FromExecutionResult(multiInstanceResult())
	require.Len(t, rd.Instances, 2)
	assert.Equal(t, "SUCCESS", rd.Instances[0].Status)
	assert.Equal(t, 1, rd.Instances[0].Scanned)
	assert.Equal(t, "FAILED", rd.Instances[1].Status)
	assert.Equal(t, "saas:team/app", rd.Targets[0].DisplayPath())

	csvOut := render(t, report.FormatCSV, rd)
	lines := strings.Split(strings.TrimSpace(csvOut), "\n")
	assert.True(t, strings.HasPrefix(lines[0], "instance,resource_type,"), lines[0])
	assert.True(t, strings.HasPrefix(lines[1], "saas,project,1,team/app,"), lines[1])
	assert.True(t, strings.HasPrefix(lines[2], "onprem,project,1,team/app,"), lines[2])

	md := render(t, report.FormatMarkdown, rd)
	assert.Contains(t, md, "GitLab Instances")
	assert.Contains(t, md, "onprem:team/app")

	table := render(t, report.FormatTable, rd)
	assert.Contains(t, table, "GITLAB INSTANCES")
	assert.Contains(t, table, "error: boom")

	assert.Contains(t, render(t, report.FormatJSON, rd), `"instance": "saas"`)
}

func TestReport_SingleInstanceUnchanged(t *testing.T) {
	res := multiInstanceResult()
	res.Instances = nil
	for _, tr := range res.TargetResults {
		tr.Instance = ""
	}
	rd := report.FromExecutionResult(res)

	csvOut := render(t, report.FormatCSV, rd)
	assert.True(t, strings.HasPrefix(csvOut, "resource_type,"), "no instance column for single-instance runs")
	assert.NotContains(t, render(t, report.FormatMarkdown, rd), "GitLab Instances")
	assert.NotContains(t, render(t, report.FormatJSON, rd), `"instance"`)
}
