package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

// compliantAuditServer serves one fully compliant project, ID 201, so two
// servers report the same project ID on different instances.
func compliantAuditServer(t *testing.T) *mockserver.MockGitLabServer {
	t.Helper()
	server := mockserver.NewMockGitLabServer()
	t.Cleanup(server.Close)

	proj := &gitlab.Project{ID: 201, Name: "audit-project", Path: "audit-project", PathWithNamespace: "compliance/audit-project", DefaultBranch: "main"}
	server.State().AddProject(proj)
	exp := gitlab.ISOTime{}
	_ = exp.UnmarshalJSON([]byte(`"2027-01-01"`))
	server.State().AddProjectMember(proj.ID, &gitlab.ProjectMember{ID: 1, Username: "secops", AccessLevel: gitlab.DeveloperPermissions, ExpiresAt: &exp})
	server.State().ProtectBranch(proj.ID, &gitlab.ProtectedBranch{ID: 1, Name: "main", CodeOwnerApprovalRequired: true})
	server.State().ProtectEnvironment(proj.ID, &gitlab.ProtectedEnvironment{Name: "production", RequiredApprovalCount: 1})
	server.State().ProtectTag(proj.ID, &gitlab.ProtectedTag{Name: "v*", CreateAccessLevels: []*gitlab.TagAccessDescription{{AccessLevel: gitlab.MaintainerPermissions}}})
	return server
}

func twoInstanceAuditPolicy(t *testing.T, alphaURL, betaURL string) string {
	t.Helper()
	return writePolicy(t, fmt.Sprintf(`
settings:
  concurrency: 2
  gitlab:
    retry_base_delay_ms: 1
    retry_max_delay_ms: 5
targets:
  project_selector:
    namespaces_include: ["compliance"]
instances:
  - name: alpha
    base_url: "%s"
    token: mock-token
  - name: beta
    base_url: "%s"
    token: mock-token
`, alphaURL, betaURL))
}

func TestAudit_MultiInstance(t *testing.T) {
	license.SetVerificationPublicKey(testutil.GetTestPublicKey())
	defer license.ResetVerificationPublicKey()
	// The environment must not redirect either instance.
	t.Setenv("GITLAB_BASE_URL", "http://127.0.0.1:1/api/v4")
	t.Setenv("GITLAB_TOKEN", "env-token")

	ctx := context.Background()
	alpha, beta := compliantAuditServer(t), compliantAuditServer(t)
	policy := twoInstanceAuditPolicy(t, alpha.BaseURL(), beta.BaseURL())
	dir := t.TempDir()
	lic := "--license-key=" + testutil.CLITestToken

	jsonOut := filepath.Join(dir, "audit.json")
	_, _, err := executeCommand(ctx, "audit", "-c", policy, lic, "--format=json", "-o", jsonOut)
	require.NoError(t, err)
	raw, err := os.ReadFile(jsonOut)
	require.NoError(t, err)
	var rep audit.AuditReport
	require.NoError(t, json.Unmarshal(raw, &rep))

	require.Len(t, rep.Instances, 2)
	assert.Equal(t, "alpha", rep.Instances[0].Name)
	assert.Equal(t, alpha.BaseURL(), rep.Instances[0].BaseURL)
	assert.Equal(t, 1, rep.Instances[0].ProjectsScanned)
	assert.Equal(t, "beta", rep.Instances[1].Name)
	assert.Equal(t, 2, rep.Summary.TotalProjectsScanned, "same project ID on two instances counts twice")
	assert.Equal(t, 2, rep.LicenseAttestation.DiscoveredProjects, "licence sees the whole fleet")
	require.NotEmpty(t, rep.ProtectedBranchFindings)
	seen := map[string]bool{}
	for _, f := range rep.ProtectedBranchFindings {
		seen[f.Instance] = true
	}
	assert.Equal(t, map[string]bool{"alpha": true, "beta": true}, seen)

	csvOut := filepath.Join(dir, "audit.csv")
	_, _, err = executeCommand(ctx, "audit", "-c", policy, lic, "--format=csv", "-o", csvOut)
	require.NoError(t, err)
	f, err := os.Open(csvOut)
	require.NoError(t, err)
	rows, err := csv.NewReader(f).ReadAll()
	_ = f.Close()
	require.NoError(t, err)
	assert.Equal(t, "Instance", rows[0][0])
	assert.Contains(t, []string{"alpha", "beta"}, rows[1][0])

	mdOut := filepath.Join(dir, "audit.md")
	_, _, err = executeCommand(ctx, "audit", "-c", policy, lic, "--format=markdown", "-o", mdOut)
	require.NoError(t, err)
	md, err := os.ReadFile(mdOut)
	require.NoError(t, err)
	assert.Contains(t, string(md), "## GitLab Instances")
	assert.Contains(t, string(md), "alpha:compliance/audit-project")

	htmlOut := filepath.Join(dir, "audit.html")
	_, _, err = executeCommand(ctx, "audit", "-c", policy, lic, "--format=html", "-o", htmlOut)
	require.NoError(t, err)
	page, err := os.ReadFile(htmlOut)
	require.NoError(t, err)
	assert.Contains(t, string(page), "<h2>GitLab Instances</h2>")
	assert.Contains(t, string(page), "beta:compliance/audit-project")

	xlsxOut := filepath.Join(dir, "audit.xlsx")
	_, _, err = executeCommand(ctx, "audit", "-c", policy, lic, "-o", xlsxOut)
	require.NoError(t, err)
	wb, err := excelize.OpenFile(xlsxOut)
	require.NoError(t, err)
	defer func() { _ = wb.Close() }()
	assert.Contains(t, wb.GetSheetList(), "GitLab Instances")
	cell, err := wb.GetCellValue("GitLab Instances", "A3")
	require.NoError(t, err)
	assert.Equal(t, "beta", cell)
}

func TestAudit_MultiInstancePartialFailure(t *testing.T) {
	license.SetVerificationPublicKey(testutil.GetTestPublicKey())
	defer license.ResetVerificationPublicKey()

	alpha := compliantAuditServer(t)
	// beta rejects the token, a non-retryable failure.
	beta := compliantAuditServer(t)
	beta.Faults().AddRule(mockserver.FaultRule{StatusCode: 401, FailCount: 1000, Message: "401 Unauthorized"})
	downURL := beta.BaseURL()

	policy := twoInstanceAuditPolicy(t, alpha.BaseURL(), downURL)
	out := filepath.Join(t.TempDir(), "audit.json")
	_, _, err := executeCommand(context.Background(), "audit", "-c", policy, "--license-key="+testutil.CLITestToken, "--format=json", "-o", out)
	require.ErrorContains(t, err, `audit failed on 1 of 2 instances: instance "beta"`)

	raw, readErr := os.ReadFile(out)
	require.NoError(t, readErr, "the report is still written")
	var rep audit.AuditReport
	require.NoError(t, json.Unmarshal(raw, &rep))
	require.Len(t, rep.Instances, 2)
	assert.Empty(t, rep.Instances[0].Error)
	assert.NotEmpty(t, rep.Instances[1].Error)
	assert.Equal(t, 1, rep.Summary.TotalProjectsScanned)
}

func TestAudit_MultiInstanceNeedsEnterprise(t *testing.T) {
	alpha, beta := compliantAuditServer(t), compliantAuditServer(t)
	t.Setenv("DIVMORA_LICENSE_KEY", "")
	policy := twoInstanceAuditPolicy(t, alpha.BaseURL(), beta.BaseURL())
	out := filepath.Join(t.TempDir(), "a.json")

	// Like the other commercial audit features, dry-run (the default) only warns.
	_, _, err := executeCommand(context.Background(), "audit", "-c", policy, "--format=json", "-o", out)
	require.NoError(t, err)

	_, _, err = executeCommand(context.Background(), "audit", "-c", policy, "--dry-run=false", "--format=json", "-o", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), license.FeatureMultiInstance)
}

func TestExportAndMerge_InstanceSelection(t *testing.T) {
	ctx := context.Background()
	policy := twoInstanceAuditPolicy(t, "https://a.example/api/v4", "https://b.example/api/v4")
	single := writePolicy(t, "version: v1\nsettings:\n  gitlab:\n    token: t\n")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"export", "-c", policy, "--project-id", "1"}, "several GitLab instances (alpha, beta); choose one with --instance"},
		{[]string{"export", "-c", policy, "--project-id", "1", "--instance", "gamma"}, `--instance "gamma": no such instance (have alpha, beta)`},
		{[]string{"export", "-c", single, "--project-id", "1", "--instance", "alpha"}, "has no instances block"},
		{[]string{"export", "--project-id", "1", "--instance", "alpha"}, "--instance needs a policy with an instances block"},
		{[]string{"merge", "-c", policy, "-p", "1", "-s", "a", "-t", "b"}, "choose one with --instance"},
		{[]string{"merge", "-c", policy, "-p", "1", "-s", "a", "-t", "b", "--instance", "gamma"}, "no such instance"},
		{[]string{"merge", "-p", "1", "-s", "a", "-t", "b", "--instance", "alpha"}, "--instance needs a policy with an instances block"},
	} {
		_, _, err := executeCommand(ctx, tc.args...)
		require.Error(t, err, strings.Join(tc.args, " "))
		assert.Contains(t, err.Error(), tc.want, strings.Join(tc.args, " "))
	}
}

func TestExport_FromOneInstance(t *testing.T) {
	alpha := compliantAuditServer(t)
	policy := twoInstanceAuditPolicy(t, alpha.BaseURL(), "http://127.0.0.1:1/api/v4")
	t.Setenv("GITLAB_BASE_URL", "http://127.0.0.1:1/api/v4")

	out, _, err := executeCommand(context.Background(), "export", "-c", policy, "--instance", "alpha", "--project-id", "201", "--output", "-")
	require.NoError(t, err)
	assert.Contains(t, out, "policies:")
	assert.NotContains(t, out, "mock-token", "the export must not leak the instance token")
}
