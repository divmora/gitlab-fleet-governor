package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func twoInstancePolicy(a, b *mockserver.MockGitLabServer, dryRun bool) *config.PolicyConfig {
	cfg := &config.PolicyConfig{
		Version:  "v1",
		Settings: config.SettingsConfig{DryRun: gogitlab.Ptr(dryRun), Concurrency: 2},
		Targets: config.TargetSelectors{GroupSelector: &config.GroupSelector{
			GroupIDsInclude: []int64{10},
			Recursive:       gogitlab.Ptr(false),
		}},
		Instances: []config.InstanceConfig{
			{Name: "alpha", GitLabSettingsConfig: config.GitLabSettingsConfig{BaseURL: a.BaseURL(), Token: "mock-token"}},
			{Name: "beta", GitLabSettingsConfig: config.GitLabSettingsConfig{BaseURL: b.BaseURL(), Token: "mock-token"}},
		},
		Policies: config.PoliciesConfig{Variables: []config.VariableConfig{{Key: "MULTI_INSTANCE_TEST", Value: "1"}}},
	}
	cfg.SetDefaults()
	return cfg
}

func realExecutor(ctx context.Context, cfg *config.PolicyConfig) (*engine.ExecutionResult, error) {
	client, err := gitlab.NewClientForRun(cfg)
	if err != nil {
		return nil, err
	}
	eng, err := engine.NewGovernanceEngine(client, cfg)
	if err != nil {
		return nil, err
	}
	return eng.Execute(ctx, cfg)
}

func seededServer(t *testing.T) *mockserver.MockGitLabServer {
	t.Helper()
	s := mockserver.NewMockGitLabServer()
	t.Cleanup(s.Close)
	s.Seed()
	return s
}

func TestExecuteInstances_TwoMockInstances(t *testing.T) {
	license.SetVerificationPublicKey(testutil.GetTestPublicKey())
	defer license.ResetVerificationPublicKey()

	alpha, beta := seededServer(t), seededServer(t)
	// The environment must not redirect either instance.
	t.Setenv("GITLAB_BASE_URL", "http://127.0.0.1:1/api/v4")
	t.Setenv("GITLAB_TOKEN", "env-token")

	cfg := twoInstancePolicy(alpha, beta, false)
	cfg.Settings.License.Key = testutil.CLITestToken

	single, err := realExecutor(context.Background(), cfg.InstanceRuns()[0].Config)
	require.NoError(t, err)
	perInstance := len(single.ProjectResults)
	require.Positive(t, perInstance)

	// Reset alpha, which the single run above just changed.
	alpha.Reset()
	alpha.Seed()

	res, err := engine.ExecuteInstances(context.Background(), cfg, realExecutor)
	require.NoError(t, err)
	require.True(t, res.Success)
	assert.Equal(t, "apply", res.Mode)

	require.Len(t, res.Instances, 2)
	assert.Equal(t, "alpha", res.Instances[0].Name)
	assert.Equal(t, alpha.BaseURL(), res.Instances[0].BaseURL)
	assert.True(t, res.Instances[0].Success)
	assert.Equal(t, "beta", res.Instances[1].Name)

	// Both instances report the same project IDs; the instance tells them apart.
	require.Len(t, res.ProjectResults, 2*perInstance)
	byInstance := map[string]int{}
	for _, pr := range res.ProjectResults {
		byInstance[pr.Instance]++
	}
	assert.Equal(t, map[string]int{"alpha": perInstance, "beta": perInstance}, byInstance)
	for _, ch := range res.ProjectChanges {
		assert.Contains(t, []string{"alpha", "beta"}, ch.Instance)
	}

	assert.Equal(t, res.Instances[0].Metrics.TotalScanned+res.Instances[1].Metrics.TotalScanned, res.Metrics.TotalScanned)
	assert.Equal(t, res.Instances[0].Metrics.TotalChanged+res.Instances[1].Metrics.TotalChanged, res.Metrics.TotalChanged)
	assert.Positive(t, res.Metrics.TotalChanged, "the variable was created on both instances")
}

func TestExecuteInstances_RequiresEnterpriseToApply(t *testing.T) {
	alpha, beta := seededServer(t), seededServer(t)
	t.Setenv("DIVMORA_LICENSE_KEY", "")

	res, err := engine.ExecuteInstances(context.Background(), twoInstancePolicy(alpha, beta, false), realExecutor)
	require.Error(t, err)
	assert.Contains(t, err.Error(), engine.FeatureMultiInstance)
	assert.Contains(t, err.Error(), "ENTERPRISE")
	assert.False(t, res.Success)
	assert.Zero(t, res.Metrics.TotalChanged, "nothing is applied without the licence")

	// Dry-run is permitted, as for every other commercial feature.
	res, err = engine.ExecuteInstances(context.Background(), twoInstancePolicy(alpha, beta, true), realExecutor)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "plan", res.Mode)
}

func TestExecuteInstances_SingleInstancePassThrough(t *testing.T) {
	cfg := &config.PolicyConfig{Version: "v1"}
	want := &engine.ExecutionResult{Mode: "plan", Success: true}
	calls := 0
	got, err := engine.ExecuteInstances(context.Background(), cfg, func(_ context.Context, c *config.PolicyConfig) (*engine.ExecutionResult, error) {
		calls++
		assert.Same(t, cfg, c)
		return want, nil
	})
	require.NoError(t, err)
	assert.Same(t, want, got)
	assert.Equal(t, 1, calls)
	assert.Empty(t, got.Instances)
}

func TestExecuteInstances_ContinuesAfterFailureAndCountsPriorProjects(t *testing.T) {
	cfg := &config.PolicyConfig{Version: "v1", Instances: []config.InstanceConfig{{Name: "a"}, {Name: "b"}, {Name: "c"}}}
	boom := errors.New("boom")
	var seen []config.RunContext

	res, err := engine.ExecuteInstances(context.Background(), cfg, func(_ context.Context, c *config.PolicyConfig) (*engine.ExecutionResult, error) {
		seen = append(seen, c.Run)
		fleet := &discovery.TargetFleet{Projects: map[int64]*discovery.TargetProject{1: {}, 2: {}, 3: {}}}
		switch c.Run.Instance {
		case "b":
			return nil, boom
		default:
			return &engine.ExecutionResult{
				Success:       true,
				Fleet:         fleet,
				TargetResults: []*engine.TargetResult{{TargetID: 1, Success: true}},
				Metrics:       &engine.SummaryMetricsSnapshot{TotalScanned: 3},
			}, nil
		}
	})

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), `instance "b"`)
	assert.Equal(t, []config.RunContext{
		{Instance: "a", InstanceCount: 3, PriorProjects: 0},
		{Instance: "b", InstanceCount: 3, PriorProjects: 3},
		{Instance: "c", InstanceCount: 3, PriorProjects: 3},
	}, seen)

	assert.False(t, res.Success)
	assert.Equal(t, 6, res.Metrics.TotalScanned)
	require.Len(t, res.TargetResults, 2)
	assert.Equal(t, "a", res.TargetResults[0].Instance)
	assert.Equal(t, "c", res.TargetResults[1].Instance)
	require.Len(t, res.Errors, 1)
	assert.True(t, res.HasErrors())
	assert.False(t, res.Instances[1].Success)
	assert.Equal(t, "boom", res.Instances[1].Error)
}
