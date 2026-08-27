package governance

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// CachingResolver resolves GitLab usernames and group paths to numeric IDs
// with thread-safe in-memory caching to eliminate redundant API queries.
type CachingResolver struct {
	mu         sync.RWMutex
	userCache  map[string]int // username (lowercase) -> userID
	groupCache map[string]int // groupPath (lowercase) -> groupID
}

// NewCachingResolver initializes a new CachingResolver.
func NewCachingResolver() *CachingResolver {
	return &CachingResolver{
		userCache:  make(map[string]int),
		groupCache: make(map[string]int),
	}
}

// ResolveUsername looks up a username and returns its GitLab user ID.
func (r *CachingResolver) ResolveUsername(ctx context.Context, client gitlab.GitLabClient, username string) (int, error) {
	cleanUser := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(username)), "@")
	if cleanUser == "" {
		return 0, fmt.Errorf("empty username provided")
	}

	r.mu.RLock()
	if id, ok := r.userCache[cleanUser]; ok {
		r.mu.RUnlock()
		return id, nil
	}
	r.mu.RUnlock()

	users, _, err := client.Users().ListUsers(&gogitlab.ListUsersOptions{
		Username: gogitlab.Ptr(cleanUser),
	}, gogitlab.WithContext(ctx))
	if err != nil {
		return 0, fmt.Errorf("failed to lookup user '%s': %w", cleanUser, err)
	}

	if len(users) == 0 {
		return 0, fmt.Errorf("user '%s' not found in GitLab instance", cleanUser)
	}

	userID := users[0].ID
	r.mu.Lock()
	r.userCache[cleanUser] = userID
	r.mu.Unlock()

	return userID, nil
}

// ResolveGroupPath looks up a group full path and returns its GitLab group ID.
func (r *CachingResolver) ResolveGroupPath(ctx context.Context, client gitlab.GitLabClient, groupPath string) (int, error) {
	cleanPath := strings.Trim(strings.TrimSpace(strings.ToLower(groupPath)), "/")
	if cleanPath == "" {
		return 0, fmt.Errorf("empty group path provided")
	}

	r.mu.RLock()
	if id, ok := r.groupCache[cleanPath]; ok {
		r.mu.RUnlock()
		return id, nil
	}
	r.mu.RUnlock()

	group, _, err := client.Groups().GetGroup(cleanPath, nil, gogitlab.WithContext(ctx))
	if err != nil {
		return 0, fmt.Errorf("failed to lookup group '%s': %w", cleanPath, err)
	}
	if group == nil {
		return 0, fmt.Errorf("group '%s' not found in GitLab instance", cleanPath)
	}

	r.mu.Lock()
	r.groupCache[cleanPath] = group.ID
	r.mu.Unlock()

	return group.ID, nil
}

// ApprovalRulesReconciler reconciles project MR approval settings and named approval rules.
type ApprovalRulesReconciler struct {
	resolver *CachingResolver
}

// NewApprovalRulesReconciler instantiates an ApprovalRulesReconciler.
func NewApprovalRulesReconciler(resolver ...*CachingResolver) *ApprovalRulesReconciler {
	var res *CachingResolver
	if len(resolver) > 0 && resolver[0] != nil {
		res = resolver[0]
	} else {
		res = NewCachingResolver()
	}
	return &ApprovalRulesReconciler{resolver: res}
}

// NewApprovalRulesOperation creates an ApprovalRulesReconciler.
func NewApprovalRulesOperation(resolver ...*CachingResolver) *ApprovalRulesReconciler {
	return NewApprovalRulesReconciler(resolver...)
}

// Name returns the operation identifier.
func (r *ApprovalRulesReconciler) Name() string {
	return "approval_rules"
}

// Order returns the execution order sequence (30).
func (r *ApprovalRulesReconciler) Order() int {
	return 30
}

// Plan computes the diff between desired MR approval configuration/rules and GitLab state.
func (r *ApprovalRulesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.ApprovalRules == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	appConfig := cfg.Policies.ApprovalRules
	var allDiffs []Diff

	// 1. Reconcile Project General MR Approval Settings
	effSettings := effectiveApprovalSettings(appConfig)
	if effSettings != nil {
		liveApprovals, _, err := client.ApprovalRules().GetApprovalConfiguration(project.ID, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to get project approval configuration for project %d: %w", project.ID, err)
		}
		if liveApprovals == nil {
			liveApprovals = &gogitlab.ProjectApprovals{}
		}

		settingsDiff := r.diffApprovalSettings(effSettings, liveApprovals)
		if settingsDiff.HasChanges() {
			allDiffs = append(allDiffs, settingsDiff)
		}
	}

	// 2. Reconcile Named Approval Rules
	liveRules, _, err := client.ApprovalRules().GetProjectApprovalRules(project.ID, nil, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list project approval rules for project %d: %w", project.ID, err)
	}

	liveRulesMap := make(map[string]*gogitlab.ProjectApprovalRule)
	for _, lr := range liveRules {
		if lr != nil {
			liveRulesMap[strings.ToLower(strings.TrimSpace(lr.Name))] = lr
		}
	}

	matchedLiveRuleIDs := make(map[int]bool)

	// Evaluate desired rules
	for _, ruleCfg := range appConfig.Rules {
		ruleNameKey := strings.ToLower(strings.TrimSpace(ruleCfg.Name))
		existingRule, found := liveRulesMap[ruleNameKey]

		// Resolve approver user IDs
		desiredUserIDs, err := r.resolveUserIDs(ctx, client, ruleCfg)
		if err != nil {
			return nil, fmt.Errorf("rule '%s' user resolution failed: %w", ruleCfg.Name, err)
		}

		// Resolve approver group IDs
		desiredGroupIDs, err := r.resolveGroupIDs(ctx, client, ruleCfg)
		if err != nil {
			return nil, fmt.Errorf("rule '%s' group resolution failed: %w", ruleCfg.Name, err)
		}

		if !found {
			// Rule needs to be created
			builder := NewDiffBuilder()
			builder.AddField("name", nil, ruleCfg.Name, ActionCreate)
			builder.AddField("approvals_required", nil, ruleCfg.ApprovalsRequired, ActionCreate)
			if len(desiredUserIDs) > 0 {
				builder.AddField("user_ids", nil, desiredUserIDs, ActionCreate)
			}
			if len(desiredGroupIDs) > 0 {
				builder.AddField("group_ids", nil, desiredGroupIDs, ActionCreate)
			}
			builder.SetDetails(fmt.Sprintf("Create approval rule '%s' requiring %d approvals", ruleCfg.Name, ruleCfg.ApprovalsRequired))
			allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("approval_rule:%s", ruleCfg.Name), ActionCreate))
		} else {
			matchedLiveRuleIDs[existingRule.ID] = true
			ruleDiff := r.diffSingleRule(ruleCfg, existingRule, desiredUserIDs, desiredGroupIDs)
			if ruleDiff.HasChanges() {
				allDiffs = append(allDiffs, ruleDiff)
			}
		}
	}

	// 3. Drift Pruning of unmanaged rules (if prune is enabled)
	if appConfig.Prune != nil && *appConfig.Prune {
		for _, lr := range liveRules {
			if lr == nil || matchedLiveRuleIDs[lr.ID] {
				continue
			}
			// Skip system/report/code_owner rules unless regular
			if lr.RuleType != "" && lr.RuleType != "regular" {
				continue
			}
			builder := NewDiffBuilder()
			builder.AddField("name", lr.Name, nil, ActionDelete)
			builder.AddField("id", lr.ID, nil, ActionDelete)
			builder.SetDetails(fmt.Sprintf("Prune unmanaged approval rule '%s' (ID: %d)", lr.Name, lr.ID))
			allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("approval_rule:%s", lr.Name), ActionDelete))
		}
	}

	if len(allDiffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, allDiffs), nil
}

// Apply executes planned MR approval settings and named approval rules changes.
func (r *ApprovalRulesReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.ApprovalRules == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	plan, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	appConfig := cfg.Policies.ApprovalRules

	// 1. Apply Settings update if needed
	effSettings := effectiveApprovalSettings(appConfig)
	if effSettings != nil {
		changeOpts := &gogitlab.ChangeApprovalConfigurationOptions{}
		hasSettingChange := false

		if effSettings.ApprovalsBeforeMerge != nil {
			changeOpts.ApprovalsBeforeMerge = effSettings.ApprovalsBeforeMerge
			hasSettingChange = true
		}
		if effSettings.ResetApprovalsOnPush != nil {
			changeOpts.ResetApprovalsOnPush = effSettings.ResetApprovalsOnPush
			hasSettingChange = true
		}
		if effSettings.AllowAuthorApproval != nil {
			changeOpts.MergeRequestsAuthorApproval = effSettings.AllowAuthorApproval
			hasSettingChange = true
		}
		if effSettings.AllowCommitterApproval != nil {
			// Inverted boolean
			disabled := !(*effSettings.AllowCommitterApproval)
			changeOpts.MergeRequestsDisableCommittersApproval = &disabled
			hasSettingChange = true
		}
		if effSettings.AllowOverridesToApproverListPerMergeRequest != nil {
			// Inverted boolean
			disabled := !(*effSettings.AllowOverridesToApproverListPerMergeRequest)
			changeOpts.DisableOverridingApproversPerMergeRequest = &disabled
			hasSettingChange = true
		}
		if effSettings.RetainApprovalsOnPush != nil {
			// Inverted boolean
			reset := !(*effSettings.RetainApprovalsOnPush)
			changeOpts.ResetApprovalsOnPush = &reset
			hasSettingChange = true
		}
		if effSettings.SelectiveCodeOwnerRemovals != nil {
			changeOpts.SelectiveCodeOwnerRemovals = effSettings.SelectiveCodeOwnerRemovals
			hasSettingChange = true
		}
		if effSettings.RequirePasswordToApprove != nil {
			changeOpts.RequirePasswordToApprove = effSettings.RequirePasswordToApprove
			hasSettingChange = true
		}

		if hasSettingChange {
			_, _, err := client.ApprovalRules().ChangeApprovalConfiguration(project.ID, changeOpts, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to change approval configuration: %w", err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		}
	}

	// 2. Fetch current rules for mutating
	liveRules, _, err := client.ApprovalRules().GetProjectApprovalRules(project.ID, nil, gogitlab.WithContext(ctx))
	if err != nil {
		applyErr := fmt.Errorf("failed to list project approval rules: %w", err)
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
	}

	liveRulesMap := make(map[string]*gogitlab.ProjectApprovalRule)
	for _, lr := range liveRules {
		if lr != nil {
			liveRulesMap[strings.ToLower(strings.TrimSpace(lr.Name))] = lr
		}
	}

	matchedLiveRuleIDs := make(map[int]bool)

	// Apply declared rules
	for _, ruleCfg := range appConfig.Rules {
		ruleNameKey := strings.ToLower(strings.TrimSpace(ruleCfg.Name))
		existingRule, found := liveRulesMap[ruleNameKey]

		userIDs, _ := r.resolveUserIDs(ctx, client, ruleCfg)
		groupIDs, _ := r.resolveGroupIDs(ctx, client, ruleCfg)

		if !found {
			createOpts := &gogitlab.CreateProjectLevelRuleOptions{
				Name:              gogitlab.Ptr(ruleCfg.Name),
				ApprovalsRequired: gogitlab.Ptr(ruleCfg.ApprovalsRequired),
				UserIDs:           &userIDs,
				GroupIDs:          &groupIDs,
			}
			if len(ruleCfg.ProtectedBranchIDs) > 0 {
				createOpts.ProtectedBranchIDs = &ruleCfg.ProtectedBranchIDs
			}
			if ruleCfg.RuleType != "" {
				createOpts.RuleType = gogitlab.Ptr(ruleCfg.RuleType)
			}
			_, _, err := client.ApprovalRules().CreateProjectApprovalRule(project.ID, createOpts, gogitlab.WithContext(ctx))
			if err != nil {
				createErr := fmt.Errorf("failed to create approval rule '%s': %w", ruleCfg.Name, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, StatusFailed, plan.Diffs, createErr, start), createErr
			}
		} else {
			matchedLiveRuleIDs[existingRule.ID] = true
			updateOpts := &gogitlab.UpdateProjectLevelRuleOptions{
				Name:              gogitlab.Ptr(ruleCfg.Name),
				ApprovalsRequired: gogitlab.Ptr(ruleCfg.ApprovalsRequired),
				UserIDs:           &userIDs,
				GroupIDs:          &groupIDs,
			}
			if len(ruleCfg.ProtectedBranchIDs) > 0 {
				updateOpts.ProtectedBranchIDs = &ruleCfg.ProtectedBranchIDs
			}
			_, _, err := client.ApprovalRules().UpdateProjectApprovalRule(project.ID, existingRule.ID, updateOpts, gogitlab.WithContext(ctx))
			if err != nil {
				updateErr := fmt.Errorf("failed to update approval rule '%s' (ID %d): %w", ruleCfg.Name, existingRule.ID, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, updateErr, start), updateErr
			}
		}
	}

	// Apply drift pruning
	if appConfig.Prune != nil && *appConfig.Prune {
		for _, lr := range liveRules {
			if lr == nil || matchedLiveRuleIDs[lr.ID] {
				continue
			}
			if lr.RuleType != "" && lr.RuleType != "regular" {
				continue
			}
			_, err := client.ApprovalRules().DeleteProjectApprovalRule(project.ID, lr.ID, gogitlab.WithContext(ctx))
			if err != nil {
				deleteErr := fmt.Errorf("failed to delete unmanaged approval rule '%s' (ID %d): %w", lr.Name, lr.ID, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionDelete, StatusFailed, plan.Diffs, deleteErr, start), deleteErr
			}
		}
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, plan.Diffs, nil, start), nil
}

// PlanGroup is a no-op since GitLab approval rules are project-scoped.
func (r *ApprovalRulesReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Approval rules are not applicable to groups"), nil
}

// ApplyGroup is a no-op since GitLab approval rules are project-scoped.
func (r *ApprovalRulesReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Approval rules are not applicable to groups"), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *ApprovalRulesReconciler) resolveUserIDs(ctx context.Context, client gitlab.GitLabClient, ruleCfg config.ApprovalRuleConfig) ([]int, error) {
	idSet := make(map[int]bool)
	for _, id := range ruleCfg.UserIDs {
		if id > 0 {
			idSet[id] = true
		}
	}
	for _, u := range ruleCfg.UserUsernames {
		if u != "" {
			uid, err := r.resolver.ResolveUsername(ctx, client, u)
			if err != nil {
				return nil, err
			}
			idSet[uid] = true
		}
	}
	result := make([]int, 0, len(idSet))
	for id := range idSet {
		result = append(result, id)
	}
	sort.Ints(result)
	return result, nil
}

func (r *ApprovalRulesReconciler) resolveGroupIDs(ctx context.Context, client gitlab.GitLabClient, ruleCfg config.ApprovalRuleConfig) ([]int, error) {
	idSet := make(map[int]bool)
	for _, id := range ruleCfg.GroupIDs {
		if id > 0 {
			idSet[id] = true
		}
	}
	for _, g := range ruleCfg.GroupPaths {
		if g != "" {
			gid, err := r.resolver.ResolveGroupPath(ctx, client, g)
			if err != nil {
				return nil, err
			}
			idSet[gid] = true
		}
	}
	result := make([]int, 0, len(idSet))
	for id := range idSet {
		result = append(result, id)
	}
	sort.Ints(result)
	return result, nil
}

func (r *ApprovalRulesReconciler) diffApprovalSettings(desired *config.ApprovalSettingsConfig, live *gogitlab.ProjectApprovals) Diff {
	builder := NewDiffBuilder()

	if desired.ApprovalsBeforeMerge != nil && live.ApprovalsBeforeMerge != *desired.ApprovalsBeforeMerge {
		builder.AddField("approvals_before_merge", live.ApprovalsBeforeMerge, *desired.ApprovalsBeforeMerge, ActionUpdate)
	}
	if desired.ResetApprovalsOnPush != nil && live.ResetApprovalsOnPush != *desired.ResetApprovalsOnPush {
		builder.AddField("reset_approvals_on_push", live.ResetApprovalsOnPush, *desired.ResetApprovalsOnPush, ActionUpdate)
	}
	if desired.AllowAuthorApproval != nil && live.MergeRequestsAuthorApproval != *desired.AllowAuthorApproval {
		builder.AddField("allow_author_approval", live.MergeRequestsAuthorApproval, *desired.AllowAuthorApproval, ActionUpdate)
	}
	if desired.AllowCommitterApproval != nil {
		liveAllowed := !live.MergeRequestsDisableCommittersApproval
		if liveAllowed != *desired.AllowCommitterApproval {
			builder.AddField("allow_committer_approval", liveAllowed, *desired.AllowCommitterApproval, ActionUpdate)
		}
	}
	if desired.AllowOverridesToApproverListPerMergeRequest != nil {
		liveAllowed := !live.DisableOverridingApproversPerMergeRequest
		if liveAllowed != *desired.AllowOverridesToApproverListPerMergeRequest {
			builder.AddField("allow_overrides_to_approver_list_per_merge_request", liveAllowed, *desired.AllowOverridesToApproverListPerMergeRequest, ActionUpdate)
		}
	}
	if desired.RetainApprovalsOnPush != nil {
		liveRetained := !live.ResetApprovalsOnPush
		if liveRetained != *desired.RetainApprovalsOnPush {
			builder.AddField("retain_approvals_on_push", liveRetained, *desired.RetainApprovalsOnPush, ActionUpdate)
		}
	}
	if desired.SelectiveCodeOwnerRemovals != nil && live.SelectiveCodeOwnerRemovals != *desired.SelectiveCodeOwnerRemovals {
		builder.AddField("selective_code_owner_removals", live.SelectiveCodeOwnerRemovals, *desired.SelectiveCodeOwnerRemovals, ActionUpdate)
	}
	if desired.RequirePasswordToApprove != nil && live.RequirePasswordToApprove != *desired.RequirePasswordToApprove {
		builder.AddField("require_password_to_approve", live.RequirePasswordToApprove, *desired.RequirePasswordToApprove, ActionUpdate)
	}

	return builder.Build("project_approval_settings", ActionUpdate)
}

func effectiveApprovalSettings(appConfig *config.ApprovalRulesConfig) *config.ApprovalSettingsConfig {
	if appConfig == nil {
		return nil
	}
	if appConfig.Settings == nil && appConfig.ApprovalsBeforeMerge == nil && appConfig.ResetApprovalsOnPush == nil {
		return nil
	}
	res := &config.ApprovalSettingsConfig{}
	if appConfig.Settings != nil {
		*res = *appConfig.Settings
	}
	if appConfig.ApprovalsBeforeMerge != nil {
		res.ApprovalsBeforeMerge = appConfig.ApprovalsBeforeMerge
	}
	if appConfig.ResetApprovalsOnPush != nil {
		res.ResetApprovalsOnPush = appConfig.ResetApprovalsOnPush
	}
	return res
}

func (r *ApprovalRulesReconciler) diffSingleRule(desired config.ApprovalRuleConfig, live *gogitlab.ProjectApprovalRule, desiredUserIDs, desiredGroupIDs []int) Diff {
	builder := NewDiffBuilder()

	if live.ApprovalsRequired != desired.ApprovalsRequired {
		builder.AddField("approvals_required", live.ApprovalsRequired, desired.ApprovalsRequired, ActionUpdate)
	}

	// Live user IDs
	liveUserIDs := make([]int, 0, len(live.Users))
	for _, u := range live.Users {
		if u != nil {
			liveUserIDs = append(liveUserIDs, u.ID)
		}
	}
	sort.Ints(liveUserIDs)
	if !slicesEqual(liveUserIDs, desiredUserIDs) {
		builder.AddField("user_ids", liveUserIDs, desiredUserIDs, ActionUpdate)
	}

	// Live group IDs
	liveGroupIDs := make([]int, 0, len(live.Groups))
	for _, g := range live.Groups {
		if g != nil {
			liveGroupIDs = append(liveGroupIDs, g.ID)
		}
	}
	sort.Ints(liveGroupIDs)
	if !slicesEqual(liveGroupIDs, desiredGroupIDs) {
		builder.AddField("group_ids", liveGroupIDs, desiredGroupIDs, ActionUpdate)
	}

	return builder.Build(fmt.Sprintf("approval_rule:%s", desired.Name), ActionUpdate)
}

func slicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
