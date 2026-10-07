package governance

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// ProtectedEnvironmentsReconciler implements GovernanceOperation for protected environments.
type ProtectedEnvironmentsReconciler struct {
	resolver *CachingResolver
}

// NewProtectedEnvironmentsReconciler instantiates a new protected environments reconciler.
func NewProtectedEnvironmentsReconciler(resolver ...*CachingResolver) *ProtectedEnvironmentsReconciler {
	res := NewCachingResolver()
	if len(resolver) > 0 && resolver[0] != nil {
		res = resolver[0]
	}
	return &ProtectedEnvironmentsReconciler{resolver: res}
}

// NewProtectedEnvironmentsOperation creates a new protected environments operation instance.
func NewProtectedEnvironmentsOperation(resolver ...*CachingResolver) *ProtectedEnvironmentsReconciler {
	return NewProtectedEnvironmentsReconciler(resolver...)
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
	if cfg == nil || cfg.Policies.ProtectedEnvironments == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	peCfg := cfg.Policies.ProtectedEnvironments
	prune := peCfg.Prune != nil && *peCfg.Prune
	if len(peCfg.Rules) == 0 && !prune {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveEnvs, err := r.fetchAllLiveProtectedEnvironments(ctx, client, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list protected environments for project %d: %w", project.ID, err)
	}

	expandedRules, err := r.expandRules(ctx, client, project.ID, peCfg.Rules, liveEnvs)
	if err != nil {
		return nil, fmt.Errorf("failed to expand protected environment rules: %w", err)
	}

	liveMap := make(map[string]*gogitlab.ProtectedEnvironment)
	for _, env := range liveEnvs {
		if env != nil {
			liveMap[env.Name] = env
		}
	}

	var allDiffs []Diff
	overallAction := ActionNoop
	managedNames := make(map[string]bool)

	for _, rule := range expandedRules {
		managedNames[rule.Name] = true
		protectOpt, optErr := r.toProtectOptions(ctx, client, &rule)
		if optErr != nil {
			return nil, optErr
		}

		live, found := liveMap[rule.Name]
		if !found {
			// Protection missing -> CREATE
			diff := r.buildCreateDiff(&rule, protectOpt)
			allDiffs = append(allDiffs, diff)
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		} else {
			// Protection exists -> compare attributes
			diff := r.buildUpdateDiff(live, &rule, protectOpt)
			if diff.HasChanges() {
				allDiffs = append(allDiffs, diff)
				if overallAction == ActionNoop {
					overallAction = ActionUpdate
				}
			}
		}
	}

	// Prune unmanaged protected environments
	if prune {
		for _, live := range liveEnvs {
			if live == nil {
				continue
			}
			if !r.isManaged(live.Name, peCfg.Rules, managedNames) {
				builder := NewDiffBuilder()
				builder.AddField("protected", true, false, ActionDelete)
				allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("protected_environment:%s", live.Name), ActionDelete))
				if overallAction == ActionNoop {
					overallAction = ActionDelete
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
	if cfg == nil || cfg.Policies.ProtectedEnvironments == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	peCfg := cfg.Policies.ProtectedEnvironments
	prune := peCfg.Prune != nil && *peCfg.Prune
	if len(peCfg.Rules) == 0 && !prune {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveEnvs, err := r.fetchAllLiveProtectedEnvironments(ctx, client, project.ID)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	expandedRules, err := r.expandRules(ctx, client, project.ID, peCfg.Rules, liveEnvs)
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
	managedNames := make(map[string]bool)

	for _, rule := range expandedRules {
		managedNames[rule.Name] = true
		protectOpt, optErr := r.toProtectOptions(ctx, client, &rule)
		if optErr != nil {
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, appliedDiffs, optErr, start), optErr
		}

		live, found := liveMap[rule.Name]
		if !found {
			// 1. Missing: Create environment protection
			diff := r.buildCreateDiff(&rule, protectOpt)
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
			diff := r.buildUpdateDiff(live, &rule, protectOpt)
			if !diff.HasChanges() {
				continue
			}

			// Drift detected: Recreate protection atomically (Unprotect -> Protect)
			_, _ = client.ProtectedEnvironments().UnprotectEnvironment(project.ID, rule.Name, gogitlab.WithContext(ctx))

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

	// Prune unmanaged protected environments
	if prune {
		for _, live := range liveEnvs {
			if live == nil {
				continue
			}
			if !r.isManaged(live.Name, peCfg.Rules, managedNames) {
				_, unprotectErr := client.ProtectedEnvironments().UnprotectEnvironment(project.ID, live.Name, gogitlab.WithContext(ctx))
				if unprotectErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionDelete, StatusFailed, appliedDiffs, fmt.Errorf("failed to prune unmanaged protected environment '%s': %w", live.Name, unprotectErr), start), unprotectErr
				}
				builder := NewDiffBuilder()
				builder.AddField("protected", true, false, ActionDelete)
				appliedDiffs = append(appliedDiffs, builder.Build(fmt.Sprintf("protected_environment:%s", live.Name), ActionDelete))
				if overallAction == ActionNoop {
					overallAction = ActionDelete
				}
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

func (r *ProtectedEnvironmentsReconciler) expandRules(ctx context.Context, client gitlab.GitLabClient, projectID int, rules []config.ProtectedEnvironmentRuleConfig, liveEnvs []*gogitlab.ProtectedEnvironment) ([]config.ProtectedEnvironmentRuleConfig, error) {
	var hasWildcards bool
	for _, rule := range rules {
		if strings.ContainsAny(rule.Name, "*?[") {
			hasWildcards = true
			break
		}
	}

	if !hasWildcards {
		return rules, nil
	}

	// Fetch all project environments for wildcard expansion
	var projectEnvs []*gogitlab.Environment
	if client.Environments() != nil {
		page := 1
		for {
			opts := &gogitlab.ListEnvironmentsOptions{
				ListOptions: gogitlab.ListOptions{
					Page:    page,
					PerPage: 100,
				},
			}
			envs, resp, err := client.Environments().ListEnvironments(projectID, opts, gogitlab.WithContext(ctx))
			if err != nil {
				break
			}
			projectEnvs = append(projectEnvs, envs...)
			if resp == nil || resp.NextPage == 0 {
				break
			}
			page = resp.NextPage
		}
	}

	candidateNames := make(map[string]bool)
	for _, pe := range projectEnvs {
		if pe != nil && pe.Name != "" {
			candidateNames[pe.Name] = true
		}
	}
	for _, le := range liveEnvs {
		if le != nil && le.Name != "" {
			candidateNames[le.Name] = true
		}
	}

	seenNames := make(map[string]bool)
	var expanded []config.ProtectedEnvironmentRuleConfig

	for _, rule := range rules {
		if strings.ContainsAny(rule.Name, "*?[") {
			for name := range candidateNames {
				matched, _ := path.Match(rule.Name, name)
				if matched && !seenNames[name] {
					seenNames[name] = true
					concrete := rule
					concrete.Name = name
					expanded = append(expanded, concrete)
				}
			}
		} else {
			if !seenNames[rule.Name] {
				seenNames[rule.Name] = true
				expanded = append(expanded, rule)
			}
		}
	}

	return expanded, nil
}

func (r *ProtectedEnvironmentsReconciler) isManaged(name string, rules []config.ProtectedEnvironmentRuleConfig, explicitManaged map[string]bool) bool {
	if explicitManaged[name] {
		return true
	}
	for _, rule := range rules {
		if rule.Name == name {
			return true
		}
		if strings.ContainsAny(rule.Name, "*?[") {
			if matched, _ := path.Match(rule.Name, name); matched {
				return true
			}
		}
	}
	return false
}

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

func (r *ProtectedEnvironmentsReconciler) toProtectOptions(ctx context.Context, client gitlab.GitLabClient, rule *config.ProtectedEnvironmentRuleConfig) (*gogitlab.ProtectRepositoryEnvironmentsOptions, error) {
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
			uid := acc.UserID
			if uid == 0 && acc.Username != "" && r.resolver != nil {
				resolved, err := r.resolver.ResolveUsername(ctx, client, acc.Username)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve username '%s' for deploy access: %w", acc.Username, err)
				}
				uid = resolved
			}
			if uid > 0 {
				entry.UserID = gogitlab.Ptr(uid)
			}
			gid := acc.GroupID
			if gid == 0 && acc.GroupPath != "" && r.resolver != nil {
				resolved, err := r.resolver.ResolveGroupPath(ctx, client, acc.GroupPath)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve group_path '%s' for deploy access: %w", acc.GroupPath, err)
				}
				gid = resolved
			}
			if gid > 0 {
				entry.GroupID = gogitlab.Ptr(gid)
			}
			accessList = append(accessList, entry)
		}
		opt.DeployAccessLevels = &accessList
	}

	var ruleList []*gogitlab.EnvironmentApprovalRuleOptions
	for _, ar := range rule.ApprovalRules {
		entry := &gogitlab.EnvironmentApprovalRuleOptions{}
		if ar.AccessLevel > 0 {
			entry.AccessLevel = gogitlab.Ptr(gogitlab.AccessLevelValue(ar.AccessLevel))
		}
		uid := ar.UserID
		if uid == 0 && ar.Username != "" && r.resolver != nil {
			resolved, err := r.resolver.ResolveUsername(ctx, client, ar.Username)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve username '%s' for approval rule: %w", ar.Username, err)
			}
			uid = resolved
		}
		if uid > 0 {
			entry.UserID = gogitlab.Ptr(uid)
		}
		gid := ar.GroupID
		if gid == 0 && ar.GroupPath != "" && r.resolver != nil {
			resolved, err := r.resolver.ResolveGroupPath(ctx, client, ar.GroupPath)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve group_path '%s' for approval rule: %w", ar.GroupPath, err)
			}
			gid = resolved
		}
		if gid > 0 {
			entry.GroupID = gogitlab.Ptr(gid)
		}
		if ar.RequiredApprovalCount != nil {
			entry.RequiredApprovalCount = ar.RequiredApprovalCount
		}
		ruleList = append(ruleList, entry)
	}

	for _, u := range rule.ApprovalUsers {
		uid := u.UserID
		if uid == 0 && u.Username != "" && r.resolver != nil {
			resolved, err := r.resolver.ResolveUsername(ctx, client, u.Username)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve username '%s' for approval user: %w", u.Username, err)
			}
			uid = resolved
		}
		if uid > 0 {
			ruleList = append(ruleList, &gogitlab.EnvironmentApprovalRuleOptions{
				UserID: gogitlab.Ptr(uid),
			})
		}
	}

	for _, g := range rule.ApprovalGroups {
		gid := g.GroupID
		if gid == 0 && g.GroupPath != "" && r.resolver != nil {
			resolved, err := r.resolver.ResolveGroupPath(ctx, client, g.GroupPath)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve group_path '%s' for approval group: %w", g.GroupPath, err)
			}
			gid = resolved
		}
		if gid > 0 {
			ruleList = append(ruleList, &gogitlab.EnvironmentApprovalRuleOptions{
				GroupID: gogitlab.Ptr(gid),
			})
		}
	}

	if len(ruleList) > 0 {
		opt.ApprovalRules = &ruleList
	}
	return opt, nil
}

func (r *ProtectedEnvironmentsReconciler) buildCreateDiff(rule *config.ProtectedEnvironmentRuleConfig, opt *gogitlab.ProtectRepositoryEnvironmentsOptions) Diff {
	builder := NewDiffBuilder()
	builder.AddField("name", nil, rule.Name, ActionCreate)
	if rule.RequiredApprovalCount != nil {
		builder.AddField("required_approval_count", nil, *rule.RequiredApprovalCount, ActionCreate)
	}
	if opt != nil && opt.DeployAccessLevels != nil && len(*opt.DeployAccessLevels) > 0 {
		builder.AddField("deploy_access_levels", nil, formatDesiredEnvAccess(*opt.DeployAccessLevels), ActionCreate)
	}
	if opt != nil && opt.ApprovalRules != nil && len(*opt.ApprovalRules) > 0 {
		builder.AddField("approval_rules", nil, formatDesiredApprovalRules(*opt.ApprovalRules), ActionCreate)
	}
	return builder.Build(fmt.Sprintf("protected_environment:%s", rule.Name), ActionCreate)
}

func (r *ProtectedEnvironmentsReconciler) buildUpdateDiff(live *gogitlab.ProtectedEnvironment, rule *config.ProtectedEnvironmentRuleConfig, opt *gogitlab.ProtectRepositoryEnvironmentsOptions) Diff {
	builder := NewDiffBuilder()

	// Compare RequiredApprovalCount
	if rule.RequiredApprovalCount != nil && *rule.RequiredApprovalCount != live.RequiredApprovalCount {
		builder.AddField("required_approval_count", live.RequiredApprovalCount, *rule.RequiredApprovalCount, ActionUpdate)
	}

	// Compare DeployAccessLevels
	if opt != nil && opt.DeployAccessLevels != nil && len(*opt.DeployAccessLevels) > 0 {
		if !equalEnvAccess(live.DeployAccessLevels, *opt.DeployAccessLevels) {
			builder.AddField("deploy_access_levels", formatLiveEnvAccess(live.DeployAccessLevels), formatDesiredEnvAccess(*opt.DeployAccessLevels), ActionUpdate)
		}
	}

	// Compare ApprovalRules
	if opt != nil && opt.ApprovalRules != nil && len(*opt.ApprovalRules) > 0 {
		if !equalEnvApprovalRules(live.ApprovalRules, *opt.ApprovalRules) {
			builder.AddField("approval_rules", formatLiveApprovalRules(live.ApprovalRules), formatDesiredApprovalRules(*opt.ApprovalRules), ActionUpdate)
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

func formatDesiredEnvAccess(list []*gogitlab.EnvironmentAccessOptions) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		if a.UserID != nil && *a.UserID > 0 {
			parts = append(parts, fmt.Sprintf("User:%d", *a.UserID))
		} else if a.GroupID != nil && *a.GroupID > 0 {
			parts = append(parts, fmt.Sprintf("Group:%d", *a.GroupID))
		} else if a.AccessLevel != nil && *a.AccessLevel > 0 {
			parts = append(parts, formatAccessLevel(int(*a.AccessLevel)))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func formatDesiredApprovalRules(list []*gogitlab.EnvironmentApprovalRuleOptions) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		target := "unknown"
		if a.UserID != nil && *a.UserID > 0 {
			target = fmt.Sprintf("User:%d", *a.UserID)
		} else if a.GroupID != nil && *a.GroupID > 0 {
			target = fmt.Sprintf("Group:%d", *a.GroupID)
		} else if a.AccessLevel != nil && *a.AccessLevel > 0 {
			target = formatAccessLevel(int(*a.AccessLevel))
		}
		req := 1
		if a.RequiredApprovalCount != nil {
			req = *a.RequiredApprovalCount
		}
		parts = append(parts, fmt.Sprintf("%s(%d)", target, req))
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

func equalEnvAccess(live []*gogitlab.EnvironmentAccessDescription, desired []*gogitlab.EnvironmentAccessOptions) bool {
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
		if d == nil {
			continue
		}
		acc := 0
		if d.AccessLevel != nil {
			acc = int(*d.AccessLevel)
		}
		uid := 0
		if d.UserID != nil {
			uid = *d.UserID
		}
		gid := 0
		if d.GroupID != nil {
			gid = *d.GroupID
		}
		desiredKeys = append(desiredKeys, fmt.Sprintf("%d:%d:%d", acc, uid, gid))
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

func equalEnvApprovalRules(live []*gogitlab.EnvironmentApprovalRule, desired []*gogitlab.EnvironmentApprovalRuleOptions) bool {
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
		if d == nil {
			continue
		}
		acc := 0
		if d.AccessLevel != nil {
			acc = int(*d.AccessLevel)
		}
		uid := 0
		if d.UserID != nil {
			uid = *d.UserID
		}
		gid := 0
		if d.GroupID != nil {
			gid = *d.GroupID
		}
		req := 1
		if d.RequiredApprovalCount != nil {
			req = *d.RequiredApprovalCount
		}
		desiredKeys = append(desiredKeys, fmt.Sprintf("%d:%d:%d:%d", acc, uid, gid, req))
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
