package gitlab_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

func TestRunEnvLookup_IsolatesInstances(t *testing.T) {
	env := func(k string) (string, bool) {
		switch k {
		case "GITLAB_BASE_URL":
			return "https://self-managed.example/api/v4", true
		case "GITLAB_TOKEN":
			return "env-token", true
		}
		return "", false
	}

	// An instance on gitlab.com must stay on gitlab.com even when
	// GITLAB_BASE_URL points elsewhere.
	inst := &config.PolicyConfig{Run: config.RunContext{Instance: "saas", InstanceCount: 2}}
	inst.Settings.GitLab = config.GitLabSettingsConfig{BaseURL: gitlab.DefaultGitLabBaseURL, Token: "saas-token"}
	auth, err := gitlab.ResolveAuth(&inst.Settings.GitLab, gitlab.RunEnvLookup(inst, env))
	require.NoError(t, err)
	assert.Equal(t, gitlab.DefaultGitLabBaseURL, auth.BaseURL)
	assert.Equal(t, "saas-token", auth.Token)

	// A single-instance policy keeps the documented environment fallbacks.
	single := &config.PolicyConfig{}
	auth, err = gitlab.ResolveAuth(&single.Settings.GitLab, gitlab.RunEnvLookup(single, env))
	require.NoError(t, err)
	assert.Equal(t, "https://self-managed.example/api/v4", auth.BaseURL)
	assert.Equal(t, "env-token", auth.Token)
}
