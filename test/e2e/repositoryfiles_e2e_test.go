package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func TestE2E_RepositoryFiles_FullSyncLifecycle(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	tmpDir := t.TempDir()
	secTemplatePath := filepath.Join(tmpDir, "SECURITY.md.tmpl")
	err = os.WriteFile(secTemplatePath, []byte("# Enterprise Security Policy\nContact sec@corp.com"), 0644)
	require.NoError(t, err)

	policyYaml := `version: "v1"
settings:
  dry_run: false
  concurrency: 5
  gitlab:
    base_url: "` + srv.BaseURL() + `"
    token: "mock-token"
targets:
  project_selector:
    namespaces_include:
      - "platform"
policies:
  repository_files:
    - path: "CODEOWNERS"
      content: "* @platform/security"
      target_branch: "main"
      enforcement: "direct_commit"

    - path: "SECURITY.md"
      content_file: "` + secTemplatePath + `"
      target_branch: "main"
      enforcement: "merge_request"
      mr_title: "chore: sync SECURITY.md"
`

	policyPath := filepath.Join(tmpDir, "policy.yaml")
	err = os.WriteFile(policyPath, []byte(policyYaml), 0644)
	require.NoError(t, err)

	loader := config.NewLoader()
	rawCfg, _, err := loader.LoadRaw(context.Background(), policyPath)
	require.NoError(t, err)

	expanded, err := config.ExpandEnv(string(rawCfg))
	require.NoError(t, err)

	policy, err := config.Unmarshal([]byte(expanded))
	require.NoError(t, err)
	require.NoError(t, policy.Validate())

	// Phase 1: Dry-Run Simulation
	dryRunEngine, err := engine.NewGovernanceEngine(client, policy, engine.WithDryRun(true), engine.WithConcurrency(2))
	require.NoError(t, err)

	dryRunResult, err := dryRunEngine.Plan(context.Background())
	require.NoError(t, err)
	assert.True(t, dryRunResult.DryRun)
	assert.GreaterOrEqual(t, dryRunResult.Metrics.TotalChanged, 1)

	// Phase 2: Live Mutating Reconciliation
	liveEngine, err := engine.NewGovernanceEngine(client, policy, engine.WithDryRun(false), engine.WithConcurrency(2))
	require.NoError(t, err)

	liveResult, err := liveEngine.Apply(context.Background())
	require.NoError(t, err)
	if !liveResult.Success {
		for _, e := range liveResult.Errors {
			t.Logf("Live result error: %v", e)
		}
		for _, tr := range liveResult.TargetResults {
			if tr.Error != nil {
				t.Logf("Target %d error: %v", tr.TargetID, tr.Error)
			}
			for _, op := range tr.Operations {
				if op.Error != nil {
					t.Logf("Op %s error: %v", op.OperationName, op.Error)
				}
			}
		}
	}
	assert.True(t, liveResult.Success)

	// Verify direct commit file creation in mock server state
	codeownersBytes, found := srv.State().GetRawFile(101, "CODEOWNERS", "main")
	require.True(t, found)
	assert.Contains(t, string(codeownersBytes), "* @platform/security")

	// Verify MR creation in mock server state
	mrs := srv.State().ListMergeRequests(101, "governance/sync-security-md", "main", "opened")
	require.Len(t, mrs, 1)
	assert.Equal(t, "chore: sync SECURITY.md", mrs[0].Title)

	// Phase 3: Idempotency Verification Re-Run
	reRunResult, err := liveEngine.Apply(context.Background())
	require.NoError(t, err)
	assert.True(t, reRunResult.Success)

	// Ensure no duplicate MR was created
	mrsAfter := srv.State().ListMergeRequests(101, "governance/sync-security-md", "main", "opened")
	assert.Len(t, mrsAfter, 1)
}
