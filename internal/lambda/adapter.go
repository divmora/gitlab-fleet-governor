package lambda

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
)

func (h *Handler) executeEngine(ctx context.Context, cfg *config.PolicyConfig) (*engine.ExecutionResult, error) {
	client, err := h.clientFactory(&cfg.Settings.GitLab, h.lookupEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GitLab client: %w", err)
	}

	eng := h.engineFactory(client, cfg)
	return eng.Execute(ctx, cfg)
}

// BuildLambdaResponse constructs a formatted LambdaResponse from engine execution output.
func BuildLambdaResponse(
	eventType EventType,
	configSource string,
	dryRun bool,
	res *engine.ExecutionResult,
	execErr error,
	startTime time.Time,
) *LambdaResponse {
	duration := time.Since(startTime)
	resp := &LambdaResponse{
		EventType:           eventType,
		ConfigSource:        configSource,
		DryRun:              dryRun,
		ExecutionDurationMS: duration.Milliseconds(),
		Errors:              make([]string, 0),
		ChangedProjects:     make([]ProjectChangeSummary, 0),
	}

	if execErr != nil {
		resp.StatusCode = http.StatusInternalServerError
		resp.Status = "FAILED"
		resp.Errors = append(resp.Errors, execErr.Error())
		return resp
	}

	if res == nil {
		resp.StatusCode = http.StatusInternalServerError
		resp.Status = "FAILED"
		resp.Errors = append(resp.Errors, "nil execution result returned from engine")
		return resp
	}

	resp.DryRun = res.DryRun

	if res.Metrics != nil {
		m := res.Metrics
		resp.Summary = &ExecutionSummary{
			ScannedGroups:     m.ScannedGroups,
			MatchedGroups:     m.TargetedGroups,
			ScannedProjects:   m.ScannedProjects,
			MatchedProjects:   m.TargetedProjects,
			AppliedOperations: m.TotalAppliedOperations,
			SkippedOperations: m.TotalSkippedOperations,
			FailedOperations:  m.TotalFailedOperations,
		}
		resp.Metrics = &ExecutionMetrics{
			TotalScanned:   m.TotalScanned,
			TotalTargeted:  m.TotalTargeted,
			TotalChanged:   m.TotalChanged,
			TotalUnchanged: m.TotalUnchanged,
			TotalFailed:    m.TotalFailed,
			Duration:       res.Duration,
		}
	}

	for _, ch := range res.ProjectChanges {
		resp.ChangedProjects = append(resp.ChangedProjects, ProjectChangeSummary{
			ProjectID:   ch.ProjectID,
			ProjectPath: ch.ProjectPath,
			Action:      string(ch.Action),
			Operations:  ch.Operations,
		})
	}

	for _, err := range res.Errors {
		resp.Errors = append(resp.Errors, err.Error())
	}

	if len(resp.Errors) > 0 && resp.Summary != nil && (resp.Summary.AppliedOperations > 0 || resp.Summary.MatchedProjects > 0) {
		resp.StatusCode = http.StatusOK
		resp.Status = "PARTIAL_SUCCESS"
	} else if len(resp.Errors) > 0 || !res.Success {
		resp.StatusCode = http.StatusInternalServerError
		resp.Status = "FAILED"
	} else {
		resp.StatusCode = http.StatusOK
		resp.Status = "SUCCESS"
	}

	return resp
}

// BuildErrorResponse constructs an error LambdaResponse.
func BuildErrorResponse(eventType EventType, statusCode int, err error, startTime time.Time) *LambdaResponse {
	duration := time.Since(startTime)
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	return &LambdaResponse{
		StatusCode:          statusCode,
		Status:              "FAILED",
		EventType:           eventType,
		Errors:              []string{errMsg},
		ExecutionDurationMS: duration.Milliseconds(),
	}
}

func mergeEngineResult(
	summary *ExecutionSummary,
	metrics *ExecutionMetrics,
	changed *[]ProjectChangeSummary,
	res *engine.ExecutionResult,
) {
	if res == nil {
		return
	}
	if res.Metrics != nil {
		m := res.Metrics
		summary.ScannedGroups += m.ScannedGroups
		summary.MatchedGroups += m.TargetedGroups
		summary.ScannedProjects += m.ScannedProjects
		summary.MatchedProjects += m.TargetedProjects
		summary.AppliedOperations += m.TotalAppliedOperations
		summary.SkippedOperations += m.TotalSkippedOperations
		summary.FailedOperations += m.TotalFailedOperations

		metrics.TotalScanned += m.TotalScanned
		metrics.TotalTargeted += m.TotalTargeted
		metrics.TotalChanged += m.TotalChanged
		metrics.TotalUnchanged += m.TotalUnchanged
		metrics.TotalFailed += m.TotalFailed
		metrics.Duration += res.Duration
	}

	for _, ch := range res.ProjectChanges {
		*changed = append(*changed, ProjectChangeSummary{
			ProjectID:   ch.ProjectID,
			ProjectPath: ch.ProjectPath,
			Action:      string(ch.Action),
			Operations:  ch.Operations,
		})
	}
}
