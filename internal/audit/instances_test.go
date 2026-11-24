package audit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
)

func instanceReport(name string, projects int, att int, sev audit.Severity) *audit.AuditReport {
	r := &audit.AuditReport{
		GovernorVersion:    "test",
		ActiveModules:      []string{"protected_branches"},
		AuthenticatedUser:  &audit.UserInfo{Username: name + "-bot", Instance: name},
		LicenseAttestation: &audit.LicenseAttestation{DiscoveredProjects: att},
		Summary:            audit.SummaryMetrics{TotalProjectsScanned: projects, ActiveProjectsCount: projects, AuditedBy: "@" + name + "-bot"},
		ProtectedBranchFindings: []audit.ProtectedBranchFinding{
			{Instance: name, ProjectID: 201, ProjectPath: "team/app", BranchName: "main", Severity: sev},
		},
		UserDirectory: []*audit.UserInfo{{ID: 1, Username: "alice", Instance: name}},
	}
	r.ComputeSummary()
	return r
}

func TestMergeReports(t *testing.T) {
	started := time.Now()
	merged := audit.MergeReports([]audit.InstanceResult{
		{Name: "alpha", BaseURL: "https://a.example/api/v4", Report: instanceReport("alpha", 1, 1, audit.SeverityHigh)},
		{Name: "beta", BaseURL: "https://b.example/api/v4", Report: instanceReport("beta", 2, 3, audit.SeverityHigh)},
		{Name: "gamma", BaseURL: "https://c.example/api/v4", Err: errors.New("401 Unauthorized")},
	}, started)

	require.True(t, merged.IsMultiInstance())
	require.Len(t, merged.Instances, 3)
	assert.Equal(t, "alpha-bot", merged.Instances[0].AuthenticatedUser.Username)
	assert.Equal(t, 1, merged.Instances[0].Violations)
	assert.Equal(t, "401 Unauthorized", merged.Instances[2].Error)

	assert.Equal(t, 3, merged.Summary.TotalProjectsScanned)
	assert.Equal(t, 2, merged.Summary.TotalViolations)
	assert.Equal(t, 2, merged.Summary.NonCompliantProjectsCount, "project 201 on alpha and on beta are different projects")
	assert.Equal(t, 1, merged.Summary.CompliantProjectsCount)
	assert.Equal(t, 3, merged.LicenseAttestation.DiscoveredProjects, "attestation from the last instance covers the fleet")
	assert.Equal(t, "alpha:@alpha-bot; beta:@beta-bot", merged.Summary.AuditedBy)
	assert.Equal(t, []string{"protected_branches"}, merged.ActiveModules)
	assert.Len(t, merged.UserDirectory, 2)
	assert.Equal(t, "alpha", merged.ProtectedBranchFindings[0].Instance)
	assert.Equal(t, "beta", merged.ProtectedBranchFindings[1].Instance)
}

func TestLabel(t *testing.T) {
	assert.Equal(t, "team/app", audit.Label("", "team/app"))
	assert.Equal(t, "saas:team/app", audit.Label("saas", "team/app"))
}
