package governance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// VariablesReconciler reconciles CI/CD variables and secrets across projects and groups,
// supporting composite keys (Key + EnvironmentScope), masked/protected/raw flags, and drift pruning.
type VariablesReconciler struct {
	pruneUnmanaged bool
}

// NewVariablesReconciler initializes a VariablesReconciler.
func NewVariablesReconciler(pruneUnmanaged ...bool) *VariablesReconciler {
	prune := false
	if len(pruneUnmanaged) > 0 {
		prune = pruneUnmanaged[0]
	}
	return &VariablesReconciler{pruneUnmanaged: prune}
}

// NewVariablesOperation creates a VariablesReconciler.
func NewVariablesOperation(pruneUnmanaged ...bool) *VariablesReconciler {
	return NewVariablesReconciler(pruneUnmanaged...)
}

// Name returns the operation identifier.
func (r *VariablesReconciler) Name() string {
	return "variables"
}

// Order returns the execution order sequence (60).
func (r *VariablesReconciler) Order() int {
	return 60
}

// varCompositeKey returns "KEY::SCOPE" normalized with default "*" scope.
func varCompositeKey(key, scope string) string {
	if scope == "" {
		scope = "*"
	}
	return fmt.Sprintf("%s::%s", strings.TrimSpace(key), strings.TrimSpace(scope))
}

// isSensitiveVariable checks if a variable name or flag indicates confidential secret data.
func isSensitiveVariable(key string, masked bool) bool {
	if masked {
		return true
	}
	kUpper := strings.ToUpper(key)
	return strings.Contains(kUpper, "SECRET") ||
		strings.Contains(kUpper, "TOKEN") ||
		strings.Contains(kUpper, "PASSWORD") ||
		strings.Contains(kUpper, "KEY") ||
		strings.Contains(kUpper, "AUTH") ||
		strings.Contains(kUpper, "PRIVATE")
}

// sanitizeValue masks secret values in human-readable diff logs.
func sanitizeValue(key, val string, masked bool) string {
	if isSensitiveVariable(key, masked) {
		return "******"
	}
	return val
}

// ============================================================================
// Project CI/CD Variables
// ============================================================================

// Plan computes the diff for project CI/CD variables.
func (r *VariablesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.Variables) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveVars, _, err := client.Variables().ListProjectVariables(project.ID, &gogitlab.ListProjectVariablesOptions{
		PerPage: 100,
	}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list project variables for project %d: %w", project.ID, err)
	}

	liveMap := make(map[string]*gogitlab.ProjectVariable)
	for _, lv := range liveVars {
		if lv != nil {
			liveMap[varCompositeKey(lv.Key, lv.EnvironmentScope)] = lv
		}
	}

	matchedKeys := make(map[string]bool)
	var allDiffs []Diff

	// 1. Evaluate declared variables
	for _, decl := range cfg.Policies.Variables {
		scope := decl.EnvironmentScope
		if scope == "" {
			scope = "*"
		}
		cKey := varCompositeKey(decl.Key, scope)
		existing, found := liveMap[cKey]

		if !found {
			// Variable needs to be created
			masked := decl.Masked != nil && *decl.Masked
			builder := NewDiffBuilder()
			builder.AddField("key", nil, decl.Key, ActionCreate)
			builder.AddField("value", nil, sanitizeValue(decl.Key, decl.Value, masked), ActionCreate)
			builder.AddField("environment_scope", nil, scope, ActionCreate)
			if decl.VariableType != "" {
				builder.AddField("variable_type", nil, decl.VariableType, ActionCreate)
			}
			if decl.Masked != nil {
				builder.AddField("masked", nil, *decl.Masked, ActionCreate)
			}
			if decl.Protected != nil {
				builder.AddField("protected", nil, *decl.Protected, ActionCreate)
			}
			if decl.Raw != nil {
				builder.AddField("raw", nil, *decl.Raw, ActionCreate)
			}
			builder.SetDetails(fmt.Sprintf("Create variable '%s' (scope: %s)", decl.Key, scope))
			allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("variable:%s", cKey), ActionCreate))
		} else {
			matchedKeys[cKey] = true
			varDiff := r.diffProjectVariable(decl, existing)
			if varDiff.HasChanges() {
				allDiffs = append(allDiffs, varDiff)
			}
		}
	}

	// 2. Evaluate unmanaged drift pruning
	if r.pruneUnmanaged {
		for _, lv := range liveVars {
			if lv == nil {
				continue
			}
			cKey := varCompositeKey(lv.Key, lv.EnvironmentScope)
			if !matchedKeys[cKey] {
				builder := NewDiffBuilder()
				builder.AddField("key", lv.Key, nil, ActionDelete)
				builder.AddField("environment_scope", lv.EnvironmentScope, nil, ActionDelete)
				builder.SetDetails(fmt.Sprintf("Prune unmanaged variable '%s' (scope: %s)", lv.Key, lv.EnvironmentScope))
				allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("variable:%s", cKey), ActionDelete))
			}
		}
	}

	if len(allDiffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, allDiffs), nil
}

// Apply executes planned project CI/CD variable mutations.
func (r *VariablesReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || len(cfg.Policies.Variables) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	plan, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveVars, _, err := client.Variables().ListProjectVariables(project.ID, &gogitlab.ListProjectVariablesOptions{
		PerPage: 100,
	}, gogitlab.WithContext(ctx))
	if err != nil {
		applyErr := fmt.Errorf("failed to list project variables: %w", err)
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
	}

	liveMap := make(map[string]*gogitlab.ProjectVariable)
	for _, lv := range liveVars {
		if lv != nil {
			liveMap[varCompositeKey(lv.Key, lv.EnvironmentScope)] = lv
		}
	}

	matchedKeys := make(map[string]bool)

	for _, decl := range cfg.Policies.Variables {
		scope := decl.EnvironmentScope
		if scope == "" {
			scope = "*"
		}
		cKey := varCompositeKey(decl.Key, scope)
		_, found := liveMap[cKey]

		if !found {
			createOpts := &gogitlab.CreateProjectVariableOptions{
				Key:              gogitlab.Ptr(decl.Key),
				Value:            gogitlab.Ptr(decl.Value),
				EnvironmentScope: gogitlab.Ptr(scope),
			}
			if decl.VariableType != "" {
				createOpts.VariableType = gogitlab.Ptr(gogitlab.VariableTypeValue(decl.VariableType))
			}
			if decl.Protected != nil {
				createOpts.Protected = decl.Protected
			}
			if decl.Masked != nil {
				createOpts.Masked = decl.Masked
			}
			if decl.Raw != nil {
				createOpts.Raw = decl.Raw
			}
			if decl.Description != "" {
				createOpts.Description = gogitlab.Ptr(decl.Description)
			}

			_, _, err := client.Variables().CreateProjectVariable(project.ID, createOpts, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to create project variable '%s': %w", cKey, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		} else {
			matchedKeys[cKey] = true
			updateOpts := &gogitlab.UpdateProjectVariableOptions{
				Value: gogitlab.Ptr(decl.Value),
				Filter: &gogitlab.VariableFilter{
					EnvironmentScope: scope,
				},
			}
			if decl.VariableType != "" {
				updateOpts.VariableType = gogitlab.Ptr(gogitlab.VariableTypeValue(decl.VariableType))
			}
			if decl.Protected != nil {
				updateOpts.Protected = decl.Protected
			}
			if decl.Masked != nil {
				updateOpts.Masked = decl.Masked
			}
			if decl.Raw != nil {
				updateOpts.Raw = decl.Raw
			}
			if decl.Description != "" {
				updateOpts.Description = gogitlab.Ptr(decl.Description)
			}

			_, _, err := client.Variables().UpdateProjectVariable(project.ID, decl.Key, updateOpts, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to update project variable '%s': %w", cKey, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		}
	}

	if r.pruneUnmanaged {
		for _, lv := range liveVars {
			if lv == nil {
				continue
			}
			cKey := varCompositeKey(lv.Key, lv.EnvironmentScope)
			if !matchedKeys[cKey] {
				removeOpts := &gogitlab.RemoveProjectVariableOptions{
					Filter: &gogitlab.VariableFilter{
						EnvironmentScope: lv.EnvironmentScope,
					},
				}
				_, err := client.Variables().RemoveProjectVariable(project.ID, lv.Key, removeOpts, gogitlab.WithContext(ctx))
				if err != nil {
					applyErr := fmt.Errorf("failed to remove unmanaged project variable '%s': %w", cKey, err)
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionDelete, StatusFailed, plan.Diffs, applyErr, start), applyErr
				}
			}
		}
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, plan.Diffs, nil, start), nil
}

// ============================================================================
// Group CI/CD Variables
// ============================================================================

// PlanGroup computes the diff for group CI/CD variables.
func (r *VariablesReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.Variables) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	liveVars, _, err := client.Variables().ListGroupVariables(group.ID, &gogitlab.ListGroupVariablesOptions{
		PerPage: 100,
	}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list group variables for group %d: %w", group.ID, err)
	}

	liveMap := make(map[string]*gogitlab.GroupVariable)
	for _, lv := range liveVars {
		if lv != nil {
			liveMap[varCompositeKey(lv.Key, lv.EnvironmentScope)] = lv
		}
	}

	matchedKeys := make(map[string]bool)
	var allDiffs []Diff

	for _, decl := range cfg.Policies.Variables {
		scope := decl.EnvironmentScope
		if scope == "" {
			scope = "*"
		}
		cKey := varCompositeKey(decl.Key, scope)
		existing, found := liveMap[cKey]

		if !found {
			masked := decl.Masked != nil && *decl.Masked
			builder := NewDiffBuilder()
			builder.AddField("key", nil, decl.Key, ActionCreate)
			builder.AddField("value", nil, sanitizeValue(decl.Key, decl.Value, masked), ActionCreate)
			builder.AddField("environment_scope", nil, scope, ActionCreate)
			if decl.VariableType != "" {
				builder.AddField("variable_type", nil, decl.VariableType, ActionCreate)
			}
			if decl.Masked != nil {
				builder.AddField("masked", nil, *decl.Masked, ActionCreate)
			}
			if decl.Protected != nil {
				builder.AddField("protected", nil, *decl.Protected, ActionCreate)
			}
			if decl.Raw != nil {
				builder.AddField("raw", nil, *decl.Raw, ActionCreate)
			}
			builder.SetDetails(fmt.Sprintf("Create group variable '%s' (scope: %s)", decl.Key, scope))
			allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("variable:%s", cKey), ActionCreate))
		} else {
			matchedKeys[cKey] = true
			varDiff := r.diffGroupVariable(decl, existing)
			if varDiff.HasChanges() {
				allDiffs = append(allDiffs, varDiff)
			}
		}
	}

	if r.pruneUnmanaged {
		for _, lv := range liveVars {
			if lv == nil {
				continue
			}
			cKey := varCompositeKey(lv.Key, lv.EnvironmentScope)
			if !matchedKeys[cKey] {
				builder := NewDiffBuilder()
				builder.AddField("key", lv.Key, nil, ActionDelete)
				builder.AddField("environment_scope", lv.EnvironmentScope, nil, ActionDelete)
				builder.SetDetails(fmt.Sprintf("Prune unmanaged group variable '%s' (scope: %s)", lv.Key, lv.EnvironmentScope))
				allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("variable:%s", cKey), ActionDelete))
			}
		}
	}

	if len(allDiffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, allDiffs), nil
}

// ApplyGroup executes planned group CI/CD variable mutations.
func (r *VariablesReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || len(cfg.Policies.Variables) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	plan, err := r.PlanGroup(ctx, client, group, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	liveVars, _, err := client.Variables().ListGroupVariables(group.ID, &gogitlab.ListGroupVariablesOptions{
		PerPage: 100,
	}, gogitlab.WithContext(ctx))
	if err != nil {
		applyErr := fmt.Errorf("failed to list group variables: %w", err)
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
	}

	liveMap := make(map[string]*gogitlab.GroupVariable)
	for _, lv := range liveVars {
		if lv != nil {
			liveMap[varCompositeKey(lv.Key, lv.EnvironmentScope)] = lv
		}
	}

	matchedKeys := make(map[string]bool)

	for _, decl := range cfg.Policies.Variables {
		scope := decl.EnvironmentScope
		if scope == "" {
			scope = "*"
		}
		cKey := varCompositeKey(decl.Key, scope)
		_, found := liveMap[cKey]

		if !found {
			createOpts := &gogitlab.CreateGroupVariableOptions{
				Key:              gogitlab.Ptr(decl.Key),
				Value:            gogitlab.Ptr(decl.Value),
				EnvironmentScope: gogitlab.Ptr(scope),
			}
			if decl.VariableType != "" {
				createOpts.VariableType = gogitlab.Ptr(gogitlab.VariableTypeValue(decl.VariableType))
			}
			if decl.Protected != nil {
				createOpts.Protected = decl.Protected
			}
			if decl.Masked != nil {
				createOpts.Masked = decl.Masked
			}
			if decl.Raw != nil {
				createOpts.Raw = decl.Raw
			}
			if decl.Description != "" {
				createOpts.Description = gogitlab.Ptr(decl.Description)
			}

			_, _, err := client.Variables().CreateGroupVariable(group.ID, createOpts, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to create group variable '%s': %w", cKey, err)
				return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionCreate, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		} else {
			matchedKeys[cKey] = true
			updateOpts := &gogitlab.UpdateGroupVariableOptions{
				Value:            gogitlab.Ptr(decl.Value),
				EnvironmentScope: gogitlab.Ptr(scope),
			}
			if decl.VariableType != "" {
				updateOpts.VariableType = gogitlab.Ptr(gogitlab.VariableTypeValue(decl.VariableType))
			}
			if decl.Protected != nil {
				updateOpts.Protected = decl.Protected
			}
			if decl.Masked != nil {
				updateOpts.Masked = decl.Masked
			}
			if decl.Raw != nil {
				updateOpts.Raw = decl.Raw
			}
			if decl.Description != "" {
				updateOpts.Description = gogitlab.Ptr(decl.Description)
			}

			_, _, err := client.Variables().UpdateGroupVariable(group.ID, decl.Key, updateOpts, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to update group variable '%s': %w", cKey, err)
				return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		}
	}

	if r.pruneUnmanaged {
		for _, lv := range liveVars {
			if lv == nil {
				continue
			}
			cKey := varCompositeKey(lv.Key, lv.EnvironmentScope)
			if !matchedKeys[cKey] {
				_, err := client.Variables().RemoveGroupVariable(group.ID, lv.Key, gogitlab.WithContext(ctx))
				if err != nil {
					applyErr := fmt.Errorf("failed to remove unmanaged group variable '%s': %w", cKey, err)
					return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionDelete, StatusFailed, plan.Diffs, applyErr, start), applyErr
				}
			}
		}
	}

	return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, StatusSuccess, plan.Diffs, nil, start), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *VariablesReconciler) diffProjectVariable(desired config.VariableConfig, live *gogitlab.ProjectVariable) Diff {
	builder := NewDiffBuilder()
	masked := (desired.Masked != nil && *desired.Masked) || live.Masked

	if live.Value != desired.Value {
		builder.AddField("value", sanitizeValue(live.Key, live.Value, masked), sanitizeValue(desired.Key, desired.Value, masked), ActionUpdate)
	}
	if desired.VariableType != "" && string(live.VariableType) != desired.VariableType {
		builder.AddField("variable_type", string(live.VariableType), desired.VariableType, ActionUpdate)
	}
	builder.Add(CompareBoolPtr("protected", live.Protected, desired.Protected))
	builder.Add(CompareBoolPtr("masked", live.Masked, desired.Masked))
	builder.Add(CompareBoolPtr("raw", live.Raw, desired.Raw))
	if desired.Description != "" && live.Description != desired.Description {
		builder.AddField("description", live.Description, desired.Description, ActionUpdate)
	}

	cKey := varCompositeKey(desired.Key, desired.EnvironmentScope)
	return builder.Build(fmt.Sprintf("variable:%s", cKey), ActionUpdate)
}

func (r *VariablesReconciler) diffGroupVariable(desired config.VariableConfig, live *gogitlab.GroupVariable) Diff {
	builder := NewDiffBuilder()
	masked := (desired.Masked != nil && *desired.Masked) || live.Masked

	if live.Value != desired.Value {
		builder.AddField("value", sanitizeValue(live.Key, live.Value, masked), sanitizeValue(desired.Key, desired.Value, masked), ActionUpdate)
	}
	if desired.VariableType != "" && string(live.VariableType) != desired.VariableType {
		builder.AddField("variable_type", string(live.VariableType), desired.VariableType, ActionUpdate)
	}
	builder.Add(CompareBoolPtr("protected", live.Protected, desired.Protected))
	builder.Add(CompareBoolPtr("masked", live.Masked, desired.Masked))
	builder.Add(CompareBoolPtr("raw", live.Raw, desired.Raw))
	if desired.Description != "" && live.Description != desired.Description {
		builder.AddField("description", live.Description, desired.Description, ActionUpdate)
	}

	cKey := varCompositeKey(desired.Key, desired.EnvironmentScope)
	return builder.Build(fmt.Sprintf("variable:%s", cKey), ActionUpdate)
}
