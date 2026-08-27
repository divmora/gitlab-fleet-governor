package lambda_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
)

func TestHandler_DirectInvocation(t *testing.T) {
	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "plan",
			DryRun:  true,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:     20,
				TotalTargeted:    5,
				TotalChanged:     2,
				TotalUnchanged:   3,
				TotalFailed:      0,
				ScannedProjects:  20,
				TargetedProjects: 5,
			},
			Duration: 50 * time.Millisecond,
		},
	}

	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	payload := []byte(`{
		"config_yaml": "version: 'v1'\nsettings:\n  concurrency: 10",
		"dry_run": true
	}`)

	respAny, err := handler.HandleRequest(context.Background(), payload)
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
	if resp.EventType != lambda.EventTypeDirectInvocation {
		t.Errorf("expected EventType='DIRECT_INVOCATION', got %q", resp.EventType)
	}
	if resp.Metrics == nil || resp.Metrics.TotalTargeted != 5 {
		t.Errorf("expected Metrics.TotalTargeted=5")
	}
}

func TestHandler_EventBridgeScheduledEvent(t *testing.T) {
	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "plan",
			DryRun:  true,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:     15,
				TotalTargeted:    3,
				TotalChanged:     0,
				TotalUnchanged:   3,
				TotalFailed:      0,
				ScannedProjects:  15,
				TargetedProjects: 3,
			},
			Duration: 40 * time.Millisecond,
		},
	}

	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	event := []byte(`{
		"version": "0",
		"id": "abc-123",
		"detail-type": "Scheduled Event",
		"source": "aws.events",
		"time": "2026-08-25T12:00:00Z",
		"resources": ["arn:aws:events:us-east-1:123456789012:rule/hourly-audit"],
		"detail": {
			"config_yaml": "version: 'v1'",
			"dry_run": true
		}
	}`)

	respAny, err := handler.HandleRequest(context.Background(), event)
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
	if resp.EventType != lambda.EventTypeEventBridgeSchedule {
		t.Errorf("expected EventType='EVENTBRIDGE_SCHEDULED', got %q", resp.EventType)
	}
}

func TestHandler_APIGatewayProxy(t *testing.T) {
	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "plan",
			DryRun:  true,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:     1,
				TotalTargeted:    1,
				TotalChanged:     0,
				TotalUnchanged:   1,
				TotalFailed:      0,
				ScannedProjects:  1,
				TargetedProjects: 1,
			},
			Duration: 20 * time.Millisecond,
		},
	}

	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	bodyPayload := `{"config_yaml": "version: 'v1'", "dry_run": true}`
	encodedBody := base64.StdEncoding.EncodeToString([]byte(bodyPayload))

	apigwReq := []byte(`{
		"httpMethod": "POST",
		"path": "/audit",
		"isBase64Encoded": true,
		"body": "` + encodedBody + `"
	}`)

	respAny, err := handler.HandleRequest(context.Background(), apigwReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	apigwResp, ok := respAny.(*lambda.APIGatewayProxyResponse)
	if !ok {
		t.Fatalf("expected *APIGatewayProxyResponse, got %T", respAny)
	}

	if apigwResp.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200, got %d", apigwResp.StatusCode)
	}

	var parsed lambda.LambdaResponse
	if err := json.Unmarshal([]byte(apigwResp.Body), &parsed); err != nil {
		t.Fatalf("failed to parse API Gateway body: %v", err)
	}
	if parsed.Status != "SUCCESS" {
		t.Errorf("expected Status='SUCCESS', got %q", parsed.Status)
	}
}

func TestHandler_PanicRecovery(t *testing.T) {
	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			panic("unexpected boom in client factory")
		}),
	)

	payload := []byte(`{
		"config_yaml": "version: 'v1'",
		"dry_run": true
	}`)

	respAny, err := handler.HandleRequest(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse, got %T", respAny)
	}

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected StatusCode=500, got %d", resp.StatusCode)
	}
	if resp.Status != "FAILED" {
		t.Errorf("expected Status='FAILED', got %q", resp.Status)
	}
	if len(resp.Errors) == 0 {
		t.Errorf("expected error message describing panic")
	}
}

