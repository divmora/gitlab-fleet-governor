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

// GroupSettingsReconciler reconciles group-level security and membership
// settings (visibility, 2FA enforcement, creation levels, sharing and
// forking restrictions, IP and email-domain restrictions, default branch
// protection) on every targeted group.
type GroupSettingsReconciler struct{}

// NewGroupSettingsReconciler instantiates a new GroupSettingsReconciler.
func NewGroupSettingsReconciler() *GroupSettingsReconciler {
	return &GroupSettingsReconciler{}
}

// Name returns the operation identifier.
func (r *GroupSettingsReconciler) Name() string {
	return "group_settings"
}

// Order returns the execution order sequence (5): group settings such as
// 2FA enforcement come before anything that relies on membership.
func (r *GroupSettingsReconciler) Order() int {
	return 5
}

// Plan is a no-op for projects: group settings only apply to groups, and a
// no-op (rather than a skip) keeps project reports free of an extra entry.
func (r *GroupSettingsReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
}

// Apply is a no-op for projects; see Plan.
func (r *GroupSettingsReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
}

// PlanGroup compares the desired group settings with the live group.
func (r *GroupSettingsReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.GroupSettings == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	live, err := r.liveGroup(ctx, client, group)
	if err != nil {
		return nil, err
	}

	diff, _ := r.diff(live, cfg.Policies.GroupSettings)
	if !diff.HasChanges() {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}
	return NewPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, []Diff{diff}), nil
}

// ApplyGroup updates only the settings that differ from the policy.
func (r *GroupSettingsReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.GroupSettings == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	live, err := r.liveGroup(ctx, client, group)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionNoop, StatusFailed, nil, err, start), err
	}

	diff, opts := r.diff(live, cfg.Policies.GroupSettings)
	if !diff.HasChanges() {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	if _, _, err := client.Groups().UpdateGroup(group.ID, opts, gogitlab.WithContext(ctx)); err != nil {
		applyErr := fmt.Errorf("failed to update group settings for group %d: %w", group.ID, err)
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, StatusFailed, []Diff{diff}, applyErr, start), applyErr
	}
	return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, StatusSuccess, []Diff{diff}, nil, start), nil
}

func (r *GroupSettingsReconciler) liveGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group) (*gogitlab.Group, error) {
	live, _, err := client.Groups().GetGroup(group.ID, nil, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get group %d details: %w", group.ID, err)
	}
	if live == nil {
		live = group
	}
	return live, nil
}

// diff returns the drift between live and desired, and the update request
// that carries only the drifted settings.
func (r *GroupSettingsReconciler) diff(live *gogitlab.Group, d *config.GroupSettingsConfig) (Diff, *gogitlab.UpdateGroupOptions) {
	b := NewDiffBuilder()
	opts := &gogitlab.UpdateGroupOptions{}

	if fd := CompareString("visibility", string(live.Visibility), d.Visibility); fd != nil {
		b.Add(fd)
		opts.Visibility = gogitlab.Ptr(gogitlab.VisibilityValue(d.Visibility))
	}
	if fd := CompareBoolPtr("require_two_factor_authentication", live.RequireTwoFactorAuth, d.RequireTwoFactorAuth); fd != nil {
		b.Add(fd)
		opts.RequireTwoFactorAuth = d.RequireTwoFactorAuth
	}
	if fd := CompareIntPtr("two_factor_grace_period", live.TwoFactorGracePeriod, d.TwoFactorGracePeriod); fd != nil {
		b.Add(fd)
		opts.TwoFactorGracePeriod = d.TwoFactorGracePeriod
	}
	if fd := CompareString("project_creation_level", string(live.ProjectCreationLevel), d.ProjectCreationLevel); fd != nil {
		b.Add(fd)
		opts.ProjectCreationLevel = gogitlab.Ptr(gogitlab.ProjectCreationLevelValue(d.ProjectCreationLevel))
	}
	if fd := CompareString("subgroup_creation_level", string(live.SubGroupCreationLevel), d.SubGroupCreationLevel); fd != nil {
		b.Add(fd)
		opts.SubGroupCreationLevel = gogitlab.Ptr(gogitlab.SubGroupCreationLevelValue(d.SubGroupCreationLevel))
	}

	for _, f := range []struct {
		name string
		live bool
		want *bool
		set  func(*bool)
	}{
		{"membership_lock", live.MembershipLock, d.MembershipLock, func(v *bool) { opts.MembershipLock = v }},
		{"share_with_group_lock", live.ShareWithGroupLock, d.ShareWithGroupLock, func(v *bool) { opts.ShareWithGroupLock = v }},
		{"prevent_forking_outside_group", live.PreventForkingOutsideGroup, d.PreventForkingOutsideGroup, func(v *bool) { opts.PreventForkingOutsideGroup = v }},
		{"request_access_enabled", live.RequestAccessEnabled, d.RequestAccessEnabled, func(v *bool) { opts.RequestAccessEnabled = v }},
		{"emails_enabled", live.EmailsEnabled, d.EmailsEnabled, func(v *bool) { opts.EmailsEnabled = v }},
		{"mentions_disabled", live.MentionsDisabled, d.MentionsDisabled, func(v *bool) { opts.MentionsDisabled = v }},
	} {
		if fd := CompareBoolPtr(f.name, f.live, f.want); fd != nil {
			b.Add(fd)
			f.set(f.want)
		}
	}

	if d.IPRestrictionRanges != nil {
		if fd := CompareStringSlice("ip_restriction_ranges", splitList(live.IPRestrictionRanges), normaliseList(*d.IPRestrictionRanges)); fd != nil {
			b.Add(fd)
			opts.IPRestrictionRanges = gogitlab.Ptr(strings.Join(normaliseList(*d.IPRestrictionRanges), ","))
		}
	}
	if d.AllowedEmailDomains != nil {
		if fd := CompareStringSlice("allowed_email_domains", splitList(live.AllowedEmailDomainsList), normaliseList(*d.AllowedEmailDomains)); fd != nil {
			b.Add(fd)
			opts.AllowedEmailDomainsList = gogitlab.Ptr(strings.Join(normaliseList(*d.AllowedEmailDomains), ","))
		}
	}

	if d.DefaultBranchProtection != nil {
		if bp := r.branchProtectionDiff(b, live.DefaultBranchProtectionDefaults, d.DefaultBranchProtection); bp != nil {
			opts.DefaultBranchProtectionDefaults = bp
		}
	}

	return b.Build("group_settings", ActionUpdate), opts
}

// branchProtectionDiff adds drift in default_branch_protection_defaults to
// b and returns the options to send, or nil when nothing differs. GitLab
// treats the object as a whole, so the request carries every configured
// field once any of them differs.
func (r *GroupSettingsReconciler) branchProtectionDiff(b *DiffBuilder, live *gogitlab.BranchProtectionDefaults, d *config.DefaultBranchProtectionConfig) *gogitlab.DefaultBranchProtectionDefaultsOptions {
	if live == nil {
		live = &gogitlab.BranchProtectionDefaults{}
	}
	changed := false
	prefix := "default_branch_protection."

	compareLevels := func(field string, liveLevels []*gogitlab.GroupAccessLevel, want *[]int) {
		if want == nil {
			return
		}
		got := accessLevels(liveLevels)
		desired := append([]int(nil), *want...)
		sort.Ints(desired)
		if fmt.Sprint(got) != fmt.Sprint(desired) {
			b.AddField(prefix+field, got, desired, ActionUpdate)
			changed = true
		}
	}
	compareLevels("allowed_to_push", live.AllowedToPush, d.AllowedToPush)
	compareLevels("allowed_to_merge", live.AllowedToMerge, d.AllowedToMerge)

	for _, f := range []struct {
		name string
		live bool
		want *bool
	}{
		{"allow_force_push", live.AllowForcePush, d.AllowForcePush},
		{"developer_can_initial_push", live.DeveloperCanInitialPush, d.DeveloperCanInitialPush},
		{"code_owner_approval_required", live.CodeOwnerApprovalRequired, d.CodeOwnerApprovalRequired},
	} {
		if fd := CompareBoolPtr(prefix+f.name, f.live, f.want); fd != nil {
			b.Add(fd)
			changed = true
		}
	}
	if !changed {
		return nil
	}

	opts := &gogitlab.DefaultBranchProtectionDefaultsOptions{
		AllowForcePush:            d.AllowForcePush,
		DeveloperCanInitialPush:   d.DeveloperCanInitialPush,
		CodeOwnerApprovalRequired: d.CodeOwnerApprovalRequired,
	}
	if d.AllowedToPush != nil {
		opts.AllowedToPush = gogitlab.Ptr(toGroupAccessLevels(*d.AllowedToPush))
	}
	if d.AllowedToMerge != nil {
		opts.AllowedToMerge = gogitlab.Ptr(toGroupAccessLevels(*d.AllowedToMerge))
	}
	return opts
}

func accessLevels(in []*gogitlab.GroupAccessLevel) []int {
	out := make([]int, 0, len(in))
	for _, l := range in {
		if l != nil && l.AccessLevel != nil {
			out = append(out, int(*l.AccessLevel))
		}
	}
	sort.Ints(out)
	return out
}

func toGroupAccessLevels(levels []int) []*gogitlab.GroupAccessLevel {
	out := make([]*gogitlab.GroupAccessLevel, 0, len(levels))
	for _, l := range levels {
		out = append(out, &gogitlab.GroupAccessLevel{AccessLevel: gogitlab.Ptr(gogitlab.AccessLevelValue(l))})
	}
	return out
}

// splitList parses GitLab's comma-separated list fields.
func splitList(s string) []string {
	return normaliseList(strings.Split(s, ","))
}

func normaliseList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			out = append(out, v)
		}
	}
	return out
}
