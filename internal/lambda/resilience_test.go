package lambda_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
)

func TestHandler_Resilience_PanicRecovery(t *testing.T) {
	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			panic("simulated fatal panic in lambda client factory")
		}),
	)

	payload := []byte(`{"config_yaml": "version: 'v1'"}`)
	respAny, err := handler.HandleRequest(context.Background(), payload)
	if err != nil {
		t.Fatalf("handler returned unexpected Go error instead of LambdaResponse: %v", err)
	}

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse on panic recovery, got %T", respAny)
	}

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected StatusCode=500 on panic, got %d", resp.StatusCode)
	}
	if resp.Status != "FAILED" {
		t.Errorf("expected Status='FAILED', got %q", resp.Status)
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected error message recorded from recovered panic")
	}
}

func TestHandler_PartialSuccess(t *testing.T) {
	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "apply",
			DryRun:  false,
			Success: false,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:           10,
				TotalTargeted:          2,
				TotalChanged:           1,
				TotalFailed:            1,
				TotalAppliedOperations: 1,
				TotalFailedOperations:  1,
			},
			Errors: []error{errors.New("project 102 failed: 403 Forbidden")},
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

	payload := []byte(`{"config_yaml": "version: 'v1'", "dry_run": false}`)
	respAny, err := handler.HandleRequest(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse, got %T", respAny)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200 on partial success, got %d", resp.StatusCode)
	}
	if resp.Status != "PARTIAL_SUCCESS" {
		t.Errorf("expected Status='PARTIAL_SUCCESS', got %q", resp.Status)
	}
	if len(resp.Errors) != 1 {
		t.Errorf("expected 1 error recorded in Errors, got %d", len(resp.Errors))
	}
}

func TestHandler_InvalidConfiguration(t *testing.T) {
	handler := lambda.NewHandler()

	payload := []byte(`{"config_yaml": "invalid: yaml: : content [}"}`)
	respAny, err := handler.HandleRequest(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse, got %T", respAny)
	}

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected StatusCode=400 on invalid config, got %d", resp.StatusCode)
	}
	if resp.Status != "FAILED" {
		t.Errorf("expected Status='FAILED', got %q", resp.Status)
	}
}

func TestHandler_EngineExecutionError(t *testing.T) {
	mockExec := &mockEngineExecutor{
		err: errors.New("fleet discovery connection timeout"),
	}

	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	payload := []byte(`{"config_yaml": "version: 'v1'"}`)
	respAny, err := handler.HandleRequest(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse, got %T", respAny)
	}

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected StatusCode=500 on engine error, got %d", resp.StatusCode)
	}
	if resp.Status != "FAILED" {
		t.Errorf("expected Status='FAILED', got %q", resp.Status)
	}
}
