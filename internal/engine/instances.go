package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
)

// FeatureMultiInstance is the licence feature required to govern more than
// one GitLab instance in a single run.
const FeatureMultiInstance = "platform.multi_instance"

// InstanceSummary reports the outcome of one GitLab instance in a
// multi-instance run.
type InstanceSummary struct {
	Name    string                  `json:"name"`
	BaseURL string                  `json:"base_url"`
	Success bool                    `json:"success"`
	Error   string                  `json:"error,omitempty"`
	Metrics *SummaryMetricsSnapshot `json:"metrics,omitempty"`
}

// InstanceExecutor governs a single GitLab instance. cfg is the policy as it
// applies to that instance: cfg.Settings.GitLab holds the instance's
// connection settings and cfg.Run identifies it.
type InstanceExecutor func(ctx context.Context, cfg *config.PolicyConfig) (*ExecutionResult, error)

// ExecuteInstances runs exec once per GitLab instance in cfg and merges the
// results. A single-instance policy is passed to exec unchanged, so existing
// behaviour and output are preserved.
//
// Instances run one after another. A failure on one instance is recorded and
// the remaining instances still run; the returned error joins every
// instance's error. Projects found on earlier instances count towards licence
// limits on later ones.
func ExecuteInstances(ctx context.Context, cfg *config.PolicyConfig, exec InstanceExecutor) (*ExecutionResult, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}
	if !cfg.IsMultiInstance() {
		return exec(ctx, cfg)
	}

	merged := &ExecutionResult{
		Success:        true,
		GroupResults:   make([]*TargetResult, 0),
		ProjectResults: make([]*TargetResult, 0),
		TargetResults:  make([]*TargetResult, 0),
		ProjectChanges: make([]ProjectChange, 0),
		Instances:      make([]*InstanceSummary, 0, len(cfg.Instances)),
		StartedAt:      time.Now(),
	}
	merged.DryRun = cfg.Settings.DryRun == nil || *cfg.Settings.DryRun
	merged.Mode = "apply"
	if merged.DryRun {
		merged.Mode = "plan"
	}

	var metrics *SummaryMetricsSnapshot
	var runErrs []error
	gotResult := false
	priorProjects := 0

	for _, run := range cfg.InstanceRuns() {
		summary := &InstanceSummary{Name: run.Name, BaseURL: run.Config.Settings.GitLab.BaseURL}
		merged.Instances = append(merged.Instances, summary)

		if err := ctx.Err(); err != nil {
			summary.Error = err.Error()
			runErrs = append(runErrs, fmt.Errorf("instance %q: %w", run.Name, err))
			merged.Errors = append(merged.Errors, fmt.Errorf("instance %q: %w", run.Name, err))
			merged.Success = false
			continue
		}

		run.Config.Run.PriorProjects = priorProjects
		res, err := exec(ctx, run.Config)
		if err != nil {
			summary.Error = err.Error()
			runErrs = append(runErrs, fmt.Errorf("instance %q: %w", run.Name, err))
		}
		if res == nil {
			if err != nil {
				merged.Errors = append(merged.Errors, fmt.Errorf("instance %q: %w", run.Name, err))
			}
			merged.Success = false
			continue
		}

		if !gotResult && res.Mode != "" {
			// Report the mode the engine actually ran in.
			merged.Mode, merged.DryRun = res.Mode, res.DryRun
			gotResult = true
		}
		summary.Success = res.Success && err == nil
		summary.Metrics = res.Metrics
		if !summary.Success {
			merged.Success = false
		}
		if res.Fleet != nil {
			priorProjects += len(res.Fleet.Projects)
		}
		mergeInstanceResult(merged, run.Name, res)
		metrics = mergeSnapshots(metrics, res.Metrics)
	}

	merged.CompletedAt = time.Now()
	merged.Duration = merged.CompletedAt.Sub(merged.StartedAt)
	if metrics == nil {
		metrics = NewSummaryMetrics(merged.DryRun).Snapshot()
	}
	metrics.StartTime = merged.StartedAt
	metrics.EndTime = merged.CompletedAt
	metrics.ExecutionDuration = merged.Duration
	metrics.DurationMs = merged.Duration.Milliseconds()
	merged.Metrics = metrics
	merged.SummaryMetrics = metrics

	return merged, errors.Join(runErrs...)
}

func mergeInstanceResult(dst *ExecutionResult, instance string, res *ExecutionResult) {
	stamp := func(in []*TargetResult) []*TargetResult {
		for _, tr := range in {
			if tr != nil {
				tr.Instance = instance
			}
		}
		return in
	}
	dst.GroupResults = append(dst.GroupResults, stamp(res.GroupResults)...)
	dst.ProjectResults = append(dst.ProjectResults, stamp(res.ProjectResults)...)
	// TargetResults usually holds the same pointers as Group/ProjectResults;
	// stamping twice is harmless.
	dst.TargetResults = append(dst.TargetResults, stamp(res.TargetResults)...)
	for _, ch := range res.ProjectChanges {
		ch.Instance = instance
		dst.ProjectChanges = append(dst.ProjectChanges, ch)
	}
	for _, err := range res.Errors {
		dst.Errors = append(dst.Errors, fmt.Errorf("instance %q: %w", instance, err))
	}
}

// mergeSnapshots adds src's counts into dst, allocating dst when nil. Times
// and durations are left for the caller to set.
func mergeSnapshots(dst, src *SummaryMetricsSnapshot) *SummaryMetricsSnapshot {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = &SummaryMetricsSnapshot{
			DryRun:          src.DryRun,
			OperationCounts: make(map[string]*OperationSummary),
			DriftSummary: DriftSummary{
				DriftByAction:    make(map[governance.ActionType]int),
				DriftByOperation: make(map[string]int),
				DriftByResource:  make(map[string]int),
				ChangedResources: make([]string, 0),
				FailedResources:  make([]string, 0),
			},
		}
	}

	dst.ScannedProjects += src.ScannedProjects
	dst.ScannedGroups += src.ScannedGroups
	dst.TotalScanned += src.TotalScanned
	dst.TargetedProjects += src.TargetedProjects
	dst.TargetedGroups += src.TargetedGroups
	dst.TotalTargeted += src.TotalTargeted
	dst.TotalChanged += src.TotalChanged
	dst.TotalUnchanged += src.TotalUnchanged
	dst.TotalFailed += src.TotalFailed
	dst.TotalSkipped += src.TotalSkipped
	dst.TotalScannedGroups += src.TotalScannedGroups
	dst.TotalMatchedGroups += src.TotalMatchedGroups
	dst.TotalScannedProjects += src.TotalScannedProjects
	dst.TotalMatchedProjects += src.TotalMatchedProjects
	dst.TotalAppliedOperations += src.TotalAppliedOperations
	dst.TotalSkippedOperations += src.TotalSkippedOperations
	dst.TotalFailedOperations += src.TotalFailedOperations

	for name, op := range src.OperationCounts {
		if op == nil {
			continue
		}
		cur, ok := dst.OperationCounts[name]
		if !ok {
			cp := *op
			dst.OperationCounts[name] = &cp
			continue
		}
		cur.Total += op.Total
		cur.Created += op.Created
		cur.Updated += op.Updated
		cur.Deleted += op.Deleted
		cur.Noop += op.Noop
		cur.Audit += op.Audit
		cur.Skipped += op.Skipped
		cur.Failed += op.Failed
		cur.Duration += op.Duration
	}

	d, s := &dst.DriftSummary, src.DriftSummary
	d.TotalDriftFields += s.TotalDriftFields
	for k, v := range s.DriftByAction {
		d.DriftByAction[k] += v
	}
	for k, v := range s.DriftByOperation {
		d.DriftByOperation[k] += v
	}
	for k, v := range s.DriftByResource {
		d.DriftByResource[k] += v
	}
	d.ChangedResources = append(d.ChangedResources, s.ChangedResources...)
	d.FailedResources = append(d.FailedResources, s.FailedResources...)
	return dst
}
