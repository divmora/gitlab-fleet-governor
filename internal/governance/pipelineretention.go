package governance

import (
	"context"
	"fmt"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// PipelineRetentionReconciler reconciles automatic pipeline retention duration
// by translating high-level retention_days into GitLab's native ci_delete_pipelines_in_seconds.
type PipelineRetentionReconciler struct{}

// NewPipelineRetentionReconciler initializes a PipelineRetentionReconciler.
func NewPipelineRetentionReconciler() *PipelineRetentionReconciler {
	return &PipelineRetentionReconciler{}
}

// NewPipelineRetentionOperation creates a PipelineRetentionReconciler.
func NewPipelineRetentionOperation() *PipelineRetentionReconciler {
	return NewPipelineRetentionReconciler()
}

// Name returns the operation identifier.
func (r *PipelineRetentionReconciler) Name() string {
	return "pipeline_retention"
}

// Order returns the execution order sequence (50).
func (r *PipelineRetentionReconciler) Order() int {
	return 50
}

// Plan computes the diff between desired pipeline retention days and current project ci_delete_pipelines_in_seconds.
func (r *PipelineRetentionReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.PipelineRetention == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	currentSeconds, _, err := client.Projects().GetProjectPipelineRetention(project.ID, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch project %d pipeline retention: %w", project.ID, err)
	}

	retentionCfg := cfg.Policies.PipelineRetention
	desiredSeconds := retentionCfg.Seconds() // RetentionDays * 86400

	if currentSeconds == desiredSeconds {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	builder := NewDiffBuilder()
	builder.AddField("ci_delete_pipelines_in_seconds", currentSeconds, desiredSeconds, ActionUpdate)
	builder.AddField("retention_days", currentSeconds/86400, retentionCfg.RetentionDays, ActionUpdate)
	builder.SetDetails(fmt.Sprintf("Update pipeline retention from %d days (%d sec) to %d days (%d sec)",
		currentSeconds/86400, currentSeconds,
		retentionCfg.RetentionDays, desiredSeconds))

	diff := builder.Build("pipeline_retention", ActionUpdate)
	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, []Diff{diff}), nil
}

// Apply executes the pipeline retention update on the GitLab project.
func (r *PipelineRetentionReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.PipelineRetention == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	plan, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	desiredSeconds := cfg.Policies.PipelineRetention.Seconds()
	_, err = client.Projects().SetProjectPipelineRetention(project.ID, desiredSeconds, gogitlab.WithContext(ctx))
	if err != nil {
		applyErr := fmt.Errorf("failed to update pipeline retention on project %d: %w", project.ID, err)
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, plan.Diffs, nil, start), nil
}

// PlanGroup returns ActionSkipped as pipeline retention is project-scoped.
func (r *PipelineRetentionReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Pipeline retention is not applicable to groups"), nil
}

// ApplyGroup returns ActionSkipped as pipeline retention is project-scoped.
func (r *PipelineRetentionReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Pipeline retention is not applicable to groups"), nil
}
