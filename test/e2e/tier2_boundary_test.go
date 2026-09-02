package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/cli"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ----------------------------------------------------------------------------
// Tier 2: Boundary & Corner Cases
// ----------------------------------------------------------------------------

// 1. Empty Fleet Discovery (Zero Matching Targets)
func TestE2E_Tier2_Empty_Fleet_Discovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	emptyPolicyYAML := fmt.Sprintf(`
version: "v1"
settings:
  dry_run: true
  concurrency: 2
  report_format: "json"
  gitlab:
    base_url: "%s"
    token: "mock-token"
targets:
  project_selector:
    project_name_regex_include: "^nonexistent-project-xyz-[0-9]+$"
policies:
  pipeline_retention:
    retention_days: 30
`, h.Server.BaseURL())

	policyPath := h.WriteConfigFile("empty_fleet_policy.yaml", emptyPolicyYAML)

	t.Run("CLI Run on Empty Fleet Returns Success With Zero Targets", func(t *testing.T) {
		stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--report-format=json")
		require.NoError(t, err, "stderr: %s", stderr)

		var rd report.ReportData
		err = json.Unmarshal([]byte(stdout), &rd)
		require.NoError(t, err)

		assert.Equal(t, 0, rd.TotalTargeted)
		assert.Equal(t, 0, rd.TotalChanged)
		assert.Equal(t, 0, rd.TotalFailed)
		assert.Empty(t, rd.Targets)
	})

	t.Run("All Report Formats Render Empty Fleet Gracefully", func(t *testing.T) {
		for _, format := range []string{"table", "csv", "markdown", "summary"} {
			stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--report-format", format)
			require.NoError(t, err, "format %s failed, stderr: %s", format, stderr)
			assert.NotEmpty(t, stdout)
		}
	})
}

// 2. High-Scale 1,000+ Items Keyset Pagination
func TestE2E_Tier2_High_Scale_Keyset_Pagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	// Seed 1,050 projects in mock state under a dedicated parent group
	largeGroup := h.Server.State().AddGroup(&gogitlab.Group{
		ID:       900,
		Name:     "Scale Group",
		Path:     "scale-group",
		FullPath: "scale-group",
	})

	totalScaleProjects := 1050
	for i := 1; i <= totalScaleProjects; i++ {
		projID := 1000 + i
		p := h.Server.State().AddProject(&gogitlab.Project{
			ID:                projID,
			Name:              fmt.Sprintf("scale-proj-%04d", i),
			Path:              fmt.Sprintf("scale-proj-%04d", i),
			PathWithNamespace: fmt.Sprintf("scale-group/scale-proj-%04d", i),
			DefaultBranch:     "main",
			Visibility:        gogitlab.PrivateVisibility,
			Archived:          false,
		})
		h.Server.State().AddGroupProject(largeGroup.ID, p.ID)
	}

	client, err := h.GovernorClient(gl.WithRateLimit(500.0, 500))
	require.NoError(t, err)

	boolTrue := true
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupIDsInclude: []int{largeGroup.ID},
			Recursive:       &boolTrue,
		},
	}

	fleet, err := discovery.DiscoverFleet(ctx, client, targets, discovery.WithConcurrency(8))
	require.NoError(t, err)

	assert.Equal(t, 1, len(fleet.Groups), "scale parent group should be discovered")
	assert.Equal(t, totalScaleProjects, len(fleet.Projects), "all 1,050 projects must be discovered via keyset pagination")

	// Verify all individual project IDs are present without duplicates or dropped items
	for i := 1; i <= totalScaleProjects; i++ {
		projID := 1000 + i
		assert.Contains(t, fleet.Projects, projID, "project ID %d should be discovered", projID)
	}
}

// 3. Circular Subgroup BFS Traversal & Cycle Detection
func TestE2E_Tier2_Circular_Subgroup_Cycle_Detection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	// Inject circular subgroup tree: Group 500 -> Subgroup 501 -> Subgroup 502 -> Subgroup 500
	h.Server.SeedCircularSubgroups()

	client, err := h.GovernorClient()
	require.NoError(t, err)

	boolTrue := true
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupIDsInclude: []int{500},
			Recursive:       &boolTrue,
		},
	}

	// BFS traversal must terminate cleanly without infinite loop or stack overflow
	fleet, err := discovery.DiscoverFleet(ctx, client, targets)
	require.NoError(t, err)

	assert.Equal(t, 3, len(fleet.Groups), "cycle detection must discover exactly 3 unique groups in the cycle")
	assert.Contains(t, fleet.Groups, 500)
	assert.Contains(t, fleet.Groups, 501)
	assert.Contains(t, fleet.Groups, 502)
}

// 4. HTTP 429 Rate Limit Burst Storm & Jittered Exponential Backoff
func TestE2E_Tier2_HTTP_429_Rate_Limit_Recovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	// Inject 2 consecutive HTTP 429 responses with Retry-After: 1 on /api/v4/projects
	h.Server.Faults().Inject429("GET", "/api/v4/projects", 2, 1)

	client, err := h.GovernorClient(
		gl.WithMaxRetries(3),
		gl.WithRetryBaseDelay(50*time.Millisecond),
	)
	require.NoError(t, err)

	targets := config.TargetSelectors{
		ProjectSelector: &config.ProjectSelector{
			NamespacesInclude: []string{"platform"},
		},
	}

	fleet, err := discovery.DiscoverFleet(ctx, client, targets)
	require.NoError(t, err, "client must automatically recover from HTTP 429 rate limit errors")
	assert.NotEmpty(t, fleet.Projects)
	assert.Contains(t, fleet.Projects, 101)
}

// 5. HTTP 500/502/503/504 Transient Server Errors & Backoff Recovery
func TestE2E_Tier2_HTTP_5xx_Transient_Error_Recovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	// Inject 1x HTTP 500 and 1x HTTP 503 on project 101 push_rule endpoint
	h.Server.Faults().Inject5xx("GET", "/api/v4/projects/101/push_rule", http.StatusInternalServerError, 1)
	h.Server.Faults().Inject5xx("GET", "/api/v4/projects/101/push_rule", http.StatusServiceUnavailable, 1)

	policyYAML := fmt.Sprintf(`
version: "v1"
settings:
  dry_run: false
  concurrency: 1
  gitlab:
    base_url: "%s"
    token: "mock-token"
    max_retries: 4
    retry_base_delay_ms: 50
targets:
  project_selector:
    namespaces_include:
      - "platform"
    project_name_regex_include: "^fleet-governor$"
policies:
  push_rules:
    author_email_regex: '@resilient\.org$'
    prevent_secrets: true
`, h.Server.BaseURL())

	policyPath := h.WriteConfigFile("resilience_policy.yaml", policyYAML)

	stdout, stderr, err := h.ExecuteCLI(ctx, "run", "-c", policyPath, "--report-format=json")
	require.NoError(t, err, "stderr: %s", stderr)

	var rd report.ReportData
	err = json.Unmarshal([]byte(stdout), &rd)
	require.NoError(t, err)

	assert.Equal(t, 0, rd.TotalFailed, "transient 5xx errors should be retried and recovered")
	assert.GreaterOrEqual(t, rd.TotalChanged, 1)

	pr, found := h.Server.State().GetProjectPushRule("101")
	require.True(t, found)
	assert.Equal(t, `@resilient\.org$`, pr.AuthorEmailRegex)
}

// 6. Variable Masking Length & Charset Limits
func TestE2E_Tier2_Variable_Masking_Constraints(t *testing.T) {
	h := NewE2EHarness(t)

	t.Run("Valid Masked Variable (>=8 chars, valid charset)", func(t *testing.T) {
		validYAML := h.BasePolicyYAML(`
  variables:
    - key: "VALID_SECRET_TOKEN"
      value: "super_secret_token_12345"
      variable_type: "env_var"
      protected: true
      masked: true
      raw: true
      environment_scope: "*"
`)
		validPath := h.WriteConfigFile("valid_masked_var.yaml", validYAML)
		_, _, err := config.Load(context.Background(), validPath)
		require.NoError(t, err)
	})

	t.Run("Invalid Masked Variable - Too Short (<8 chars)", func(t *testing.T) {
		shortYAML := h.BasePolicyYAML(`
  variables:
    - key: "SHORT_SECRET"
      value: "short"
      variable_type: "env_var"
      masked: true
`)
		shortPath := h.WriteConfigFile("short_masked_var.yaml", shortYAML)
		_, _, err := config.Load(context.Background(), shortPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "masked CI/CD variable value must be at least 8 characters long")
	})

	t.Run("Invalid Masked Variable - Invalid Charset (spaces / special symbols)", func(t *testing.T) {
		invalidCharYAML := h.BasePolicyYAML(`
  variables:
    - key: "INVALID_CHAR_SECRET"
      value: "secret with invalid spaces!"
      variable_type: "env_var"
      masked: true
`)
		invalidCharPath := h.WriteConfigFile("invalid_char_masked_var.yaml", invalidCharYAML)
		_, _, err := config.Load(context.Background(), invalidCharPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "masked CI/CD variable value contains invalid characters")
	})
}

// 7. Nil / Empty / Corrupt / Invalid Config Files
func TestE2E_Tier2_Config_File_Edge_Cases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	t.Run("Empty 0-byte File", func(t *testing.T) {
		emptyPath := h.WriteConfigFile("empty.yaml", "")
		_, stderr, err := h.ExecuteCLI(ctx, "validate", "-c", emptyPath)
		require.Error(t, err)
		assert.Contains(t, stderr+err.Error(), "empty configuration content")
	})

	t.Run("Whitespace Only File", func(t *testing.T) {
		wsPath := h.WriteConfigFile("whitespace.yaml", "   \n\t  \n")
		_, stderr, err := h.ExecuteCLI(ctx, "validate", "-c", wsPath)
		require.Error(t, err)
		assert.Contains(t, stderr+err.Error(), "empty configuration content")
	})

	t.Run("Non-existent File Path", func(t *testing.T) {
		_, stderr, err := h.ExecuteCLI(ctx, "run", "-c", filepath.Join(h.TempDir, "does-not-exist.yaml"))
		require.Error(t, err)
		assert.Contains(t, stderr+err.Error(), "failed to load configuration")
	})

	t.Run("Corrupt Malformed YAML Syntax", func(t *testing.T) {
		corruptYAML := `
version: "v1"
settings:
  concurrency: 5
policies:
  push_rules:
  - invalid_tab:	value
`
		corruptPath := h.WriteConfigFile("corrupt.yaml", corruptYAML)
		_, stderr, err := h.ExecuteCLI(ctx, "validate", "-c", corruptPath)
		require.Error(t, err)
		assert.NotEmpty(t, stderr+err.Error())
	})

	t.Run("Semantic Schema Violation (Invalid Regex, Negative Concurrency)", func(t *testing.T) {
		semanticInvalidYAML := fmt.Sprintf(`
version: "v1"
settings:
  concurrency: -5
  gitlab:
    base_url: "%s"
targets:
  project_selector:
    project_name_regex_include: "[unclosed-regex-syntax"
policies:
  pipeline_retention:
    retention_days: -10
`, h.Server.BaseURL())

		semanticPath := h.WriteConfigFile("semantic_invalid.yaml", semanticInvalidYAML)
		stdout, _, err := h.ExecuteCLI(ctx, "validate", "-c", semanticPath, "--json")
		require.Error(t, err)

		var out cli.ValidateJSONOutput
		jsonErr := json.Unmarshal([]byte(stdout), &out)
		require.NoError(t, jsonErr)
		assert.False(t, out.Valid)
		assert.Equal(t, "INVALID", out.Status)
		assert.NotEmpty(t, out.Errors)

		fields := make(map[string]bool)
		for _, e := range out.Errors {
			fields[e.Field] = true
		}
		assert.True(t, fields["settings.concurrency"] || fields["targets.project_selector.project_name_regex_include"])
	})
}

// 8. AWS Lambda S3 Event Payloads: URL Unescaping & Non-Config Extension Filtering
func TestE2E_Tier2_Lambda_S3_Payload_Handling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)

	validPolicyYAML := h.BasePolicyYAML(`
  pipeline_retention:
    retention_days: 45
`)

	t.Run("URL Unescaping of S3 Object Keys", func(t *testing.T) {
		// Key stored in S3 contains spaces and plus: "policies/fleet prod+v1.yaml"
		storedKey := "policies/fleet prod+v1.yaml"
		h.S3Client.Put("governance-bucket", storedKey, []byte(validPolicyYAML))

		// AWS S3 Event notifications URL-encode keys: spaces as '%20' or '+'
		rawEvent := []byte(`{
			"Records": [
				{
					"eventVersion": "2.1",
					"eventSource": "aws:s3",
					"eventName": "ObjectCreated:Put",
					"s3": {
						"bucket": {"name": "governance-bucket"},
						"object": {"key": "policies%2Ffleet%20prod%2Bv1.yaml"}
					}
				}
			]
		}`)

		handler := h.NewLambdaHandler()
		respAny, err := handler.HandleRequest(ctx, rawEvent)
		require.NoError(t, err)

		resp, ok := respAny.(*lambda.LambdaResponse)
		require.True(t, ok)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "SUCCESS", resp.Status)
		assert.Equal(t, lambda.EventTypeS3Put, resp.EventType)
		assert.Contains(t, resp.ConfigSource, "policies/fleet prod+v1.yaml")
	})

	t.Run("Filtering Non-Config Extensions (.png, .txt, .zip)", func(t *testing.T) {
		rawEvent := []byte(`{
			"Records": [
				{
					"eventVersion": "2.1",
					"eventSource": "aws:s3",
					"eventName": "ObjectCreated:Put",
					"s3": {
						"bucket": {"name": "governance-bucket"},
						"object": {"key": "assets/diagram.png"}
					}
				},
				{
					"eventVersion": "2.1",
					"eventSource": "aws:s3",
					"eventName": "ObjectCreated:Put",
					"s3": {
						"bucket": {"name": "governance-bucket"},
						"object": {"key": "docs/readme.txt"}
					}
				}
			]
		}`)

		handler := h.NewLambdaHandler()
		respAny, err := handler.HandleRequest(ctx, rawEvent)
		require.NoError(t, err)

		resp, ok := respAny.(*lambda.LambdaResponse)
		require.True(t, ok)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 0, resp.Summary.MatchedProjects, "non-config files should be ignored with 0 matched projects")
	})

	t.Run("Empty Records Array in S3 Event Returns Bad Request", func(t *testing.T) {
		rawEvent := []byte(`{"Records": []}`)

		handler := h.NewLambdaHandler()
		respAny, err := handler.HandleRequest(ctx, rawEvent)
		require.NoError(t, err)

		resp, ok := respAny.(*lambda.LambdaResponse)
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "FAILED", resp.Status)
	})
}
