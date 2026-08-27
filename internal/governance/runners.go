package governance

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// RunnersReconciler reconciles project runner fleet settings and runner attributes.
type RunnersReconciler struct{}

// NewRunnersReconciler creates a new RunnersReconciler instance.
func NewRunnersReconciler() *RunnersReconciler {
	return &RunnersReconciler{}
}

// NewRunnersOperation creates a RunnersReconciler.
func NewRunnersOperation() *RunnersReconciler {
	return NewRunnersReconciler()
}

// Name returns the operation identifier.
func (r *RunnersReconciler) Name() string {
	return "runners"
}

// Order returns the execution order sequence (70).
func (r *RunnersReconciler) Order() int {
	return 70
}

// Plan evaluates project runner settings drift without making changes.
func (r *RunnersReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.Runners == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	diffs, err := r.calculateProjectDiffs(ctx, client, cfg.Policies.Runners, project)
	if err != nil {
		return nil, err
	}

	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, diffs), nil
}

// Apply applies runner settings changes to GitLab project.
func (r *RunnersReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.Runners == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	plan, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	desired := cfg.Policies.Runners
	projectEditNeeded := false
	editOpt := &gogitlab.EditProjectOptions{}

	if desired.SharedRunnersEnabled != nil {
		editOpt.SharedRunnersEnabled = desired.SharedRunnersEnabled
		projectEditNeeded = true
	}
	if desired.GroupRunnersEnabled != nil {
		editOpt.GroupRunnersEnabled = desired.GroupRunnersEnabled
		projectEditNeeded = true
	}

	if projectEditNeeded {
		_, _, err := client.Projects().EditProject(project.ID, editOpt, gogitlab.WithContext(ctx))
		if err != nil {
			applyErr := fmt.Errorf("failed to update project runner settings for %d: %w", project.ID, err)
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
		}
	}

	if len(desired.Runners) > 0 {
		existingRunners, _, err := client.Runners().ListProjectRunners(project.ID, &gogitlab.ListProjectRunnersOptions{}, gogitlab.WithContext(ctx))
		if err != nil {
			applyErr := fmt.Errorf("failed to list runners for project %d: %w", project.ID, err)
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
		}

		for _, runnerCfg := range desired.Runners {
			targetRunner := r.findMatchingRunner(runnerCfg, existingRunners)
			if targetRunner == nil {
				continue
			}

			updateOpt := &gogitlab.UpdateRunnerDetailsOptions{}
			hasUpdate := false

			if runnerCfg.Description != "" {
				updateOpt.Description = &runnerCfg.Description
				hasUpdate = true
			}
			if runnerCfg.Paused != nil {
				updateOpt.Paused = runnerCfg.Paused
				hasUpdate = true
			}
			if runnerCfg.Locked != nil {
				updateOpt.Locked = runnerCfg.Locked
				hasUpdate = true
			}
			if runnerCfg.RunUntagged != nil {
				updateOpt.RunUntagged = runnerCfg.RunUntagged
				hasUpdate = true
			}
			if runnerCfg.AccessLevel != "" {
				updateOpt.AccessLevel = &runnerCfg.AccessLevel
				hasUpdate = true
			}
			if runnerCfg.MaximumTimeout != nil {
				updateOpt.MaximumTimeout = runnerCfg.MaximumTimeout
				hasUpdate = true
			}
			if len(runnerCfg.TagList) > 0 {
				tags := runnerCfg.TagList
				updateOpt.TagList = &tags
				hasUpdate = true
			}

			if hasUpdate {
				_, _, err := client.Runners().UpdateRunnerDetails(targetRunner.ID, updateOpt, gogitlab.WithContext(ctx))
				if err != nil {
					applyErr := fmt.Errorf("failed to update runner %d: %w", targetRunner.ID, err)
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
				}
			}
		}
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, plan.Diffs, nil, start), nil
}

// PlanGroup is a no-op as runner project policies apply to projects.
func (r *RunnersReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Runner project policy is not applicable to groups"), nil
}

// ApplyGroup is a no-op as runner project policies apply to projects.
func (r *RunnersReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Runner project policy is not applicable to groups"), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *RunnersReconciler) calculateProjectDiffs(ctx context.Context, client gitlab.GitLabClient, desired *config.RunnersConfig, project *gogitlab.Project) ([]Diff, error) {
	var allDiffs []Diff

	currentProj, _, err := client.Projects().GetProject(project.ID, nil, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch project %d: %w", project.ID, err)
	}
	if currentProj == nil {
		currentProj = project
	}

	builder := NewDiffBuilder()
	builder.Add(CompareBoolPtr("shared_runners_enabled", currentProj.SharedRunnersEnabled, desired.SharedRunnersEnabled))
	builder.Add(CompareBoolPtr("group_runners_enabled", currentProj.GroupRunnersEnabled, desired.GroupRunnersEnabled))

	if builder.HasChanges() {
		allDiffs = append(allDiffs, builder.Build("runners_settings", ActionUpdate))
	}

	if len(desired.Runners) > 0 {
		existingRunners, _, err := client.Runners().ListProjectRunners(project.ID, &gogitlab.ListProjectRunnersOptions{}, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list project runners for %d: %w", project.ID, err)
		}

		for _, runnerCfg := range desired.Runners {
			matched := r.findMatchingRunner(runnerCfg, existingRunners)
			if matched == nil {
				b := NewDiffBuilder()
				b.SetDetails("Runner assertion defined in policy not assigned to project")
				allDiffs = append(allDiffs, b.Build(fmt.Sprintf("runner:%d/%s", runnerCfg.ID, runnerCfg.Description), ActionCreate))
				continue
			}

			details, _, err := client.Runners().GetRunnerDetails(matched.ID, gogitlab.WithContext(ctx))
			if err != nil {
				return nil, fmt.Errorf("failed to fetch runner details %d: %w", matched.ID, err)
			}

			rBuilder := NewDiffBuilder()
			rBuilder.Add(CompareBoolPtr("paused", details.Paused, runnerCfg.Paused))
			rBuilder.Add(CompareBoolPtr("locked", details.Locked, runnerCfg.Locked))
			rBuilder.Add(CompareBoolPtr("run_untagged", details.RunUntagged, runnerCfg.RunUntagged))
			if runnerCfg.AccessLevel != "" && details.AccessLevel != runnerCfg.AccessLevel {
				rBuilder.AddField("access_level", details.AccessLevel, runnerCfg.AccessLevel, ActionUpdate)
			}
			rBuilder.Add(CompareIntPtr("maximum_timeout", details.MaximumTimeout, runnerCfg.MaximumTimeout))
			if len(runnerCfg.TagList) > 0 && !tagsEqual(runnerCfg.TagList, details.TagList) {
				rBuilder.AddField("tag_list", details.TagList, runnerCfg.TagList, ActionUpdate)
			}

			if rBuilder.HasChanges() {
				allDiffs = append(allDiffs, rBuilder.Build(fmt.Sprintf("runner:%d", matched.ID), ActionUpdate))
			}
		}
	}

	return allDiffs, nil
}

func (r *RunnersReconciler) findMatchingRunner(cfg config.RunnerConfig, runners []*gogitlab.Runner) *gogitlab.Runner {
	for _, runner := range runners {
		if cfg.ID > 0 && runner.ID == cfg.ID {
			return runner
		}
		if cfg.Description != "" && runner.Description == cfg.Description {
			return runner
		}
	}
	return nil
}

func tagsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)
	sort.Strings(sortedA)
	sort.Strings(sortedB)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}
