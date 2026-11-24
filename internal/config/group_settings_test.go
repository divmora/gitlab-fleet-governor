package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func TestGroupSettings_Validation(t *testing.T) {
	valid := `
version: v1
policies:
  group_settings:
    visibility: private
    require_two_factor_authentication: true
    two_factor_grace_period: 48
    project_creation_level: maintainer
    subgroup_creation_level: owner
    membership_lock: true
    share_with_group_lock: true
    prevent_forking_outside_group: true
    request_access_enabled: false
    emails_enabled: true
    mentions_disabled: false
    ip_restriction_ranges: ["10.0.0.0/8", "203.0.113.7"]
    allowed_email_domains: ["example.com", "corp.example.co.uk"]
    default_branch_protection:
      allowed_to_push: [40]
      allowed_to_merge: [30, 40]
      allow_force_push: false
      developer_can_initial_push: false
      code_owner_approval_required: true
`
	cfg, err := config.Unmarshal([]byte(valid))
	require.NoError(t, err)
	cfg.SetDefaults()
	require.NoError(t, cfg.Validate())
	require.NotNil(t, cfg.Policies.GroupSettings)
	assert.Equal(t, []int{30, 40}, *cfg.Policies.GroupSettings.DefaultBranchProtection.AllowedToMerge)

	for name, tc := range map[string]struct{ yaml, field string }{
		"visibility":       {"visibility: secret", "policies.group_settings.visibility"},
		"project creation": {"project_creation_level: everyone", "policies.group_settings.project_creation_level"},
		"subgroup":         {"subgroup_creation_level: developer", "policies.group_settings.subgroup_creation_level"},
		"grace period":     {"two_factor_grace_period: -1", "policies.group_settings.two_factor_grace_period"},
		"ip range":         {"ip_restriction_ranges: [\"10.0.0.0/33\"]", "policies.group_settings.ip_restriction_ranges[0]"},
		"email domain":     {"allowed_email_domains: [\"@example.com\"]", "policies.group_settings.allowed_email_domains[0]"},
		"access level":     {"default_branch_protection:\n      allowed_to_push: [20]", "policies.group_settings.default_branch_protection.allowed_to_push[0]"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Unmarshal([]byte("version: v1\npolicies:\n  group_settings:\n    " + tc.yaml + "\n"))
			require.NoError(t, err)
			cfg.SetDefaults()
			err = cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.field)
		})
	}
}
