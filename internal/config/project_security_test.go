package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func TestProjectSecurity_Validation(t *testing.T) {
	valid := `
version: v1
policies:
  project_settings:
    visibility: private
    public_jobs: false
    forking_access_level: private
    ci_pipeline_variables_minimum_override_role: maintainer
    ci_restrict_pipeline_cancellation_role: no_one
  job_token_scope:
    limit_access: true
    allowed_projects: ["platform/deployer", "42"]
    allowed_groups: ["platform"]
    prune: true
  secret_push_protection:
    enabled: true
`
	cfg, err := config.Unmarshal([]byte(valid))
	require.NoError(t, err)
	cfg.SetDefaults()
	require.NoError(t, cfg.Validate())

	for name, tc := range map[string]struct{ yaml, field string }{
		"visibility":    {"project_settings:\n    visibility: secret", "policies.project_settings.visibility"},
		"forking":       {"project_settings:\n    forking_access_level: public", "policies.project_settings.forking_access_level"},
		"override role": {"project_settings:\n    ci_pipeline_variables_minimum_override_role: guest", "policies.project_settings.ci_pipeline_variables_minimum_override_role"},
		"cancel role":   {"project_settings:\n    ci_restrict_pipeline_cancellation_role: owner", "policies.project_settings.ci_restrict_pipeline_cancellation_role"},
		"empty entry":   {"job_token_scope:\n    allowed_projects: [\" \"]", "policies.job_token_scope.allowed_projects[0]"},
		"duplicate":     {"job_token_scope:\n    allowed_groups: [\"a\", \"A/\"]", "policies.job_token_scope.allowed_groups[1]"},
		"prune alone":   {"job_token_scope:\n    prune: true", "policies.job_token_scope.prune"},
		"spp enabled":   {"secret_push_protection: {}", "policies.secret_push_protection.enabled"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Unmarshal([]byte("version: v1\npolicies:\n  " + tc.yaml + "\n"))
			require.NoError(t, err)
			cfg.SetDefaults()
			err = cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.field)
		})
	}
}
