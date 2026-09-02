package lambda_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
)

type mockEngineExecutor struct {
	result *engine.ExecutionResult
	err    error
}

func (m *mockEngineExecutor) Execute(ctx context.Context, cfg *config.PolicyConfig) (*engine.ExecutionResult, error) {
	return m.result, m.err
}

func TestS3Handler_URLEncodingAndExecution(t *testing.T) {
	s3Content := []byte(`
version: "v1"
settings:
  concurrency: 5
`)
	mockS3 := &mockS3Client{
		objects: map[string][]byte{
			"policies/fleet-governor.yaml": s3Content,
		},
	}

	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "plan",
			DryRun:  true,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:     10,
				TotalTargeted:    2,
				TotalChanged:     1,
				TotalUnchanged:   1,
				ScannedProjects:  10,
				TargetedProjects: 2,
			},
			ProjectChanges: []engine.ProjectChange{
				{
					ProjectID:   101,
					ProjectPath: "platform/core",
					Action:      "UPDATE",
					Operations:  []string{"push_rules"},
				},
			},
			Duration: 100 * time.Millisecond,
		},
	}

	handler := lambda.NewHandler(
		lambda.WithS3Client(mockS3),
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	// Note URL-encoded key: policies%2Ffleet-governor.yaml
	rawEvent := []byte(`{
		"Records": [
			{
				"eventVersion": "2.1",
				"eventSource": "aws:s3",
				"eventName": "ObjectCreated:Put",
				"s3": {
					"bucket": {"name": "governance-bucket"},
					"object": {"key": "policies%2Ffleet-governor.yaml"}
				}
			}
		]
	}`)

	respAny, err := handler.HandleRequest(context.Background(), rawEvent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse, got %T", respAny)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200, got %d", resp.StatusCode)
	}
	if resp.Status != "SUCCESS" {
		t.Errorf("expected Status='SUCCESS', got %q", resp.Status)
	}
	if resp.EventType != lambda.EventTypeS3Put {
		t.Errorf("expected EventType='S3_OBJECT_CREATED', got %q", resp.EventType)
	}
	if resp.Summary == nil || resp.Summary.MatchedProjects != 2 {
		t.Errorf("expected Summary.MatchedProjects=2")
	}
	if len(resp.ChangedProjects) != 1 {
		t.Errorf("expected 1 changed project, got %d", len(resp.ChangedProjects))
	}
}

func TestS3Handler_FilterNonConfigFiles(t *testing.T) {
	mockS3 := &mockS3Client{
		objects: map[string][]byte{},
	}

	handler := lambda.NewHandler(
		lambda.WithS3Client(mockS3),
	)

	rawEvent := []byte(`{
		"Records": [
			{
				"eventVersion": "2.1",
				"eventSource": "aws:s3",
				"eventName": "ObjectCreated:Put",
				"s3": {
					"bucket": {"name": "governance-bucket"},
					"object": {"key": "images/logo.png"}
				}
			}
		]
	}`)

	respAny, err := handler.HandleRequest(context.Background(), rawEvent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse, got %T", respAny)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200 for filtered file, got %d", resp.StatusCode)
	}
	if resp.Summary.MatchedProjects != 0 {
		t.Errorf("expected MatchedProjects=0 for non-config file")
	}
}

type mockGitLabClientStub struct {
	gitlab.GitLabClient
}
