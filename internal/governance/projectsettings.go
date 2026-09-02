package governance

import (
	"context"
	"fmt"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ProjectSettingsReconciler reconciles project-level repository, workflow, and artifact settings.
type ProjectSettingsReconciler struct{}

// NewProjectSettingsReconciler instantiates a new ProjectSettingsReconciler.
func NewProjectSettingsReconciler() *ProjectSettingsReconciler {
	return &ProjectSettingsReconciler{}
}

// NewProjectSettingsOperation creates a ProjectSettingsReconciler.
func NewProjectSettingsOperation() *ProjectSettingsReconciler {
	return NewProjectSettingsReconciler()
}

// Name returns the operation identifier.
func (r *ProjectSettingsReconciler) Name() string {
	return "project_settings"
}

// Order returns the execution order sequence (40).
func (r *ProjectSettingsReconciler) Order() int {
	return 40
}

// Plan calculates the diff between desired project settings and live GitLab project configuration.
func (r *ProjectSettingsReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.ProjectSettings == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveProj, _, err := client.Projects().GetProject(project.ID, nil, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get project %d details: %w", project.ID, err)
	}
	if liveProj == nil {
		liveProj = project
	}

	diff := r.buildProjectSettingsDiff(liveProj, cfg.Policies.ProjectSettings)
	if !diff.HasChanges() {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, []Diff{diff}), nil
}

// Apply executes planned project settings modifications via EditProject.
func (r *ProjectSettingsReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.ProjectSettings == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	plan, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	settingCfg := cfg.Policies.ProjectSettings
	editOpts := r.toEditProjectOptions(settingCfg)

	_, _, err = client.Projects().EditProject(project.ID, editOpts, gogitlab.WithContext(ctx))
	if err != nil {
		applyErr := fmt.Errorf("failed to update project settings for project %d: %w", project.ID, err)
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, plan.Diffs, nil, start), nil
}

// PlanGroup returns ActionSkipped as project settings are project-scoped.
func (r *ProjectSettingsReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Project settings are not applicable to groups"), nil
}

// ApplyGroup returns ActionSkipped as project settings are project-scoped.
func (r *ProjectSettingsReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Project settings are not applicable to groups"), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *ProjectSettingsReconciler) buildProjectSettingsDiff(live *gogitlab.Project, desired *config.ProjectSettingsConfig) Diff {
	builder := NewDiffBuilder()

	if desired.DefaultBranch != "" && live.DefaultBranch != desired.DefaultBranch {
		builder.AddField("default_branch", live.DefaultBranch, desired.DefaultBranch, ActionUpdate)
	}

	if desired.SquashOption != "" && string(live.SquashOption) != desired.SquashOption {
		builder.AddField("squash_option", string(live.SquashOption), desired.SquashOption, ActionUpdate)
	}

	if desired.MergeMethod != "" && string(live.MergeMethod) != desired.MergeMethod {
		builder.AddField("merge_method", string(live.MergeMethod), desired.MergeMethod, ActionUpdate)
	}

	builder.Add(CompareBoolPtr("only_allow_merge_if_pipeline_succeeds", live.OnlyAllowMergeIfPipelineSucceeds, desired.OnlyAllowMergeIfPipelineSucceeds))
	builder.Add(CompareBoolPtr("allow_merge_on_skipped_pipeline", live.AllowMergeOnSkippedPipeline, desired.AllowMergeOnSkippedPipeline))
	builder.Add(CompareBoolPtr("only_allow_merge_if_all_discussions_are_resolved", live.OnlyAllowMergeIfAllDiscussionsAreResolved, desired.OnlyAllowMergeIfAllDiscussionsAreResolved))
	builder.Add(CompareBoolPtr("remove_source_branch_after_merge", live.RemoveSourceBranchAfterMerge, desired.RemoveSourceBranchAfterMerge))
	builder.Add(CompareBoolPtr("keep_latest_artifact", live.KeepLatestArtifact, desired.KeepLatestArtifact))
	builder.Add(CompareBoolPtr("printing_merge_request_link_enabled", live.PrintingMergeRequestLinkEnabled, desired.PrintingMergeRequestLinkEnabled))

	if desired.AutoCancelPendingPipelines != "" && live.AutoCancelPendingPipelines != desired.AutoCancelPendingPipelines {
		builder.AddField("auto_cancel_pending_pipelines", live.AutoCancelPendingPipelines, desired.AutoCancelPendingPipelines, ActionUpdate)
	}

	builder.Add(CompareBoolPtr("auto_devops_enabled", live.AutoDevopsEnabled, desired.AutoDevopsEnabled))

	return builder.Build("project_settings", ActionUpdate)
}

func (r *ProjectSettingsReconciler) toEditProjectOptions(cfg *config.ProjectSettingsConfig) *gogitlab.EditProjectOptions {
	editOpts := &gogitlab.EditProjectOptions{}

	if cfg.DefaultBranch != "" {
		editOpts.DefaultBranch = gogitlab.Ptr(cfg.DefaultBranch)
	}
	if cfg.SquashOption != "" {
		editOpts.SquashOption = gogitlab.Ptr(gogitlab.SquashOptionValue(cfg.SquashOption))
	}
	if cfg.MergeMethod != "" {
		editOpts.MergeMethod = gogitlab.Ptr(gogitlab.MergeMethodValue(cfg.MergeMethod))
	}
	if cfg.OnlyAllowMergeIfPipelineSucceeds != nil {
		editOpts.OnlyAllowMergeIfPipelineSucceeds = cfg.OnlyAllowMergeIfPipelineSucceeds
	}
	if cfg.AllowMergeOnSkippedPipeline != nil {
		editOpts.AllowMergeOnSkippedPipeline = cfg.AllowMergeOnSkippedPipeline
	}
	if cfg.OnlyAllowMergeIfAllDiscussionsAreResolved != nil {
		editOpts.OnlyAllowMergeIfAllDiscussionsAreResolved = cfg.OnlyAllowMergeIfAllDiscussionsAreResolved
	}
	if cfg.RemoveSourceBranchAfterMerge != nil {
		editOpts.RemoveSourceBranchAfterMerge = cfg.RemoveSourceBranchAfterMerge
	}
	if cfg.KeepLatestArtifact != nil {
		editOpts.KeepLatestArtifact = cfg.KeepLatestArtifact
	}
	if cfg.PrintingMergeRequestLinkEnabled != nil {
		editOpts.PrintingMergeRequestLinkEnabled = cfg.PrintingMergeRequestLinkEnabled
	}
	if cfg.AutoCancelPendingPipelines != "" {
		editOpts.AutoCancelPendingPipelines = gogitlab.Ptr(cfg.AutoCancelPendingPipelines)
	}
	if cfg.AutoDevopsEnabled != nil {
		editOpts.AutoDevopsEnabled = cfg.AutoDevopsEnabled
	}

	return editOpts
}
