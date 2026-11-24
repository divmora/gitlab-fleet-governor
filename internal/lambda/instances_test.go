package lambda_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
)

type instanceExec struct{}

func (instanceExec) Execute(_ context.Context, cfg *config.PolicyConfig) (*engine.ExecutionResult, error) {
	return &engine.ExecutionResult{
		Success:        true,
		Metrics:        &engine.SummaryMetricsSnapshot{TotalScanned: 2, TotalChanged: 1},
		ProjectChanges: []engine.ProjectChange{{ProjectID: 7, ProjectPath: "team/app-" + cfg.Run.Instance}},
	}, nil
}

func TestHandler_MultiInstanceEventConfig(t *testing.T) {
	type seenClient struct{ baseURL, token, envToken, envBaseURL string }
	var seen []seenClient

	handler := lambda.NewHandler(
		lambda.WithEnvLookup(func(k string) (string, bool) {
			switch k {
			case "GITLAB_TOKEN":
				return "operator-token", true
			case "GITLAB_BASE_URL":
				return "https://operator.example/api/v4", true
			}
			return "", false
		}),
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			require.Len(t, lookup, 1)
			tok, _ := lookup[0]("GITLAB_TOKEN")
			base, _ := lookup[0]("GITLAB_BASE_URL")
			seen = append(seen, seenClient{cfg.BaseURL, cfg.Token, tok, base})
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(gitlab.GitLabClient, *config.PolicyConfig) lambda.EngineExecutor {
			return instanceExec{}
		}),
	)

	payload := []byte(`{"dry_run": true, "config_yaml": "version: v1\ninstances:\n  - name: saas\n    token: glpat-a\n  - name: onprem\n    base_url: https://gitlab.corp.example/api/v4\n    token: glpat-b\n"}`)
	respAny, err := handler.HandleRequest(context.Background(), payload)
	require.NoError(t, err)
	resp := respAny.(*lambda.LambdaResponse)
	require.Equal(t, "SUCCESS", resp.Status, resp.Errors)

	// Each instance gets its own URL and token and no operator environment,
	// so GITLAB_BASE_URL cannot redirect the gitlab.com instance's token.
	assert.Equal(t, []seenClient{
		{"https://gitlab.com/api/v4", "glpat-a", "", ""},
		{"https://gitlab.corp.example/api/v4", "glpat-b", "", ""},
	}, seen)

	assert.Equal(t, 4, resp.Metrics.TotalScanned)
	assert.Equal(t, 2, resp.Metrics.TotalChanged)
	require.Len(t, resp.ChangedProjects, 2)
	assert.Equal(t, "saas", resp.ChangedProjects[0].Instance)
	assert.Equal(t, "onprem", resp.ChangedProjects[1].Instance)
}
