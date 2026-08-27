package governance

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// WebhooksReconciler reconciles project webhook integrations.
type WebhooksReconciler struct {
	pruneUnmanaged bool
}

// NewWebhooksReconciler creates a new WebhooksReconciler instance.
func NewWebhooksReconciler(pruneUnmanaged ...bool) *WebhooksReconciler {
	prune := false
	if len(pruneUnmanaged) > 0 {
		prune = pruneUnmanaged[0]
	}
	return &WebhooksReconciler{pruneUnmanaged: prune}
}

// NewWebhooksOperation creates a WebhooksReconciler.
func NewWebhooksOperation(pruneUnmanaged ...bool) *WebhooksReconciler {
	return NewWebhooksReconciler(pruneUnmanaged...)
}

// Name returns the operation identifier.
func (r *WebhooksReconciler) Name() string {
	return "webhooks"
}

// Order returns the execution order sequence (90).
func (r *WebhooksReconciler) Order() int {
	return 90
}

// Plan evaluates webhooks drift for the target project.
func (r *WebhooksReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.Webhooks) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	diffs, err := r.calculateWebhooksDiffs(ctx, client, cfg.Policies.Webhooks, project)
	if err != nil {
		return nil, err
	}

	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, diffs), nil
}

// Apply applies webhook additions, modifications, and deletions.
func (r *WebhooksReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || len(cfg.Policies.Webhooks) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	plan, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !plan.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	existingHooks, _, err := client.Webhooks().ListProjectHooks(project.ID, &gogitlab.ListProjectHooksOptions{}, gogitlab.WithContext(ctx))
	if err != nil {
		applyErr := fmt.Errorf("failed to list project hooks for %d: %w", project.ID, err)
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
	}

	existingMap := make(map[string]*gogitlab.ProjectHook)
	for _, h := range existingHooks {
		if h != nil {
			normURL := normalizeWebhookURL(h.URL)
			existingMap[normURL] = h
		}
	}

	matchedHookIDs := make(map[int]bool)

	for _, desired := range cfg.Policies.Webhooks {
		normURL := normalizeWebhookURL(desired.URL)
		existing, found := existingMap[normURL]

		if !found {
			// CREATE
			addOpt := &gogitlab.AddProjectHookOptions{
				URL:                    gogitlab.Ptr(desired.URL),
				PushEvents:             desired.PushEvents,
				MergeRequestsEvents:    desired.MergeRequestsEvents,
				TagPushEvents:          desired.TagPushEvents,
				IssuesEvents:           desired.IssuesEvents,
				PipelineEvents:         desired.PipelineEvents,
				JobEvents:              desired.JobEvents,
				ReleasesEvents:         desired.ReleasesEvents,
				EnableSSLVerification:  desired.EnableSSLVerification,
			}
			if desired.PushEventsBranchFilter != "" {
				addOpt.PushEventsBranchFilter = gogitlab.Ptr(desired.PushEventsBranchFilter)
			}
			if desired.SecretToken != "" {
				addOpt.Token = gogitlab.Ptr(desired.SecretToken)
			}
			_, _, err := client.Webhooks().AddProjectHook(project.ID, addOpt, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to add webhook %s to project %d: %w", desired.URL, project.ID, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		} else {
			// UPDATE
			matchedHookIDs[existing.ID] = true
			editOpt := &gogitlab.EditProjectHookOptions{
				URL:                    gogitlab.Ptr(desired.URL),
				PushEvents:             desired.PushEvents,
				MergeRequestsEvents:    desired.MergeRequestsEvents,
				TagPushEvents:          desired.TagPushEvents,
				IssuesEvents:           desired.IssuesEvents,
				PipelineEvents:         desired.PipelineEvents,
				JobEvents:              desired.JobEvents,
				ReleasesEvents:         desired.ReleasesEvents,
				EnableSSLVerification:  desired.EnableSSLVerification,
			}
			if desired.PushEventsBranchFilter != "" {
				editOpt.PushEventsBranchFilter = gogitlab.Ptr(desired.PushEventsBranchFilter)
			}
			if desired.SecretToken != "" {
				editOpt.Token = gogitlab.Ptr(desired.SecretToken)
			}
			_, _, err := client.Webhooks().EditProjectHook(project.ID, existing.ID, editOpt, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to edit webhook %d for project %d: %w", existing.ID, project.ID, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		}
	}

	if r.pruneUnmanaged {
		for _, h := range existingHooks {
			if h == nil || matchedHookIDs[h.ID] {
				continue
			}
			_, err := client.Webhooks().DeleteProjectHook(project.ID, h.ID, gogitlab.WithContext(ctx))
			if err != nil {
				applyErr := fmt.Errorf("failed to delete unmanaged webhook %d: %w", h.ID, err)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionDelete, StatusFailed, plan.Diffs, applyErr, start), applyErr
			}
		}
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, plan.Diffs, nil, start), nil
}

// PlanGroup is a no-op as webhooks here are configured per project.
func (r *WebhooksReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Webhooks policy is not applicable to groups"), nil
}

// ApplyGroup is a no-op as webhooks here are configured per project.
func (r *WebhooksReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Webhooks policy is not applicable to groups"), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *WebhooksReconciler) calculateWebhooksDiffs(ctx context.Context, client gitlab.GitLabClient, desiredList []config.WebhookConfig, project *gogitlab.Project) ([]Diff, error) {
	var diffs []Diff

	existingHooks, _, err := client.Webhooks().ListProjectHooks(project.ID, &gogitlab.ListProjectHooksOptions{}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list project webhooks for %d: %w", project.ID, err)
	}

	existingMap := make(map[string]*gogitlab.ProjectHook)
	for _, h := range existingHooks {
		if h != nil {
			norm := normalizeWebhookURL(h.URL)
			existingMap[norm] = h
		}
	}

	matchedHookIDs := make(map[int]bool)

	for _, desired := range desiredList {
		norm := normalizeWebhookURL(desired.URL)
		existing, found := existingMap[norm]

		if !found {
			builder := NewDiffBuilder()
			builder.AddField("url", nil, desired.URL, ActionCreate)
			builder.SetDetails(fmt.Sprintf("Webhook %s to be created", desired.URL))
			diffs = append(diffs, builder.Build(fmt.Sprintf("webhook:%s", desired.URL), ActionCreate))
			continue
		}

		matchedHookIDs[existing.ID] = true
		builder := NewDiffBuilder()
		builder.Add(CompareBoolPtr("push_events", existing.PushEvents, desired.PushEvents))
		builder.Add(CompareBoolPtr("merge_requests_events", existing.MergeRequestsEvents, desired.MergeRequestsEvents))
		builder.Add(CompareBoolPtr("tag_push_events", existing.TagPushEvents, desired.TagPushEvents))
		builder.Add(CompareBoolPtr("issues_events", existing.IssuesEvents, desired.IssuesEvents))
		builder.Add(CompareBoolPtr("pipeline_events", existing.PipelineEvents, desired.PipelineEvents))
		builder.Add(CompareBoolPtr("job_events", existing.JobEvents, desired.JobEvents))
		builder.Add(CompareBoolPtr("releases_events", existing.ReleasesEvents, desired.ReleasesEvents))
		builder.Add(CompareBoolPtr("enable_ssl_verification", existing.EnableSSLVerification, desired.EnableSSLVerification))
		if desired.PushEventsBranchFilter != "" && existing.PushEventsBranchFilter != desired.PushEventsBranchFilter {
			builder.AddField("push_events_branch_filter", existing.PushEventsBranchFilter, desired.PushEventsBranchFilter, ActionUpdate)
		}

		if builder.HasChanges() {
			builder.SetDetails(fmt.Sprintf("Webhook %s has drift", desired.URL))
			diffs = append(diffs, builder.Build(fmt.Sprintf("webhook:%d", existing.ID), ActionUpdate))
		}
	}

	if r.pruneUnmanaged {
		for _, h := range existingHooks {
			if h == nil || matchedHookIDs[h.ID] {
				continue
			}
			builder := NewDiffBuilder()
			builder.AddField("url", h.URL, nil, ActionDelete)
			builder.SetDetails(fmt.Sprintf("Prune unmanaged webhook ID %d (%s)", h.ID, h.URL))
			diffs = append(diffs, builder.Build(fmt.Sprintf("webhook:%d", h.ID), ActionDelete))
		}
	}

	return diffs, nil
}

func normalizeWebhookURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimRight(strings.ToLower(raw), "/")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return strings.ToLower(u.String())
}
