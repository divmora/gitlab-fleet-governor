package governance

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ProtectedBranchesReconciler implements GovernanceOperation for branch protections.
type ProtectedBranchesReconciler struct{}

// NewProtectedBranchesReconciler instantiates a new protected branches reconciler.
func NewProtectedBranchesReconciler() *ProtectedBranchesReconciler {
	return &ProtectedBranchesReconciler{}
}

// NewProtectedBranchesOperation creates a new protected branches operation instance.
func NewProtectedBranchesOperation() *ProtectedBranchesReconciler {
	return NewProtectedBranchesReconciler()
}

// Name returns the canonical operation identifier.
func (r *ProtectedBranchesReconciler) Name() string {
	return "protected_branches"
}

// Order returns the execution order sequence (20).
func (r *ProtectedBranchesReconciler) Order() int {
	return 20
}

// Plan evaluates project protected branches against policy config (dry-run).
func (r *ProtectedBranchesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.ProtectedBranches) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveBranches, err := r.fetchAllLiveProtectedBranches(ctx, client, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list protected branches for project %d: %w", project.ID, err)
	}

	liveMap := make(map[string]*gogitlab.ProtectedBranch)
	for _, b := range liveBranches {
		liveMap[b.Name] = b
	}

	var allDiffs []Diff
	overallAction := ActionNoop

	for _, rule := range cfg.Policies.ProtectedBranches {
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

// Apply executes protected branches policy enforcement (live mutation).
func (r *ProtectedBranchesReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || len(cfg.Policies.ProtectedBranches) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveBranches, err := r.fetchAllLiveProtectedBranches(ctx, client, project.ID)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	liveMap := make(map[string]*gogitlab.ProtectedBranch)
	for _, b := range liveBranches {
		liveMap[b.Name] = b
	}

	var appliedDiffs []Diff
	overallAction := ActionNoop

	for _, rule := range cfg.Policies.ProtectedBranches {
		live, found := liveMap[rule.Name]
		if !found {
			// 1. Missing: Create protection
			diff := r.buildCreateDiff(&rule)
			protectOpt := r.toProtectOptions(&rule)
			_, _, createErr := client.ProtectedBranches().ProtectRepositoryBranches(project.ID, protectOpt, gogitlab.WithContext(ctx))
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

			// Determine if ONLY CodeOwnerApprovalRequired changed
			onlyCodeOwnerChanged := r.isOnlyCodeOwnerApprovalChanged(diff)
			if onlyCodeOwnerChanged && rule.CodeOwnerApprovalRequired != nil {
				patchOpt := &gogitlab.UpdateProtectedBranchOptions{
					CodeOwnerApprovalRequired: rule.CodeOwnerApprovalRequired,
				}
				_, _, patchErr := client.ProtectedBranches().UpdateProtectedBranch(project.ID, rule.Name, patchOpt, gogitlab.WithContext(ctx))
				if patchErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, append(appliedDiffs, diff), patchErr, start), patchErr
				}
			} else {
				// Access levels or force push changed: Recreate protection (Unprotect -> Protect)
				_, unprotectErr := client.ProtectedBranches().UnprotectRepositoryBranches(project.ID, rule.Name, gogitlab.WithContext(ctx))
				if unprotectErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, append(appliedDiffs, diff), unprotectErr, start), unprotectErr
				}

				protectOpt := r.toProtectOptions(&rule)
				_, _, reprotectErr := client.ProtectedBranches().ProtectRepositoryBranches(project.ID, protectOpt, gogitlab.WithContext(ctx))
				if reprotectErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, append(appliedDiffs, diff), reprotectErr, start), reprotectErr
				}
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

// PlanGroup protected branches on group is a clean no-op.
func (r *ProtectedBranchesReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Protected branches are not applicable to groups"), nil
}

// ApplyGroup protected branches on group is a clean no-op.
func (r *ProtectedBranchesReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Protected branches are not applicable to groups"), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *ProtectedBranchesReconciler) fetchAllLiveProtectedBranches(ctx context.Context, client gitlab.GitLabClient, projectID int) ([]*gogitlab.ProtectedBranch, error) {
	var all []*gogitlab.ProtectedBranch
	page := 1
	for {
		opts := &gogitlab.ListProtectedBranchesOptions{
			ListOptions: gogitlab.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		}
		branches, resp, err := client.ProtectedBranches().ListProtectedBranches(projectID, opts, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		all = append(all, branches...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}

func (r *ProtectedBranchesReconciler) buildCreateDiff(rule *config.ProtectedBranchRuleConfig) Diff {
	builder := NewDiffBuilder()
	builder.AddField("name", nil, rule.Name, ActionCreate)
	if len(rule.AllowedToPush) > 0 {
		builder.AddField("allowed_to_push", nil, formatAccessDescriptions(rule.AllowedToPush), ActionCreate)
	}
	if len(rule.AllowedToMerge) > 0 {
		builder.AddField("allowed_to_merge", nil, formatAccessDescriptions(rule.AllowedToMerge), ActionCreate)
	}
	if len(rule.AllowedToUnprotect) > 0 {
		builder.AddField("allowed_to_unprotect", nil, formatAccessDescriptions(rule.AllowedToUnprotect), ActionCreate)
	}
	if rule.AllowForcePush != nil {
		builder.AddField("allow_force_push", nil, *rule.AllowForcePush, ActionCreate)
	}
	if rule.CodeOwnerApprovalRequired != nil {
		builder.AddField("code_owner_approval_required", nil, *rule.CodeOwnerApprovalRequired, ActionCreate)
	}
	return builder.Build(fmt.Sprintf("protected_branch:%s", rule.Name), ActionCreate)
}

func (r *ProtectedBranchesReconciler) buildUpdateDiff(live *gogitlab.ProtectedBranch, rule *config.ProtectedBranchRuleConfig) Diff {
	builder := NewDiffBuilder()

	// Compare push permissions
	if len(rule.AllowedToPush) > 0 {
		livePush := liveAccessToDescriptions(live.PushAccessLevels)
		if !equalAccessDescriptions(livePush, rule.AllowedToPush) {
			builder.AddField("allowed_to_push", formatAccessDescriptions(livePush), formatAccessDescriptions(rule.AllowedToPush), ActionUpdate)
		}
	}

	// Compare merge permissions
	if len(rule.AllowedToMerge) > 0 {
		liveMerge := liveAccessToDescriptions(live.MergeAccessLevels)
		if !equalAccessDescriptions(liveMerge, rule.AllowedToMerge) {
			builder.AddField("allowed_to_merge", formatAccessDescriptions(liveMerge), formatAccessDescriptions(rule.AllowedToMerge), ActionUpdate)
		}
	}

	// Compare unprotect permissions
	if len(rule.AllowedToUnprotect) > 0 {
		liveUnprotect := liveAccessToDescriptions(live.UnprotectAccessLevels)
		if !equalAccessDescriptions(liveUnprotect, rule.AllowedToUnprotect) {
			builder.AddField("allowed_to_unprotect", formatAccessDescriptions(liveUnprotect), formatAccessDescriptions(rule.AllowedToUnprotect), ActionUpdate)
		}
	}

	// Compare boolean flags
	builder.Add(CompareBoolPtr("allow_force_push", live.AllowForcePush, rule.AllowForcePush))
	builder.Add(CompareBoolPtr("code_owner_approval_required", live.CodeOwnerApprovalRequired, rule.CodeOwnerApprovalRequired))

	return builder.Build(fmt.Sprintf("protected_branch:%s", rule.Name), ActionUpdate)
}

func (r *ProtectedBranchesReconciler) isOnlyCodeOwnerApprovalChanged(diff Diff) bool {
	if len(diff.Fields) == 1 && diff.Fields[0].Field == "code_owner_approval_required" {
		return true
	}
	return false
}

func (r *ProtectedBranchesReconciler) toProtectOptions(rule *config.ProtectedBranchRuleConfig) *gogitlab.ProtectRepositoryBranchesOptions {
	opt := &gogitlab.ProtectRepositoryBranchesOptions{
		Name: gogitlab.Ptr(rule.Name),
	}

	if rule.AllowForcePush != nil {
		opt.AllowForcePush = rule.AllowForcePush
	}
	if rule.CodeOwnerApprovalRequired != nil {
		opt.CodeOwnerApprovalRequired = rule.CodeOwnerApprovalRequired
	}

	// Map AllowedToPush
	if len(rule.AllowedToPush) > 0 {
		perms := toBranchPermissionOptions(rule.AllowedToPush)
		opt.AllowedToPush = &perms
		if len(rule.AllowedToPush) == 1 && rule.AllowedToPush[0].UserID == 0 && rule.AllowedToPush[0].GroupID == 0 && rule.AllowedToPush[0].DeployKeyID == 0 {
			accessVal := gogitlab.AccessLevelValue(rule.AllowedToPush[0].AccessLevel)
			opt.PushAccessLevel = &accessVal
		}
	}

	// Map AllowedToMerge
	if len(rule.AllowedToMerge) > 0 {
		perms := toBranchPermissionOptions(rule.AllowedToMerge)
		opt.AllowedToMerge = &perms
		if len(rule.AllowedToMerge) == 1 && rule.AllowedToMerge[0].UserID == 0 && rule.AllowedToMerge[0].GroupID == 0 {
			accessVal := gogitlab.AccessLevelValue(rule.AllowedToMerge[0].AccessLevel)
			opt.MergeAccessLevel = &accessVal
		}
	}

	// Map AllowedToUnprotect
	if len(rule.AllowedToUnprotect) > 0 {
		perms := toBranchPermissionOptions(rule.AllowedToUnprotect)
		opt.AllowedToUnprotect = &perms
		if len(rule.AllowedToUnprotect) == 1 && rule.AllowedToUnprotect[0].UserID == 0 && rule.AllowedToUnprotect[0].GroupID == 0 {
			accessVal := gogitlab.AccessLevelValue(rule.AllowedToUnprotect[0].AccessLevel)
			opt.UnprotectAccessLevel = &accessVal
		}
	}

	return opt
}

func toBranchPermissionOptions(descs []config.BranchAccessDescription) []*gogitlab.BranchPermissionOptions {
	res := make([]*gogitlab.BranchPermissionOptions, 0, len(descs))
	for _, d := range descs {
		p := &gogitlab.BranchPermissionOptions{}
		if d.AccessLevel > 0 {
			accessVal := gogitlab.AccessLevelValue(d.AccessLevel)
			p.AccessLevel = &accessVal
		}
		if d.UserID > 0 {
			p.UserID = gogitlab.Ptr(d.UserID)
		}
		if d.GroupID > 0 {
			p.GroupID = gogitlab.Ptr(d.GroupID)
		}
		if d.DeployKeyID > 0 {
			p.DeployKeyID = gogitlab.Ptr(d.DeployKeyID)
		}
		res = append(res, p)
	}
	return res
}

func liveAccessToDescriptions(levels []*gogitlab.BranchAccessDescription) []config.BranchAccessDescription {
	res := make([]config.BranchAccessDescription, 0, len(levels))
	for _, l := range levels {
		res = append(res, config.BranchAccessDescription{
			AccessLevel: int(l.AccessLevel),
			UserID:      l.UserID,
			GroupID:     l.GroupID,
		})
	}
	return res
}

func equalAccessDescriptions(a, b []config.BranchAccessDescription) bool {
	if len(a) != len(b) {
		return false
	}
	sa := make([]config.BranchAccessDescription, len(a))
	copy(sa, a)
	sort.Slice(sa, func(i, j int) bool {
		return fmt.Sprintf("%d:%d:%d", sa[i].AccessLevel, sa[i].UserID, sa[i].GroupID) < fmt.Sprintf("%d:%d:%d", sa[j].AccessLevel, sa[j].UserID, sa[j].GroupID)
	})

	sb := make([]config.BranchAccessDescription, len(b))
	copy(sb, b)
	sort.Slice(sb, func(i, j int) bool {
		return fmt.Sprintf("%d:%d:%d", sb[i].AccessLevel, sb[i].UserID, sb[i].GroupID) < fmt.Sprintf("%d:%d:%d", sb[j].AccessLevel, sb[j].UserID, sb[j].GroupID)
	})

	for i := range sa {
		if sa[i].AccessLevel != sb[i].AccessLevel || sa[i].UserID != sb[i].UserID || sa[i].GroupID != sb[i].GroupID {
			return false
		}
	}
	return true
}

func formatAccessDescriptions(descs []config.BranchAccessDescription) string {
	parts := make([]string, 0, len(descs))
	for _, d := range descs {
		if d.UserID > 0 {
			parts = append(parts, fmt.Sprintf("user_id:%d", d.UserID))
		} else if d.GroupID > 0 {
			parts = append(parts, fmt.Sprintf("group_id:%d", d.GroupID))
		} else if d.DeployKeyID > 0 {
			parts = append(parts, fmt.Sprintf("deploy_key_id:%d", d.DeployKeyID))
		} else {
			parts = append(parts, fmt.Sprintf("level:%d", d.AccessLevel))
		}
	}
	return strings.Join(parts, ", ")
}
