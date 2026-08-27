package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/cli"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

// TestTier5_E2E_FullGovernancePipeline_AdversarialStress tests full end-to-end execution
// across all 10 governance operations, CLI subcommands, and Lambda triggers under stress.
func TestTier5_E2E_FullGovernancePipeline_AdversarialStress(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	tempDir := t.TempDir()

	// High-entropy secret canaries for leakage audit
	canarySecrets := []string{
		"glpat-EnterpriseSecretCanary-2026-X99",
		"Production_DB_Super_Password_987654321",
	}

	configYAML := fmt.Sprintf(`
version: "v1"
settings:
  concurrency: 5
  dry_run: true
  gitlab:
    base_url: "%s"
    token: "glpat-mock-token-test-governor"
    rate_limit_rps: 100
    rate_limit_burst: 100
targets:
  group_selector:
    group_ids_include: [10, 20]
    recursive: true
  project_selector:
    visibility: "private"
policies:
  push_rules:
    author_email_regex: "@example\\.com$"
    branch_name_regex: "^(main|release/.*|feat/.*)$"
    commit_message_regex: "^\\[(FEAT|FIX|CHORE|DOCS)\\]"
    prevent_secrets: true
    deny_delete_tag: true
    max_file_size: 10
  protected_branches:
    - name: "main"
      allowed_to_push:
        - access_level: 40
      allowed_to_merge:
        - access_level: 30
      allow_force_push: false
      code_owner_approval_required: true
  approval_rules:
    approvals_before_merge: 2
    reset_approvals_on_push: true
    rules:
      - name: "Security Review"
        approvals_required: 1
        user_usernames: ["bob"]
  project_settings:
    squash_option: "always"
    merge_method: "ff"
    only_allow_merge_if_pipeline_succeeds: true
    keep_latest_artifact: true
  pipeline_retention:
    retention_days: 45
  variables:
    - key: "DATABASE_PASS"
      value: "%s"
      masked: true
      protected: true
      environment_scope: "production"
    - key: "API_SECRET_TOKEN"
      value: "%s"
      masked: true
      protected: true
      environment_scope: "*"
  runners:
    shared_runners_enabled: true
    group_runners_enabled: true
  compliance:
    framework_name: "SOC2"
  webhooks:
    - url: "https://webhook.fleetcorp.com/events"
      push_events: true
      enable_ssl_verification: true
  members:
    max_access_level: 40
    enforce_expires_at: true
    max_expiration_days: 90
`, srv.BaseURL(), canarySecrets[0], canarySecrets[1])

	configFile := filepath.Join(tempDir, "enterprise_policy.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(configYAML), 0600))

	// 1. CLI Validation Subcommand
	t.Run("CLI Validate Subcommand JSON", func(t *testing.T) {
		cmd := cli.NewRootCmd()
		outBuf := new(bytes.Buffer)
		errBuf := new(bytes.Buffer)
		cmd.SetOut(outBuf)
		cmd.SetErr(errBuf)
		cmd.SetArgs([]string{"validate", "-c", configFile, "--json"})

		err := cmd.ExecuteContext(context.Background())
		require.NoError(t, err, "Validation must succeed for valid enterprise policy")

		var valOut cli.ValidateJSONOutput
		err = json.Unmarshal(outBuf.Bytes(), &valOut)
		require.NoError(t, err)
		assert.True(t, valOut.Valid)
		assert.Equal(t, "VALID", valOut.Status)
		assert.Empty(t, valOut.Errors)
	})

	// 2. CLI Run Dry-Run Simulation with Markdown Report File
	t.Run("CLI Run DryRun Markdown Export", func(t *testing.T) {
		reportFile := filepath.Join(tempDir, "dryrun_report.md")

		cmd := cli.NewRootCmd()
		outBuf := new(bytes.Buffer)
		errBuf := new(bytes.Buffer)
		cmd.SetOut(outBuf)
		cmd.SetErr(errBuf)
		cmd.SetArgs([]string{
			"run",
			"-c", configFile,
			"--dry-run=true",
			"--report-format", "markdown",
			"-o", reportFile,
		})

		err := cmd.ExecuteContext(context.Background())
		require.NoError(t, err, "CLI dry-run execution must complete cleanly")

		// Verify Markdown report was written
		reportBytes, err := os.ReadFile(reportFile)
		require.NoError(t, err)
		reportContent := string(reportBytes)
		assert.Contains(t, reportContent, "GitLab Fleet Governor")
		assert.Contains(t, reportContent, "push_rules")

		// Verify zero secret leakage in exported Markdown report
		for _, canary := range canarySecrets {
			assert.NotContains(t, reportContent, canary, "Exported Markdown report leaked secret canary: %s", canary)
		}
	})

	// 3. CLI Run Live Apply with JSON Report File
	t.Run("CLI Run Live Apply JSON Export", func(t *testing.T) {
		reportFile := filepath.Join(tempDir, "apply_report.json")

		cmd := cli.NewRootCmd()
		outBuf := new(bytes.Buffer)
		errBuf := new(bytes.Buffer)
		cmd.SetOut(outBuf)
		cmd.SetErr(errBuf)
		cmd.SetArgs([]string{
			"run",
			"-c", configFile,
			"--dry-run=false",
			"--concurrency=5",
			"--report-format", "json",
			"-o", reportFile,
		})

		err := cmd.ExecuteContext(context.Background())
		require.NoError(t, err, "CLI live apply execution must complete cleanly")

		// Verify JSON report was written and is valid JSON
		reportBytes, err := os.ReadFile(reportFile)
		require.NoError(t, err)

		var rd report.ReportData
		err = json.Unmarshal(reportBytes, &rd)
		require.NoError(t, err)
		assert.False(t, rd.DryRun)
		assert.Greater(t, rd.TotalTargeted, 0)

		// Verify zero secret leakage in exported JSON report
		for _, canary := range canarySecrets {
			assert.NotContains(t, string(reportBytes), canary, "Exported JSON report leaked secret canary: %s", canary)
		}
	})

	// 4. Concurrent AWS Lambda Execution Stress Test
	t.Run("Concurrent AWS Lambda Invocations", func(t *testing.T) {
		client, err := srv.GovernorClient()
		require.NoError(t, err)

		handler := lambda.NewHandler(
			lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
				return client, nil
			}),
		)

		const numLambdaWorkers = 20
		var wg sync.WaitGroup
		wg.Add(numLambdaWorkers)

		var successCount int64
		var errorCount int64

		for i := 0; i < numLambdaWorkers; i++ {
			go func(workerID int) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				var payload []byte
				if workerID%2 == 0 {
					// EventBridge Scheduled Trigger
					payload = []byte(fmt.Sprintf(`{
						"version": "0",
						"id": "event-1234-%d",
						"detail-type": "Scheduled Event",
						"source": "aws.events",
						"time": "2026-08-26T00:00:00Z",
						"region": "us-east-1",
						"resources": ["arn:aws:events:us-east-1:123456789012:rule/NightlyGovernance"],
						"detail": {
							"config_yaml": %q,
							"dry_run": true
						}
					}`, workerID, configYAML))
				} else {
					// Direct JSON Invocation
					payload = []byte(fmt.Sprintf(`{
						"action": "dry_run",
						"config_yaml": %q
					}`, configYAML))
				}

				respAny, err := handler.HandleRequest(ctx, payload)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					return
				}

				resp, ok := respAny.(*lambda.LambdaResponse)
				if !ok || resp.StatusCode != http.StatusOK {
					atomic.AddInt64(&errorCount, 1)
					return
				}

				atomic.AddInt64(&successCount, 1)
			}(i)
		}

		wg.Wait()

		assert.Equal(t, int64(numLambdaWorkers), atomic.LoadInt64(&successCount), "All concurrent Lambda invocations must succeed")
		assert.Equal(t, int64(0), atomic.LoadInt64(&errorCount))
	})
}
