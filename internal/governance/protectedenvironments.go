package governance

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// ProtectedEnvironmentsReconciler implements GovernanceOperation for protected environments.
type ProtectedEnvironmentsReconciler struct{}

// NewProtectedEnvironmentsReconciler instantiates a new protected environments reconciler.
func NewProtectedEnvironmentsReconciler() *ProtectedEnvironmentsReconciler {
	return &ProtectedEnvironmentsReconciler{}
}

// NewProtectedEnvironmentsOperation creates a new protected environments operation instance.
func NewProtectedEnvironmentsOperation() *ProtectedEnvironmentsReconciler {
	return NewProtectedEnvironmentsReconciler()
}

// Name returns the canonical operation identifier.
func (r *ProtectedEnvironmentsReconciler) Name() string {
	return "protected_environments"
}

// Order returns the execution order sequence (25).
func (r *ProtectedEnvironmentsReconciler) Order() int {
	return 25
}

// Plan evaluates project protected environments against policy config (dry-run).
func (r *ProtectedEnvironmentsReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.ProtectedEnvironments) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveEnvs, err := r.fetchAllLiveProtectedEnvironments(ctx, client, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list protected environments for project %d: %w", project.ID, err)
	}

	liveMap := make(map[string]*gogitlab.ProtectedEnvironment)
	for _, env := range liveEnvs {
		if env != nil {
			liveMap[env.Name] = env
		}
	}

	var allDiffs []Diff
	overallAction := ActionNoop

	for _, rule := range cfg.Policies.ProtectedEnvironments {
		live, found := liveMap[rule.Name]
		if !found {
			// Protection missing -> CREATE
			diff := r.buildCreateDiff(&rule)
			allDiffs = append(allDiffs, diff)
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		} else {
			// Protection exists -> compare attributes
			diff := r.buildUpdateDiff(live, &rule)
			if diff.HasChanges() {
				allDiffs = append(allDiffs, diff)
				if overallAction == ActionNoop {
					overallAction = ActionUpdate
				}
			}
		}
	}

	if len(allDiffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, allDiffs), nil
}

// Apply executes protected environments policy enforcement (live mutation).
func (r *ProtectedEnvironmentsReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || len(cfg.Policies.ProtectedEnvironments) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveEnvs, err := r.fetchAllLiveProtectedEnvironments(ctx, client, project.ID)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	liveMap := make(map[string]*gogitlab.ProtectedEnvironment)
	for _, env := range liveEnvs {
		if env != nil {
			liveMap[env.Name] = env
		}
	}

	var appliedDiffs []Diff
	overallAction := ActionNoop

	for _, rule := range cfg.Policies.ProtectedEnvironments {
		live, found := liveMap[rule.Name]
		if !found {
			// 1. Missing: Create environment protection
			diff := r.buildCreateDiff(&rule)
			protectOpt := r.toProtectOptions(&rule)
			_, _, createErr := client.ProtectedEnvironments().ProtectRepositoryEnvironments(project.ID, protectOpt, gogitlab.WithContext(ctx))
			if createErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, StatusFailed, append(appliedDiffs, diff), createErr, start), createErr
			}
			appliedDiffs = append(appliedDiffs, diff)
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		} else {
			// 2. Exists: Evaluate differences
			diff := r.buildUpdateDiff(live, &rule)
			if !diff.HasChanges() {
				continue
			}

			// Drift detected: Recreate protection atomically (Unprotect -> Protect)
			_, _ = client.ProtectedEnvironments().UnprotectEnvironment(project.ID, rule.Name, gogitlab.WithContext(ctx))

			protectOpt := r.toProtectOptions(&rule)
			_, _, reprotectErr := client.ProtectedEnvironments().ProtectRepositoryEnvironments(project.ID, protectOpt, gogitlab.WithContext(ctx))
			if reprotectErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, append(appliedDiffs, diff), reprotectErr, start), reprotectErr
			}

			appliedDiffs = append(appliedDiffs, diff)
			if overallAction == ActionNoop {
				overallAction = ActionUpdate
			}
		}
	}

	if len(appliedDiffs) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusSuccess, appliedDiffs, nil, start), nil
}

// PlanGroup protected environments on group is a clean no-op.
func (r *ProtectedEnvironmentsReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Protected environments are not applicable to groups"), nil
}

// ApplyGroup protected environments on group is a clean no-op.
func (r *ProtectedEnvironmentsReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Protected environments are not applicable to groups"), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *ProtectedEnvironmentsReconciler) fetchAllLiveProtectedEnvironments(ctx context.Context, client gitlab.GitLabClient, projectID int) ([]*gogitlab.ProtectedEnvironment, error) {
	var all []*gogitlab.ProtectedEnvironment
	page := 1
	for {
		opts := &gogitlab.ListProtectedEnvironmentsOptions{
			Page:    page,
			PerPage: 100,
		}
		envs, resp, err := client.ProtectedEnvironments().ListProtectedEnvironments(projectID, opts, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		all = append(all, envs...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}

func (r *ProtectedEnvironmentsReconciler) toProtectOptions(rule *config.ProtectedEnvironmentRuleConfig) *gogitlab.ProtectRepositoryEnvironmentsOptions {
	opt := &gogitlab.ProtectRepositoryEnvironmentsOptions{
		Name: gogitlab.Ptr(rule.Name),
	}
	if rule.RequiredApprovalCount != nil {
		opt.RequiredApprovalCount = rule.RequiredApprovalCount
	}
	if len(rule.DeployAccessLevels) > 0 {
		var accessList []*gogitlab.EnvironmentAccessOptions
		for _, acc := range rule.DeployAccessLevels {
			entry := &gogitlab.EnvironmentAccessOptions{}
			if acc.AccessLevel > 0 {
				entry.AccessLevel = gogitlab.Ptr(gogitlab.AccessLevelValue(acc.AccessLevel))
			}
			if acc.UserID > 0 {
				entry.UserID = gogitlab.Ptr(acc.UserID)
			}
			if acc.GroupID > 0 {
				entry.GroupID = gogitlab.Ptr(acc.GroupID)
			}
			accessList = append(accessList, entry)
		}
		opt.DeployAccessLevels = &accessList
	}
	if len(rule.ApprovalRules) > 0 {
		var ruleList []*gogitlab.EnvironmentApprovalRuleOptions
		for _, ar := range rule.ApprovalRules {
			entry := &gogitlab.EnvironmentApprovalRuleOptions{}
			if ar.AccessLevel > 0 {
				entry.AccessLevel = gogitlab.Ptr(gogitlab.AccessLevelValue(ar.AccessLevel))
			}
			if ar.UserID > 0 {
				entry.UserID = gogitlab.Ptr(ar.UserID)
			}
			if ar.GroupID > 0 {
				entry.GroupID = gogitlab.Ptr(ar.GroupID)
			}
			if ar.RequiredApprovalCount != nil {
				entry.RequiredApprovalCount = ar.RequiredApprovalCount
			}
			ruleList = append(ruleList, entry)
		}
		opt.ApprovalRules = &ruleList
	}
	return opt
}

func (r *ProtectedEnvironmentsReconciler) buildCreateDiff(rule *config.ProtectedEnvironmentRuleConfig) Diff {
	builder := NewDiffBuilder()
	builder.AddField("name", nil, rule.Name, ActionCreate)
	if rule.RequiredApprovalCount != nil {
		builder.AddField("required_approval_count", nil, *rule.RequiredApprovalCount, ActionCreate)
	}
	if len(rule.DeployAccessLevels) > 0 {
		builder.AddField("deploy_access_levels", nil, formatConfigEnvAccess(rule.DeployAccessLevels), ActionCreate)
	}
	if len(rule.ApprovalRules) > 0 {
		builder.AddField("approval_rules", nil, formatConfigApprovalRules(rule.ApprovalRules), ActionCreate)
	}
	return builder.Build(fmt.Sprintf("protected_environment:%s", rule.Name), ActionCreate)
}

func (r *ProtectedEnvironmentsReconciler) buildUpdateDiff(live *gogitlab.ProtectedEnvironment, rule *config.ProtectedEnvironmentRuleConfig) Diff {
	builder := NewDiffBuilder()

	// Compare RequiredApprovalCount
	if rule.RequiredApprovalCount != nil && *rule.RequiredApprovalCount != live.RequiredApprovalCount {
		builder.AddField("required_approval_count", live.RequiredApprovalCount, *rule.RequiredApprovalCount, ActionUpdate)
	}

	// Compare DeployAccessLevels
	if len(rule.DeployAccessLevels) > 0 {
		if !equalEnvAccess(live.DeployAccessLevels, rule.DeployAccessLevels) {
			builder.AddField("deploy_access_levels", formatLiveEnvAccess(live.DeployAccessLevels), formatConfigEnvAccess(rule.DeployAccessLevels), ActionUpdate)
		}
	}

	// Compare ApprovalRules
	if len(rule.ApprovalRules) > 0 {
		if !equalEnvApprovalRules(live.ApprovalRules, rule.ApprovalRules) {
			builder.AddField("approval_rules", formatLiveApprovalRules(live.ApprovalRules), formatConfigApprovalRules(rule.ApprovalRules), ActionUpdate)
		}
	}

	return builder.Build(fmt.Sprintf("protected_environment:%s", rule.Name), ActionUpdate)
}

func formatAccessLevel(level int) string {
	switch level {
	case 0:
		return "No access (0)"
	case 10:
		return "Guest (10)"
	case 20:
		return "Reporter (20)"
	case 30:
		return "Developer (30)"
	case 40:
		return "Maintainer (40)"
	case 50:
		return "Owner (50)"
	case 60:
		return "Admin (60)"
	default:
		return fmt.Sprintf("level:%d", level)
	}
}

func formatConfigEnvAccess(list []config.EnvironmentAccessDescription) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a.UserID > 0 {
			parts = append(parts, fmt.Sprintf("User:%d", a.UserID))
		} else if a.GroupID > 0 {
			parts = append(parts, fmt.Sprintf("Group:%d", a.GroupID))
		} else if a.AccessLevel > 0 {
			parts = append(parts, formatAccessLevel(a.AccessLevel))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func formatLiveEnvAccess(list []*gogitlab.EnvironmentAccessDescription) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		if a.UserID > 0 {
			parts = append(parts, fmt.Sprintf("User:%d", a.UserID))
		} else if a.GroupID > 0 {
			parts = append(parts, fmt.Sprintf("Group:%d", a.GroupID))
		} else if a.AccessLevel > 0 {
			parts = append(parts, formatAccessLevel(int(a.AccessLevel)))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func equalEnvAccess(live []*gogitlab.EnvironmentAccessDescription, desired []config.EnvironmentAccessDescription) bool {
	liveKeys := make([]string, 0, len(live))
	for _, l := range live {
		if l == nil {
			continue
		}
		liveKeys = append(liveKeys, fmt.Sprintf("%d:%d:%d", l.AccessLevel, l.UserID, l.GroupID))
	}
	sort.Strings(liveKeys)

	desiredKeys := make([]string, 0, len(desired))
	for _, d := range desired {
		desiredKeys = append(desiredKeys, fmt.Sprintf("%d:%d:%d", d.AccessLevel, d.UserID, d.GroupID))
	}
	sort.Strings(desiredKeys)

	if len(liveKeys) != len(desiredKeys) {
		return false
	}
	for i := range liveKeys {
		if liveKeys[i] != desiredKeys[i] {
			return false
		}
	}
	return true
}

func formatConfigApprovalRules(list []config.EnvironmentApprovalRuleConfig) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		req := 1
		if a.RequiredApprovalCount != nil {
			req = *a.RequiredApprovalCount
		}
		target := formatAccessLevel(a.AccessLevel)
		if a.UserID > 0 {
			target = fmt.Sprintf("User:%d", a.UserID)
		} else if a.GroupID > 0 {
			target = fmt.Sprintf("Group:%d", a.GroupID)
		}
		parts = append(parts, fmt.Sprintf("%s(%d)", target, req))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func formatLiveApprovalRules(list []*gogitlab.EnvironmentApprovalRule) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		target := formatAccessLevel(int(a.AccessLevel))
		if a.UserID > 0 {
			target = fmt.Sprintf("User:%d", a.UserID)
		} else if a.GroupID > 0 {
			target = fmt.Sprintf("Group:%d", a.GroupID)
		}
		parts = append(parts, fmt.Sprintf("%s(%d)", target, a.RequiredApprovalCount))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func equalEnvApprovalRules(live []*gogitlab.EnvironmentApprovalRule, desired []config.EnvironmentApprovalRuleConfig) bool {
	liveKeys := make([]string, 0, len(live))
	for _, l := range live {
		if l == nil {
			continue
		}
		liveKeys = append(liveKeys, fmt.Sprintf("%d:%d:%d:%d", l.AccessLevel, l.UserID, l.GroupID, l.RequiredApprovalCount))
	}
	sort.Strings(liveKeys)

	desiredKeys := make([]string, 0, len(desired))
	for _, d := range desired {
		req := 1
		if d.RequiredApprovalCount != nil {
			req = *d.RequiredApprovalCount
		}
		desiredKeys = append(desiredKeys, fmt.Sprintf("%d:%d:%d:%d", d.AccessLevel, d.UserID, d.GroupID, req))
	}
	sort.Strings(desiredKeys)

	if len(liveKeys) != len(desiredKeys) {
		return false
	}
	for i := range liveKeys {
		if liveKeys[i] != desiredKeys[i] {
			return false
		}
	}
	return true
}
