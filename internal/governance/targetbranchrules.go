package governance

import (
	"context"
	"fmt"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// TargetBranchRulesReconciler implements GovernanceOperation for MR target branch rules.
type TargetBranchRulesReconciler struct{}

// NewTargetBranchRulesReconciler instantiates a new TargetBranchRulesReconciler.
func NewTargetBranchRulesReconciler() *TargetBranchRulesReconciler {
	return &TargetBranchRulesReconciler{}
}

// NewTargetBranchRulesOperation creates a new TargetBranchRulesReconciler instance.
func NewTargetBranchRulesOperation() *TargetBranchRulesReconciler {
	return NewTargetBranchRulesReconciler()
}

// Name returns the canonical operation identifier.
func (r *TargetBranchRulesReconciler) Name() string {
	return "target_branch_rules"
}

// Order returns the execution order sequence (45).
func (r *TargetBranchRulesReconciler) Order() int {
	return 45
}

// Plan evaluates project target branch rules against policy config without state mutation.
func (r *TargetBranchRulesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.TargetBranchRules == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveRules, err := client.TargetBranchRules().GetTargetBranchRules(ctx, project.PathWithNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch target branch rules for project %s: %w", project.PathWithNamespace, err)
	}

	diffs, overallAction := r.calculateDiffs(liveRules, cfg.Policies.TargetBranchRules)
	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, diffs), nil
}

// Apply enforces project target branch rules policy via GraphQL mutations.
func (r *TargetBranchRulesReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.TargetBranchRules == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveRules, err := client.TargetBranchRules().GetTargetBranchRules(ctx, project.PathWithNamespace)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	diffs, overallAction := r.calculateDiffs(liveRules, cfg.Policies.TargetBranchRules)
	if len(diffs) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveMap := make(map[string]gitlab.TargetBranchRule)
	for _, lr := range liveRules {
		liveMap[lr.Name] = lr
	}

	desiredMap := make(map[string]config.TargetBranchRuleConfig)
	for _, dr := range cfg.Policies.TargetBranchRules.Rules {
		desiredMap[dr.Name] = dr
	}

	prune := cfg.Policies.TargetBranchRules.Prune != nil && *cfg.Policies.TargetBranchRules.Prune

	// 1. Prune unmanaged rules if requested
	if prune {
		for name, liveRule := range liveMap {
			if _, exists := desiredMap[name]; !exists {
				if destroyErr := client.TargetBranchRules().DestroyTargetBranchRule(ctx, liveRule.ID); destroyErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to destroy target branch rule %s (%s): %w", name, liveRule.ID, destroyErr), start), destroyErr
				}
			}
		}
	}

	// 2. Process desired rules (Create / Update)
	for _, dr := range cfg.Policies.TargetBranchRules.Rules {
		liveRule, exists := liveMap[dr.Name]
		if !exists {
			_, createErr := client.TargetBranchRules().CreateTargetBranchRule(ctx, project.ID, dr.Name, dr.TargetBranch)
			if createErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create target branch rule %s -> %s: %w", dr.Name, dr.TargetBranch, createErr), start), createErr
			}
		} else if liveRule.TargetBranch != dr.TargetBranch {
			// Update: Destroy existing rule then create new rule with updated target branch
			if destroyErr := client.TargetBranchRules().DestroyTargetBranchRule(ctx, liveRule.ID); destroyErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to update (destroy) target branch rule %s: %w", dr.Name, destroyErr), start), destroyErr
			}
			_, createErr := client.TargetBranchRules().CreateTargetBranchRule(ctx, project.ID, dr.Name, dr.TargetBranch)
			if createErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to update (re-create) target branch rule %s -> %s: %w", dr.Name, dr.TargetBranch, createErr), start), createErr
			}
		}
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusSuccess, diffs, nil, start), nil
}

// PlanGroup target branch rules on group is a clean skipped operation.
func (r *TargetBranchRulesReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Target branch rules are not applicable to groups"), nil
}

// ApplyGroup target branch rules on group is a clean skipped operation.
func (r *TargetBranchRulesReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Target branch rules are not applicable to groups"), nil
}

func (r *TargetBranchRulesReconciler) calculateDiffs(liveRules []gitlab.TargetBranchRule, desiredCfg *config.TargetBranchRulesConfig) ([]Diff, ActionType) {
	var diffs []Diff
	overallAction := ActionNoop

	liveMap := make(map[string]gitlab.TargetBranchRule)
	for _, lr := range liveRules {
		liveMap[lr.Name] = lr
	}

	desiredMap := make(map[string]config.TargetBranchRuleConfig)
	for _, dr := range desiredCfg.Rules {
		desiredMap[dr.Name] = dr
	}

	// 1. Process desired rules
	for _, dr := range desiredCfg.Rules {
		liveRule, exists := liveMap[dr.Name]
		if !exists {
			builder := NewDiffBuilder()
			builder.AddField("target_branch", nil, dr.TargetBranch, ActionCreate)
			diffs = append(diffs, builder.Build(fmt.Sprintf("target_branch_rule:%s", dr.Name), ActionCreate))
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		} else if liveRule.TargetBranch != dr.TargetBranch {
			builder := NewDiffBuilder()
			builder.AddField("target_branch", liveRule.TargetBranch, dr.TargetBranch, ActionUpdate)
			diffs = append(diffs, builder.Build(fmt.Sprintf("target_branch_rule:%s", dr.Name), ActionUpdate))
			if overallAction == ActionNoop {
				overallAction = ActionUpdate
			}
		}
	}

	// 2. Process unmanaged rules if prune is enabled
	prune := desiredCfg.Prune != nil && *desiredCfg.Prune
	if prune {
		for name, liveRule := range liveMap {
			if _, exists := desiredMap[name]; !exists {
				builder := NewDiffBuilder()
				builder.AddField("target_branch", liveRule.TargetBranch, nil, ActionDelete)
				diffs = append(diffs, builder.Build(fmt.Sprintf("target_branch_rule:%s", name), ActionDelete))
				if overallAction == ActionNoop {
					overallAction = ActionDelete
				}
			}
		}
	}

	return diffs, overallAction
}
