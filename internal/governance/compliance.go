package governance

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ComplianceReconciler reconciles project compliance framework labels.
type ComplianceReconciler struct{}

// NewComplianceReconciler creates a new ComplianceReconciler instance.
func NewComplianceReconciler() *ComplianceReconciler {
	return &ComplianceReconciler{}
}

// NewComplianceOperation creates a ComplianceReconciler.
func NewComplianceOperation() *ComplianceReconciler {
	return NewComplianceReconciler()
}

// Name returns the operation identifier.
func (r *ComplianceReconciler) Name() string {
	return "compliance"
}

// Order returns the execution order sequence (80).
func (r *ComplianceReconciler) Order() int {
	return 80
}

// Plan calculates compliance framework drift on the target project.
func (r *ComplianceReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.Compliance == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	diffs, err := r.calculateComplianceDiffs(ctx, client, cfg.Policies.Compliance, project)
	if err != nil {
		return nil, err
	}

	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	action := diffs[0].Action
	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, action, diffs), nil
}

// Apply applies compliance framework assignments/removals.
func (r *ComplianceReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.Compliance == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	plan, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	for _, d := range plan.Diffs {
		switch d.Action {
		case ActionCreate, ActionUpdate:
			frameworkID := r.resolveDesiredFrameworkID(cfg.Policies.Compliance)
			if frameworkID == "" {
				applyErr := fmt.Errorf("compliance framework ID or name is required")
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, d.Action, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
			err := client.Compliance().SetProjectComplianceFramework(ctx, project.ID, frameworkID)
			if err != nil {
				applyErr := fmt.Errorf("failed to set compliance framework on project %d: %w", project.ID, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, d.Action, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		case ActionDelete:
			err := client.Compliance().RemoveProjectComplianceFramework(ctx, project.ID, "")
			if err != nil {
				applyErr := fmt.Errorf("failed to remove compliance framework from project %d: %w", project.ID, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, d.Action, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		}
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, plan.Action, StatusSuccess, plan.Diffs, nil, start), nil
}

// PlanGroup returns ActionSkipped as compliance framework labels apply to projects.
func (r *ComplianceReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Compliance framework policy is not applicable to groups"), nil
}

// ApplyGroup returns ActionSkipped as compliance framework labels apply to projects.
func (r *ComplianceReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Compliance framework policy is not applicable to groups"), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *ComplianceReconciler) calculateComplianceDiffs(ctx context.Context, client gitlab.GitLabClient, desired *config.ComplianceConfig, project *gogitlab.Project) ([]Diff, error) {
	var diffs []Diff

	currentFrameworks, err := client.Compliance().GetProjectComplianceFrameworks(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get compliance frameworks for project %d: %w", project.ID, err)
	}

	var currentName, currentID string
	if len(currentFrameworks) > 0 {
		currentName = currentFrameworks[0].Name
		currentID = currentFrameworks[0].ID
	}

	desiredName := desired.FrameworkName
	desiredID := r.resolveDesiredFrameworkID(desired)

	// Prune case: project has framework, but desired wants it cleared
	if desired.Prune != nil && *desired.Prune && (desiredName == "" && desired.FrameworkID == nil) {
		if currentID != "" || currentName != "" {
			builder := NewDiffBuilder()
			builder.AddField("compliance_framework", currentName, nil, ActionDelete)
			builder.SetDetails(fmt.Sprintf("Pruning compliance framework '%s'", currentName))
			diffs = append(diffs, builder.Build("compliance_framework", ActionDelete))
		}
		return diffs, nil
	}

	if desiredName == "" && desiredID == "" {
		return diffs, nil
	}

	// Compare current vs desired
	isMatch := false
	if desiredID != "" && currentID == desiredID {
		isMatch = true
	} else if desiredName != "" && strings.EqualFold(currentName, desiredName) {
		isMatch = true
	}

	if !isMatch {
		action := ActionCreate
		if currentName != "" || currentID != "" {
			action = ActionUpdate
		}
		builder := NewDiffBuilder()
		builder.AddField("compliance_framework", currentName, desiredName, action)
		builder.SetDetails(fmt.Sprintf("Assigning compliance framework '%s' (ID: %s)", desiredName, desiredID))
		diffs = append(diffs, builder.Build("compliance_framework", action))
	}

	return diffs, nil
}

func (r *ComplianceReconciler) resolveDesiredFrameworkID(cfg *config.ComplianceConfig) string {
	if cfg.FrameworkID != nil {
		return strconv.Itoa(*cfg.FrameworkID)
	}
	return cfg.FrameworkName
}
