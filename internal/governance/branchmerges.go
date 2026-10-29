package governance

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// BranchMergesReconciler implements GovernanceOperation for automated branch merges via MRs.
type BranchMergesReconciler struct{}

// NewBranchMergesReconciler instantiates a new BranchMergesReconciler.
func NewBranchMergesReconciler() *BranchMergesReconciler {
	return &BranchMergesReconciler{}
}

// Name returns the canonical operation identifier.
func (r *BranchMergesReconciler) Name() string {
	return "branch_merges"
}

// Order returns the execution order sequence (24).
func (r *BranchMergesReconciler) Order() int {
	return 24
}

// Plan evaluates project branch merges against policy config without state mutation.
func (r *BranchMergesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.BranchMerges) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	diffs := make([]Diff, 0)
	overallAction := ActionNoop

	for _, rule := range cfg.Policies.BranchMerges {
		srcBranch := strings.TrimSpace(rule.SourceBranch)
		tgtBranch := strings.TrimSpace(rule.TargetBranch)
		if srcBranch == "" || tgtBranch == "" {
			continue
		}

		// Inspect source branch
		src, _, srcErr := client.Branches().GetBranch(project.ID, srcBranch, gogitlab.WithContext(ctx))
		if srcErr != nil {
			return nil, fmt.Errorf("source branch '%s' not found on project %s: %w", srcBranch, project.PathWithNamespace, srcErr)
		}

		// Inspect target branch
		tgt, _, tgtErr := client.Branches().GetBranch(project.ID, tgtBranch, gogitlab.WithContext(ctx))
		if tgtErr != nil {
			return nil, fmt.Errorf("target branch '%s' not found on project %s: %w", tgtBranch, project.PathWithNamespace, tgtErr)
		}

		// Check if branches are at the same commit
		if src.Commit != nil && tgt.Commit != nil && src.Commit.ID == tgt.Commit.ID {
			continue
		}

		// Check for existing open MR
		mrs, _, listErr := client.MergeRequests().ListProjectMergeRequests(project.ID, &gogitlab.ListProjectMergeRequestsOptions{
			SourceBranch: gogitlab.Ptr(srcBranch),
			TargetBranch: gogitlab.Ptr(tgtBranch),
			State:        gogitlab.Ptr("opened"),
		}, gogitlab.WithContext(ctx))
		if listErr != nil {
			return nil, fmt.Errorf("failed to list merge requests on project %s: %w", project.PathWithNamespace, listErr)
		}

		builder := NewDiffBuilder()
		targetIdentifier := fmt.Sprintf("mr:%s->%s", srcBranch, tgtBranch)

		if len(mrs) > 0 {
			existingMR := mrs[0]
			hasConflict := existingMR.HasConflicts || existingMR.DetailedMergeStatus == "conflict"

			if hasConflict {
				builder.AddField("status", fmt.Sprintf("open_with_conflicts (!%d)", existingMR.IID), "leave_open_for_manual_resolution", ActionNoop)
				builder.AddField("mr_iid", existingMR.IID, existingMR.IID, ActionNoop)
				diffs = append(diffs, builder.Build(targetIdentifier, ActionNoop))
			} else {
				autoMerge := true
				if rule.AutoMerge != nil {
					autoMerge = *rule.AutoMerge
				}
				if autoMerge {
					builder.AddField("status", fmt.Sprintf("open_clean (!%d)", existingMR.IID), "merge", ActionUpdate)
					builder.AddField("mr_iid", existingMR.IID, existingMR.IID, ActionUpdate)
					diffs = append(diffs, builder.Build(targetIdentifier, ActionUpdate))
					if overallAction == ActionNoop {
						overallAction = ActionUpdate
					}
				} else {
					builder.AddField("status", fmt.Sprintf("open_clean (!%d)", existingMR.IID), "leave_open", ActionNoop)
					diffs = append(diffs, builder.Build(targetIdentifier, ActionNoop))
				}
			}
		} else {
			// No open MR exists; plan to create
			builder.AddField("source_branch", nil, srcBranch, ActionCreate)
			builder.AddField("target_branch", nil, tgtBranch, ActionCreate)
			autoMerge := true
			if rule.AutoMerge != nil {
				autoMerge = *rule.AutoMerge
			}
			if autoMerge {
				builder.AddField("action", nil, "create_mr_and_auto_merge_if_clean", ActionCreate)
			} else {
				builder.AddField("action", nil, "create_mr_only", ActionCreate)
			}
			diffs = append(diffs, builder.Build(targetIdentifier, ActionCreate))
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		}
	}

	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, diffs), nil
}

// Apply enforces project branch merges via Merge Requests.
func (r *BranchMergesReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || len(cfg.Policies.BranchMerges) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	diffs := make([]Diff, 0)
	overallAction := ActionNoop

	for _, rule := range cfg.Policies.BranchMerges {
		srcBranch := strings.TrimSpace(rule.SourceBranch)
		tgtBranch := strings.TrimSpace(rule.TargetBranch)
		if srcBranch == "" || tgtBranch == "" {
			continue
		}

		// Inspect source branch
		src, _, srcErr := client.Branches().GetBranch(project.ID, srcBranch, gogitlab.WithContext(ctx))
		if srcErr != nil {
			err := fmt.Errorf("source branch '%s' not found on project %s: %w", srcBranch, project.PathWithNamespace, srcErr)
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, err, start), err
		}

		// Inspect target branch
		tgt, _, tgtErr := client.Branches().GetBranch(project.ID, tgtBranch, gogitlab.WithContext(ctx))
		if tgtErr != nil {
			err := fmt.Errorf("target branch '%s' not found on project %s: %w", tgtBranch, project.PathWithNamespace, tgtErr)
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, err, start), err
		}

		// Check if branches are at the same commit
		if src.Commit != nil && tgt.Commit != nil && src.Commit.ID == tgt.Commit.ID {
			continue
		}

		// Check for existing open MR
		mrs, _, listErr := client.MergeRequests().ListProjectMergeRequests(project.ID, &gogitlab.ListProjectMergeRequestsOptions{
			SourceBranch: gogitlab.Ptr(srcBranch),
			TargetBranch: gogitlab.Ptr(tgtBranch),
			State:        gogitlab.Ptr("opened"),
		}, gogitlab.WithContext(ctx))
		if listErr != nil {
			err := fmt.Errorf("failed to list merge requests on project %s: %w", project.PathWithNamespace, listErr)
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, err, start), err
		}

		builder := NewDiffBuilder()
		targetIdentifier := fmt.Sprintf("mr:%s->%s", srcBranch, tgtBranch)

		autoMerge := true
		if rule.AutoMerge != nil {
			autoMerge = *rule.AutoMerge
		}

		var targetMR *gogitlab.MergeRequest

		if len(mrs) > 0 {
			targetMR = mrs[0]
			refreshedMR, _, getErr := client.MergeRequests().GetMergeRequest(project.ID, targetMR.IID, nil, gogitlab.WithContext(ctx))
			if getErr == nil && refreshedMR != nil {
				targetMR = refreshedMR
			}
		} else {
			// Create MR
			title := rule.Title
			if title == "" {
				title = fmt.Sprintf("Merge branch '%s' into '%s'", srcBranch, tgtBranch)
			}
			mrLabels := rule.Labels
			if len(mrLabels) == 0 {
				mrLabels = []string{"automated", "fleet-governor"}
			}

			createOpts := &gogitlab.CreateMergeRequestOptions{
				SourceBranch: gogitlab.Ptr(srcBranch),
				TargetBranch: gogitlab.Ptr(tgtBranch),
				Title:        gogitlab.Ptr(title),
				Labels:       gogitlab.Ptr(gogitlab.LabelOptions(mrLabels)),
			}
			if rule.Description != "" {
				createOpts.Description = gogitlab.Ptr(rule.Description)
			}
			if rule.RemoveSourceBranch != nil {
				createOpts.RemoveSourceBranch = rule.RemoveSourceBranch
			}
			if rule.Squash != nil {
				createOpts.Squash = rule.Squash
			}

			newMR, _, createErr := client.MergeRequests().CreateMergeRequest(project.ID, createOpts, gogitlab.WithContext(ctx))
			if createErr != nil {
				if strings.Contains(strings.ToLower(createErr.Error()), "no changes") {
					slog.Info("No commit differences between branches; skipping MR creation",
						"project", project.PathWithNamespace,
						"source", srcBranch,
						"target", tgtBranch,
					)
					continue
				}
				err := fmt.Errorf("failed to create merge request from '%s' to '%s' on project %s: %w", srcBranch, tgtBranch, project.PathWithNamespace, createErr)
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, err, start), err
			}
			targetMR = newMR
			builder.AddField("mr_created", nil, targetMR.IID, ActionCreate)
		}

		// Conflict Detection
		hasConflict := targetMR.HasConflicts || targetMR.DetailedMergeStatus == "conflict"

		if hasConflict {
			slog.Warn("Merge request has conflicts; leaving open for manual resolution",
				"project", project.PathWithNamespace,
				"mr", targetMR.IID,
				"source", srcBranch,
				"target", tgtBranch,
			)
			builder.AddField("status", "conflicts_detected", "left_open_for_manual_resolution", ActionNoop)
			builder.AddField("mr_iid", targetMR.IID, targetMR.IID, ActionNoop)
			builder.AddField("has_conflicts", nil, true, ActionNoop)
			diffs = append(diffs, builder.Build(targetIdentifier, ActionCreate))
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
			continue
		}

		// No conflicts detected
		if autoMerge {
			acceptOpts := &gogitlab.AcceptMergeRequestOptions{}
			if rule.MergeWhenPipelineSucceeds != nil {
				acceptOpts.MergeWhenPipelineSucceeds = rule.MergeWhenPipelineSucceeds
			} else {
				acceptOpts.MergeWhenPipelineSucceeds = gogitlab.Ptr(true)
			}
			if rule.RemoveSourceBranch != nil {
				acceptOpts.ShouldRemoveSourceBranch = rule.RemoveSourceBranch
			}
			if rule.Squash != nil {
				acceptOpts.Squash = rule.Squash
			}

			_, _, acceptErr := client.MergeRequests().AcceptMergeRequest(project.ID, targetMR.IID, acceptOpts, gogitlab.WithContext(ctx))
			if acceptErr != nil {
				slog.Warn("Failed to auto-merge merge request; leaving open",
					"project", project.PathWithNamespace,
					"mr", targetMR.IID,
					"error", acceptErr,
				)
				builder.AddField("status", "clean", fmt.Sprintf("auto_merge_error: %v (left open)", acceptErr), ActionNoop)
				builder.AddField("mr_iid", targetMR.IID, targetMR.IID, ActionNoop)
				diffs = append(diffs, builder.Build(targetIdentifier, ActionCreate))
				if overallAction == ActionNoop {
					overallAction = ActionCreate
				}
				continue
			}

			slog.Info("Successfully merged merge request",
				"project", project.PathWithNamespace,
				"mr", targetMR.IID,
				"source", srcBranch,
				"target", tgtBranch,
			)
			builder.AddField("status", "open", "merged", ActionUpdate)
			builder.AddField("mr_iid", targetMR.IID, targetMR.IID, ActionUpdate)
			diffs = append(diffs, builder.Build(targetIdentifier, ActionUpdate))
			if overallAction == ActionNoop {
				overallAction = ActionUpdate
			}
		} else {
			builder.AddField("status", "open", "left_open_auto_merge_disabled", ActionNoop)
			builder.AddField("mr_iid", targetMR.IID, targetMR.IID, ActionNoop)
			diffs = append(diffs, builder.Build(targetIdentifier, ActionCreate))
			if overallAction == ActionNoop {
				overallAction = ActionCreate
			}
		}
	}

	if len(diffs) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusSuccess, diffs, nil, start), nil
}

// PlanGroup returns skipped because branch merges only apply to projects.
func (r *BranchMergesReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Branch merges governance is not applicable to groups"), nil
}

// ApplyGroup returns skipped because branch merges only apply to projects.
func (r *BranchMergesReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Branch merges governance is not applicable to groups"), nil
}
