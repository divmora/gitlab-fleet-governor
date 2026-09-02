package gitlab_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlabsdk "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

type mockProjectsService struct {
	getProjectCalled bool
}

func (m *mockProjectsService) GetProject(pid any, opt *gitlabsdk.GetProjectOptions, options ...gitlabsdk.RequestOptionFunc) (*gitlabsdk.Project, *gitlabsdk.Response, error) {
	m.getProjectCalled = true
	return &gitlabsdk.Project{ID: 42, Name: "mocked"}, nil, nil
}
func (m *mockProjectsService) EditProject(pid any, opt *gitlabsdk.EditProjectOptions, options ...gitlabsdk.RequestOptionFunc) (*gitlabsdk.Project, *gitlabsdk.Response, error) {
	return nil, nil, nil
}
func (m *mockProjectsService) ListProjects(opt *gitlabsdk.ListProjectsOptions, options ...gitlabsdk.RequestOptionFunc) ([]*gitlabsdk.Project, *gitlabsdk.Response, error) {
	return nil, nil, nil
}
func (m *mockProjectsService) GetProjectPipelineRetention(pid any, options ...gitlabsdk.RequestOptionFunc) (int, *gitlabsdk.Response, error) {
	return 0, nil, nil
}
func (m *mockProjectsService) SetProjectPipelineRetention(pid any, seconds int, options ...gitlabsdk.RequestOptionFunc) (*gitlabsdk.Response, error) {
	return nil, nil
}

func TestClient_InitializationAndOptions(t *testing.T) {
	auth := &gitlab.ResolvedAuth{
		BaseURL:   "https://gitlab.example.com/api/v4",
		Token:     "test-token",
		TokenType: gitlab.TokenTypePrivate,
	}

	customProjects := &mockProjectsService{}
	client, err := gitlab.NewClient(auth, gitlab.WithProjectsService(customProjects))
	require.NoError(t, err)

	assert.Equal(t, "https://gitlab.example.com/api/v4", client.BaseURL())
	assert.NotNil(t, client.RawClient())
	assert.NotNil(t, client.Groups())
	assert.NotNil(t, client.ProtectedBranches())
	assert.NotNil(t, client.PushRules())
	assert.NotNil(t, client.ApprovalRules())
	assert.NotNil(t, client.Variables())
	assert.NotNil(t, client.Runners())
	assert.NotNil(t, client.Compliance())
	assert.NotNil(t, client.Webhooks())
	assert.NotNil(t, client.Members())
	assert.NotNil(t, client.Users())

	// Verify custom service injection
	p, _, err := client.Projects().GetProject(42, nil)
	require.NoError(t, err)
	assert.Equal(t, 42, p.ID)
	assert.True(t, customProjects.getProjectCalled)
}

func TestClient_TokenTypes(t *testing.T) {
	tokenTypes := []gitlab.TokenType{
		gitlab.TokenTypePrivate,
		gitlab.TokenTypeOAuth,
		gitlab.TokenTypeJob,
	}

	for _, tt := range tokenTypes {
		t.Run(string(tt), func(t *testing.T) {
			auth := &gitlab.ResolvedAuth{
				BaseURL:   "https://gitlab.com/api/v4",
				Token:     "token-xyz",
				TokenType: tt,
			}
			client, err := gitlab.NewClient(auth)
			require.NoError(t, err)
			assert.NotNil(t, client)
		})
	}
}

func TestNewClientFromConfig(t *testing.T) {
	cfg := &config.GitLabSettingsConfig{
		BaseURL:          "https://gitlab.mycorp.internal/api/v4",
		Token:            "secret-token",
		RateLimitRPS:     50,
		RateLimitBurst:   100,
		MaxRetries:       5,
		RetryBaseDelayMs: 200,
		RetryMaxDelayMs:  10000,
		TimeoutSeconds:   15,
	}

	client, err := gitlab.NewClientFromConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.mycorp.internal/api/v4", client.BaseURL())
}

func TestDefaultComplianceService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/graphql", r.URL.Path)
		assert.Equal(t, "Bearer my-oauth-token", r.Header.Get("Authorization"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"data": {
				"project": {
					"complianceFrameworks": {
						"nodes": [
							{"id": "gid://gitlab/ComplianceManagement::Framework/1", "name": "SOC2", "description": "SOC2 Compliance", "color": "#00ff00", "default": true}
						]
					}
				}
			}
		}`))
	}))
	defer server.Close()

	auth := &gitlab.ResolvedAuth{
		BaseURL:   server.URL,
		Token:     "my-oauth-token",
		TokenType: gitlab.TokenTypeOAuth,
	}

	client, err := gitlab.NewClient(auth)
	require.NoError(t, err)

	frameworks, err := client.Compliance().GetProjectComplianceFrameworks(context.Background(), 123)
	require.NoError(t, err)
	require.Len(t, frameworks, 1)
	assert.Equal(t, "SOC2", frameworks[0].Name)
	assert.True(t, frameworks[0].Default)
}
