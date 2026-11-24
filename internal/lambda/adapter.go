package lambda

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/notify"
)

func (h *Handler) executeEngine(ctx context.Context, cfg *config.PolicyConfig) (*engine.ExecutionResult, error) {
	res, err := engine.ExecuteInstances(ctx, cfg, func(ctx context.Context, instCfg *config.PolicyConfig) (*engine.ExecutionResult, error) {
		client, err := h.clientFactory(&instCfg.Settings.GitLab, gitlab.RunEnvLookup(instCfg, h.lookupEnv))
		if err != nil {
			return nil, fmt.Errorf("failed to initialize GitLab client: %w", err)
		}
		return h.engineFactory(client, instCfg).Execute(ctx, instCfg)
	})
	h.notify(ctx, cfg, res, err)
	return res, err
}

// notify sends the run summary. On Lambda, notification targets come only
// from the function's own environment (GLFG_* variables), never from a
// policy: whoever can invoke the function can supply a config, and must
// not be able to choose where fleet findings are sent. Event configs that
// try are also rejected by config.UntrustedPolicy.
func (h *Handler) notify(ctx context.Context, cfg *config.PolicyConfig, res *engine.ExecutionResult, execErr error) {
	if cfg == nil {
		return
	}
	if !cfg.Settings.Notifications.IsEmpty() {
		slog.Info("settings.notifications is ignored on Lambda; set notification targets with GLFG_* environment variables")
	}
	lookup := h.lookupEnv
	n := notify.Resolve(nil, notify.Overrides{}, func(k string) (string, bool) {
		if lookup == nil {
			return "", false
		}
		return lookup(k)
	})
	if n.IsEmpty() {
		return
	}
	baseURL := cfg.Settings.GitLab.BaseURL
	if cfg.IsMultiInstance() {
		baseURL = cfg.Instances[0].BaseURL
	}
	// The licence check reads no file paths from the config: on Lambda a
	// file-based licence comes from DIVMORA_LICENSE_FILE (or the default
	// location), which the licence resolver falls back to.
	notify.Send(ctx, h.notifier, n, notify.FromExecution(res, execErr), notify.Gate{
		GitLabBaseURL: baseURL,
		LicenseKey:    cfg.Settings.License.Key,
		CRL:           cfg.Settings.License.CRL,
	})
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
			Instance:    ch.Instance,
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
			Instance:    ch.Instance,
			ProjectID:   ch.ProjectID,
			ProjectPath: ch.ProjectPath,
			Action:      string(ch.Action),
			Operations:  ch.Operations,
		})
	}
}
