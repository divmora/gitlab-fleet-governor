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

// ProtectedTagsReconciler implements GovernanceOperation for project protected tags.
type ProtectedTagsReconciler struct {
	resolver *CachingResolver
}

// NewProtectedTagsReconciler instantiates a new protected tags reconciler.
func NewProtectedTagsReconciler(resolver ...*CachingResolver) *ProtectedTagsReconciler {
	res := NewCachingResolver()
	if len(resolver) > 0 && resolver[0] != nil {
		res = resolver[0]
	}
	return &ProtectedTagsReconciler{resolver: res}
}

// NewProtectedTagsOperation creates a new protected tags operation instance.
func NewProtectedTagsOperation(resolver ...*CachingResolver) *ProtectedTagsReconciler {
	return NewProtectedTagsReconciler(resolver...)
}

// Name returns the canonical operation identifier.
func (r *ProtectedTagsReconciler) Name() string {
	return "protected_tags"
}

// Order returns the execution order sequence (22).
func (r *ProtectedTagsReconciler) Order() int {
	return 22
}

// MatchesTagPattern checks if a Git tag name matches a protected tag wildcard pattern (e.g. "v*" matches "v1.0.0").
func MatchesTagPattern(pattern, tag string) bool {
	pattern = strings.TrimSpace(pattern)
	tag = strings.TrimSpace(tag)
	if pattern == tag || pattern == "*" {
		return true
	}
	matched, err := path.Match(pattern, tag)
	if err == nil && matched {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasPrefix(tag, prefix) {
			return true
		}
	}
	return false
}

// Plan evaluates project protected tags against policy config (dry-run).
func (r *ProtectedTagsReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.ProtectedTags == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	ptCfg := cfg.Policies.ProtectedTags
	prune := ptCfg.Prune != nil && *ptCfg.Prune
	if len(ptCfg.Rules) == 0 && !prune {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveTags, err := r.fetchAllLiveProtectedTags(ctx, client, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list protected tags for project %d: %w", project.ID, err)
	}

	liveMap := make(map[string]*gogitlab.ProtectedTag)
	for _, t := range liveTags {
		if t != nil {
			liveMap[t.Name] = t
		}
	}

	var allDiffs []Diff
	overallAction := ActionNoop
	managedNames := make(map[string]bool)

	for _, rule := range ptCfg.Rules {
		managedNames[rule.Name] = true
		resolvedRule, resErr := r.resolveRule(ctx, client, &rule)
		if resErr != nil {
			return nil, resErr
		}

		live, found := liveMap[rule.Name]
		if !found {
			// Protection missing -> CREATE
			diff := r.buildCreateDiff(resolvedRule)
			allDiffs = append(allDiffs, diff)
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		} else {
			// Protection exists -> compare attributes
			diff := r.buildUpdateDiff(live, resolvedRule)
			if diff.HasChanges() {
				allDiffs = append(allDiffs, diff)
				if overallAction == ActionNoop {
					overallAction = ActionUpdate
				}
			}
		}
	}

	// Prune unmanaged protected tags
	if prune {
		for _, live := range liveTags {
			if live == nil {
				continue
			}
			if !managedNames[live.Name] {
				builder := NewDiffBuilder()
				builder.AddField("protected", true, false, ActionDelete)
				allDiffs = append(allDiffs, builder.Build(fmt.Sprintf("protected_tag:%s", live.Name), ActionDelete))
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

// Apply executes protected tags policy enforcement (live mutation).
func (r *ProtectedTagsReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.ProtectedTags == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	ptCfg := cfg.Policies.ProtectedTags
	prune := ptCfg.Prune != nil && *ptCfg.Prune
	if len(ptCfg.Rules) == 0 && !prune {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	liveTags, err := r.fetchAllLiveProtectedTags(ctx, client, project.ID)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	liveMap := make(map[string]*gogitlab.ProtectedTag)
	for _, t := range liveTags {
		if t != nil {
			liveMap[t.Name] = t
		}
	}

	var appliedDiffs []Diff
	overallAction := ActionNoop
	managedNames := make(map[string]bool)

	for _, rule := range ptCfg.Rules {
		managedNames[rule.Name] = true
		resolvedRule, resErr := r.resolveRule(ctx, client, &rule)
		if resErr != nil {
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, appliedDiffs, resErr, start), resErr
		}

		live, found := liveMap[rule.Name]
		if !found {
			// Protection missing -> CREATE
			diff := r.buildCreateDiff(resolvedRule)
			opt := r.toProtectOptions(resolvedRule)
			_, _, err := client.ProtectedTags().ProtectRepositoryTags(project.ID, opt, gogitlab.WithContext(ctx))
			if err != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, StatusFailed, appliedDiffs, fmt.Errorf("failed to protect tag '%s': %w", rule.Name, err), start), err
			}
			appliedDiffs = append(appliedDiffs, diff)
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		} else {
			// Protection exists -> compare attributes
			diff := r.buildUpdateDiff(live, resolvedRule)
			if diff.HasChanges() {
				// GitLab protected tags API requires unprotect + protect to update permissions
				_, _ = client.ProtectedTags().UnprotectRepositoryTags(project.ID, rule.Name, gogitlab.WithContext(ctx))
				opt := r.toProtectOptions(resolvedRule)
				_, _, err := client.ProtectedTags().ProtectRepositoryTags(project.ID, opt, gogitlab.WithContext(ctx))
				if err != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, appliedDiffs, fmt.Errorf("failed to update protected tag '%s': %w", rule.Name, err), start), err
				}
				appliedDiffs = append(appliedDiffs, diff)
				if overallAction == ActionNoop {
					overallAction = ActionUpdate
				}
			}
		}
	}

	// Prune unmanaged protected tags
	if prune {
		for _, live := range liveTags {
			if live == nil {
				continue
			}
			if !managedNames[live.Name] {
				_, err := client.ProtectedTags().UnprotectRepositoryTags(project.ID, live.Name, gogitlab.WithContext(ctx))
				if err != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionDelete, StatusFailed, appliedDiffs, fmt.Errorf("failed to unprotect tag '%s': %w", live.Name, err), start), err
				}
				builder := NewDiffBuilder()
				builder.AddField("protected", true, false, ActionDelete)
				appliedDiffs = append(appliedDiffs, builder.Build(fmt.Sprintf("protected_tag:%s", live.Name), ActionDelete))
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

// PlanGroup is a no-op for protected tags (tags exist at project level).
func (r *ProtectedTagsReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
}

// ApplyGroup is a no-op for protected tags (tags exist at project level).
func (r *ProtectedTagsReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
}

func (r *ProtectedTagsReconciler) fetchAllLiveProtectedTags(ctx context.Context, client gitlab.GitLabClient, projectID int) ([]*gogitlab.ProtectedTag, error) {
	var all []*gogitlab.ProtectedTag
	page := 1
	for {
		opts := &gogitlab.ListProtectedTagsOptions{
			Page:    page,
			PerPage: 100,
		}
		tags, resp, err := client.ProtectedTags().ListProtectedTags(projectID, opts, gogitlab.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		all = append(all, tags...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}

func (r *ProtectedTagsReconciler) resolveRule(ctx context.Context, client gitlab.GitLabClient, rule *config.ProtectedTagRuleConfig) (*config.ProtectedTagRuleConfig, error) {
	resolved := *rule
	resolved.AllowedToCreate = make([]config.TagAccessDescription, len(rule.AllowedToCreate))
	copy(resolved.AllowedToCreate, rule.AllowedToCreate)

	for i := range resolved.AllowedToCreate {
		access := &resolved.AllowedToCreate[i]
		if access.UserID == 0 && access.Username != "" && r.resolver != nil {
			uid, err := r.resolver.ResolveUsername(ctx, client, access.Username)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve username '%s' for tag rule '%s': %w", access.Username, rule.Name, err)
			}
			access.UserID = uid
		}
		if access.GroupID == 0 && access.GroupPath != "" && r.resolver != nil {
			gid, err := r.resolver.ResolveGroupPath(ctx, client, access.GroupPath)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve group path '%s' for tag rule '%s': %w", access.GroupPath, rule.Name, err)
			}
			access.GroupID = gid
		}
	}
	return &resolved, nil
}

func (r *ProtectedTagsReconciler) buildCreateDiff(rule *config.ProtectedTagRuleConfig) Diff {
	builder := NewDiffBuilder()
	builder.AddField("name", nil, rule.Name, ActionCreate)
	if len(rule.AllowedToCreate) > 0 {
		builder.AddField("allowed_to_create", nil, formatTagAccessDescriptions(rule.AllowedToCreate), ActionCreate)
	}
	return builder.Build(fmt.Sprintf("protected_tag:%s", rule.Name), ActionCreate)
}

func (r *ProtectedTagsReconciler) buildUpdateDiff(live *gogitlab.ProtectedTag, rule *config.ProtectedTagRuleConfig) Diff {
	builder := NewDiffBuilder()
	if len(rule.AllowedToCreate) > 0 {
		liveAccess := liveTagAccessToDescriptions(live.CreateAccessLevels)
		if !equalTagAccessDescriptions(liveAccess, rule.AllowedToCreate) {
			builder.AddField("allowed_to_create", formatTagAccessDescriptions(liveAccess), formatTagAccessDescriptions(rule.AllowedToCreate), ActionUpdate)
		}
	}
	return builder.Build(fmt.Sprintf("protected_tag:%s", rule.Name), ActionUpdate)
}

func (r *ProtectedTagsReconciler) toProtectOptions(rule *config.ProtectedTagRuleConfig) *gogitlab.ProtectRepositoryTagsOptions {
	opt := &gogitlab.ProtectRepositoryTagsOptions{
		Name: gogitlab.Ptr(rule.Name),
	}

	if len(rule.AllowedToCreate) == 1 && rule.AllowedToCreate[0].UserID == 0 && rule.AllowedToCreate[0].GroupID == 0 {
		// Single role-based access level
		al := gogitlab.AccessLevelValue(rule.AllowedToCreate[0].AccessLevel)
		opt.CreateAccessLevel = &al
	} else if len(rule.AllowedToCreate) > 0 {
		perms := make([]*gogitlab.TagsPermissionOptions, len(rule.AllowedToCreate))
		for i, a := range rule.AllowedToCreate {
			perm := &gogitlab.TagsPermissionOptions{}
			if a.AccessLevel != 0 {
				al := gogitlab.AccessLevelValue(a.AccessLevel)
				perm.AccessLevel = &al
			}
			if a.UserID != 0 {
				perm.UserID = gogitlab.Ptr(a.UserID)
			}
			if a.GroupID != 0 {
				perm.GroupID = gogitlab.Ptr(a.GroupID)
			}
			perms[i] = perm
		}
		opt.AllowedToCreate = &perms
	}
	return opt
}

func liveTagAccessToDescriptions(levels []*gogitlab.TagAccessDescription) []config.TagAccessDescription {
	var res []config.TagAccessDescription
	for _, l := range levels {
		if l == nil {
			continue
		}
		res = append(res, config.TagAccessDescription{
			AccessLevel: int(l.AccessLevel),
			UserID:      l.UserID,
			GroupID:     l.GroupID,
		})
	}
	return res
}

func equalTagAccessDescriptions(a, b []config.TagAccessDescription) bool {
	if len(a) != len(b) {
		return false
	}
	formatA := make([]string, len(a))
	formatB := make([]string, len(b))
	for i := range a {
		formatA[i] = fmt.Sprintf("role:%d/user:%d/group:%d", a[i].AccessLevel, a[i].UserID, a[i].GroupID)
	}
	for i := range b {
		formatB[i] = fmt.Sprintf("role:%d/user:%d/group:%d", b[i].AccessLevel, b[i].UserID, b[i].GroupID)
	}
	sort.Strings(formatA)
	sort.Strings(formatB)
	for i := range formatA {
		if formatA[i] != formatB[i] {
			return false
		}
	}
	return true
}

func formatTagAccessDescriptions(descriptions []config.TagAccessDescription) string {
	parts := make([]string, 0, len(descriptions))
	for _, d := range descriptions {
		if d.UserID != 0 {
			if d.Username != "" {
				parts = append(parts, fmt.Sprintf("user:%s", d.Username))
			} else {
				parts = append(parts, fmt.Sprintf("user:%d", d.UserID))
			}
		} else if d.GroupID != 0 {
			if d.GroupPath != "" {
				parts = append(parts, fmt.Sprintf("group:%s", d.GroupPath))
			} else {
				parts = append(parts, fmt.Sprintf("group:%d", d.GroupID))
			}
		} else {
			parts = append(parts, fmt.Sprintf("access_level:%d", d.AccessLevel))
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
