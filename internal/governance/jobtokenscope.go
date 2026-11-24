package governance

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// JobTokenScopeReconciler governs each project's CI/CD job token access
// settings: whether access is limited to an allowlist, and which projects
// and groups are on it.
type JobTokenScopeReconciler struct {
	// ids caches path-to-ID lookups for allowlist entries within a run.
	ids sync.Map
}

// NewJobTokenScopeReconciler instantiates a new JobTokenScopeReconciler.
func NewJobTokenScopeReconciler() *JobTokenScopeReconciler {
	return &JobTokenScopeReconciler{}
}

// Name returns the operation identifier.
func (r *JobTokenScopeReconciler) Name() string {
	return "job_token_scope"
}

// Order returns the execution order sequence (42).
func (r *JobTokenScopeReconciler) Order() int {
	return 42
}

// allowEntry is one allowlist entry, resolved to its ID.
type allowEntry struct {
	id   int64
	path string
}

// jobTokenPlan is the work needed to bring one project in line.
type jobTokenPlan struct {
	setLimit      *bool
	addProjects   []allowEntry
	dropProjects  []allowEntry
	addGroups     []allowEntry
	dropGroups    []allowEntry
	diff          Diff
	hasAnyChanges bool
}

// Plan compares the desired job token scope with the project's.
func (r *JobTokenScopeReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.JobTokenScope == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	plan, err := r.plan(ctx, client, project, cfg.Policies.JobTokenScope)
	if err != nil {
		return nil, err
	}
	if !plan.hasAnyChanges {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, []Diff{plan.diff}), nil
}

// Apply brings the project's job token scope in line with the policy.
func (r *JobTokenScopeReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.JobTokenScope == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	plan, err := r.plan(ctx, client, project, cfg.Policies.JobTokenScope)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}
	if !plan.hasAnyChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	fail := func(err error) (*ApplyResult, error) {
		applyErr := fmt.Errorf("failed to update job token scope for project %d: %w", project.ID, err)
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, []Diff{plan.diff}, applyErr, start), applyErr
	}
	svc := client.JobTokenScope()
	opt := gogitlab.WithContext(ctx)

	// Allowlist additions go first, so turning on the limit never locks out
	// a project the policy means to keep.
	for _, e := range plan.addProjects {
		if _, _, err := svc.AddProjectToJobScopeAllowList(project.ID, &gogitlab.JobTokenInboundAllowOptions{TargetProjectID: gogitlab.Ptr(e.id)}, opt); err != nil {
			return fail(fmt.Errorf("allow project %s: %w", e.path, err))
		}
	}
	for _, e := range plan.addGroups {
		if _, _, err := svc.AddGroupToJobTokenAllowlist(project.ID, &gogitlab.AddGroupToJobTokenAllowlistOptions{TargetGroupID: gogitlab.Ptr(e.id)}, opt); err != nil {
			return fail(fmt.Errorf("allow group %s: %w", e.path, err))
		}
	}
	if plan.setLimit != nil {
		if _, err := svc.PatchProjectJobTokenAccessSettings(project.ID, &gogitlab.PatchProjectJobTokenAccessSettingsOptions{Enabled: *plan.setLimit}, opt); err != nil {
			return fail(fmt.Errorf("set limit_access: %w", err))
		}
	}
	for _, e := range plan.dropProjects {
		if _, err := svc.RemoveProjectFromJobScopeAllowList(project.ID, e.id, opt); err != nil {
			return fail(fmt.Errorf("remove project %s: %w", e.path, err))
		}
	}
	for _, e := range plan.dropGroups {
		if _, err := svc.RemoveGroupFromJobTokenAllowlist(project.ID, e.id, opt); err != nil {
			return fail(fmt.Errorf("remove group %s: %w", e.path, err))
		}
	}
	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, []Diff{plan.diff}, nil, start), nil
}

// PlanGroup returns ActionSkipped as job token scope is project-scoped.
func (r *JobTokenScopeReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Job token scope is not applicable to groups"), nil
}

// ApplyGroup returns ActionSkipped as job token scope is project-scoped.
func (r *JobTokenScopeReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Job token scope is not applicable to groups"), nil
}

func (r *JobTokenScopeReconciler) plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, d *config.JobTokenScopeConfig) (*jobTokenPlan, error) {
	svc := client.JobTokenScope()
	opt := gogitlab.WithContext(ctx)
	b := NewDiffBuilder()
	plan := &jobTokenPlan{}
	prune := d.Prune != nil && *d.Prune

	if d.LimitAccess != nil {
		settings, _, err := svc.GetProjectJobTokenAccessSettings(project.ID, opt)
		if err != nil {
			return nil, fmt.Errorf("failed to get job token access settings for project %d: %w", project.ID, err)
		}
		if fd := CompareBoolPtr("limit_access", settings.InboundEnabled, d.LimitAccess); fd != nil {
			b.Add(fd)
			plan.setLimit = d.LimitAccess
		}
	}

	if d.AllowedProjects != nil {
		want, err := r.resolve(ctx, client, "project", *d.AllowedProjects)
		if err != nil {
			return nil, err
		}
		live, err := listAll(func(page int64) ([]*gogitlab.Project, *gogitlab.Response, error) {
			return svc.GetProjectJobTokenInboundAllowList(project.ID, &gogitlab.GetJobTokenInboundAllowListOptions{ListOptions: gogitlab.ListOptions{PerPage: 100, Page: page}}, opt)
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list job token allowlist for project %d: %w", project.ID, err)
		}
		liveEntries := make([]allowEntry, 0, len(live))
		for _, p := range live {
			// The project itself is always on its own allowlist.
			if p != nil && p.ID != project.ID {
				liveEntries = append(liveEntries, allowEntry{id: p.ID, path: p.PathWithNamespace})
			}
		}
		want = withoutID(want, project.ID)
		plan.addProjects, plan.dropProjects = diffEntries(liveEntries, want, prune)
		addEntryFields(b, "allowed_projects", plan.addProjects, plan.dropProjects)
	}

	if d.AllowedGroups != nil {
		want, err := r.resolve(ctx, client, "group", *d.AllowedGroups)
		if err != nil {
			return nil, err
		}
		live, err := listAll(func(page int64) ([]*gogitlab.Group, *gogitlab.Response, error) {
			return svc.GetJobTokenAllowlistGroups(project.ID, &gogitlab.GetJobTokenAllowlistGroupsOptions{ListOptions: gogitlab.ListOptions{PerPage: 100, Page: page}}, opt)
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list job token group allowlist for project %d: %w", project.ID, err)
		}
		liveEntries := make([]allowEntry, 0, len(live))
		for _, g := range live {
			if g != nil {
				liveEntries = append(liveEntries, allowEntry{id: g.ID, path: g.FullPath})
			}
		}
		plan.addGroups, plan.dropGroups = diffEntries(liveEntries, want, prune)
		addEntryFields(b, "allowed_groups", plan.addGroups, plan.dropGroups)
	}

	plan.diff = b.Build("job_token_scope", ActionUpdate)
	plan.hasAnyChanges = plan.diff.HasChanges()
	return plan, nil
}

// resolve turns allowlist references (full paths or numeric IDs) into IDs.
func (r *JobTokenScopeReconciler) resolve(ctx context.Context, client gitlab.GitLabClient, kind string, refs []string) ([]allowEntry, error) {
	out := make([]allowEntry, 0, len(refs))
	for _, raw := range refs {
		ref := strings.Trim(strings.TrimSpace(raw), "/")
		key := kind + ":" + strings.ToLower(ref)
		if cached, ok := r.ids.Load(key); ok {
			out = append(out, cached.(allowEntry))
			continue
		}

		var lookup any = ref
		if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
			lookup = id
		}
		var entry allowEntry
		if kind == "group" {
			g, _, err := client.Groups().GetGroup(lookup, nil, gogitlab.WithContext(ctx))
			if err != nil || g == nil {
				return nil, fmt.Errorf("job_token_scope: cannot resolve group %q: %w", ref, errOrNotFound(err))
			}
			entry = allowEntry{id: g.ID, path: g.FullPath}
		} else {
			p, _, err := client.Projects().GetProject(lookup, nil, gogitlab.WithContext(ctx))
			if err != nil || p == nil {
				return nil, fmt.Errorf("job_token_scope: cannot resolve project %q: %w", ref, errOrNotFound(err))
			}
			entry = allowEntry{id: p.ID, path: p.PathWithNamespace}
		}
		r.ids.Store(key, entry)
		out = append(out, entry)
	}
	return out, nil
}

func errOrNotFound(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("not found")
}

func withoutID(entries []allowEntry, id int64) []allowEntry {
	out := entries[:0:0]
	for _, e := range entries {
		if e.id != id {
			out = append(out, e)
		}
	}
	return out
}

// diffEntries returns the wanted entries missing from live and, when
// pruning, the live entries that aren't wanted.
func diffEntries(live, want []allowEntry, prune bool) (add, drop []allowEntry) {
	liveIDs := map[int64]bool{}
	for _, e := range live {
		liveIDs[e.id] = true
	}
	wantIDs := map[int64]bool{}
	for _, e := range want {
		if !wantIDs[e.id] && !liveIDs[e.id] {
			add = append(add, e)
		}
		wantIDs[e.id] = true
	}
	if prune {
		for _, e := range live {
			if !wantIDs[e.id] {
				drop = append(drop, e)
			}
		}
	}
	sort.Slice(add, func(i, j int) bool { return add[i].path < add[j].path })
	sort.Slice(drop, func(i, j int) bool { return drop[i].path < drop[j].path })
	return add, drop
}

func addEntryFields(b *DiffBuilder, field string, add, drop []allowEntry) {
	for _, e := range add {
		b.AddField(field, nil, e.path, ActionCreate)
	}
	for _, e := range drop {
		b.AddField(field, e.path, nil, ActionDelete)
	}
}

// listAll walks every page of a paginated list call.
func listAll[T any](fetch func(page int64) ([]T, *gogitlab.Response, error)) ([]T, error) {
	var all []T
	page := int64(1)
	for {
		items, resp, err := fetch(page)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if resp == nil || resp.NextPage == 0 || resp.NextPage <= page {
			return all, nil
		}
		page = resp.NextPage
	}
}
