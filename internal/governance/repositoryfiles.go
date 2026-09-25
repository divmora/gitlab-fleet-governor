package governance

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

var slugifyRegex = regexp.MustCompile(`-+`)

// RepositoryFilesReconciler implements GovernanceOperation for repository file synchronization.
type RepositoryFilesReconciler struct{}

// NewRepositoryFilesReconciler instantiates a new RepositoryFilesReconciler.
func NewRepositoryFilesReconciler() *RepositoryFilesReconciler {
	return &RepositoryFilesReconciler{}
}

// NewRepositoryFilesOperation creates a new RepositoryFilesReconciler instance.
func NewRepositoryFilesOperation() *RepositoryFilesReconciler {
	return NewRepositoryFilesReconciler()
}

// Name returns the canonical operation identifier.
func (r *RepositoryFilesReconciler) Name() string {
	return "repository_files"
}

// Order returns the execution order sequence (15).
func (r *RepositoryFilesReconciler) Order() int {
	return 15
}

// Plan evaluates project repository files against policy config without state mutation.
func (r *RepositoryFilesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.RepositoryFiles) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	var diffs []Diff
	overallAction := ActionNoop

	for _, fileCfg := range cfg.Policies.RepositoryFiles {
		cleanPath := strings.TrimPrefix(filepath.Clean(fileCfg.Path), "/")
		targetBranch := fileCfg.TargetBranch
		if targetBranch == "" {
			targetBranch = project.DefaultBranch
			if targetBranch == "" {
				targetBranch = "main"
			}
		}

		existingContent, fileExists, err := fetchFileContent(client, project.ID, cleanPath, targetBranch)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch raw file %s on branch %s: %w", cleanPath, targetBranch, err)
		}

		enforcement := strings.ToLower(fileCfg.Enforcement)
		if enforcement == "" {
			enforcement = "direct_commit"
		}

		desiredContent, hasDrift, _, err := calculateFileDrift(ctx, fileCfg, existingContent, fileExists, enforcement)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate drift for file %s: %w", cleanPath, err)
		}

		if hasDrift {
			if enforcement == "merge_request" {
				featureBranch := fmt.Sprintf("governance/sync-%s", slugifyPath(cleanPath))
				mrs, _, listErr := client.MergeRequests().ListProjectMergeRequests(project.ID, &gogitlab.ListProjectMergeRequestsOptions{
					SourceBranch: gogitlab.Ptr(featureBranch),
					TargetBranch: gogitlab.Ptr(targetBranch),
					State:        gogitlab.Ptr("opened"),
				})
				if listErr == nil && len(mrs) > 0 {
					featContent, featExists, _ := fetchFileContent(client, project.ID, cleanPath, featureBranch)
					if featExists && normalizeContent(featContent) == normalizeContent(desiredContent) {
						// Open MR already has the compliant file content and is pending review (0 drift / skipped)
						continue
					}
					// File on feature branch is outdated
					builder := NewDiffBuilder()
					if !featExists {
						builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
						diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s (MR !%d)", cleanPath, mrs[0].IID), ActionCreate))
					} else {
						builder.AddField("content", summarizeSnippet(featContent), summarizeSnippet(desiredContent), ActionUpdate)
						diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s (MR !%d)", cleanPath, mrs[0].IID), ActionUpdate))
					}
					if overallAction == ActionNoop {
						overallAction = ActionUpdate
					}
					continue
				}
			}

			builder := NewDiffBuilder()
			if !fileExists {
				builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionCreate))
				if overallAction == ActionNoop {
					overallAction = ActionCreate
				}
			} else {
				builder.AddField("content", summarizeSnippet(existingContent), summarizeSnippet(desiredContent), ActionUpdate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionUpdate))
				if overallAction == ActionNoop {
					overallAction = ActionUpdate
				}
			}
		}
	}

	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, diffs), nil
}

// Apply enforces repository file synchronization policies via direct commits or MRs.
func (r *RepositoryFilesReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || len(cfg.Policies.RepositoryFiles) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	var diffs []Diff
	overallAction := ActionNoop

	for _, fileCfg := range cfg.Policies.RepositoryFiles {
		cleanPath := strings.TrimPrefix(filepath.Clean(fileCfg.Path), "/")
		targetBranch := fileCfg.TargetBranch
		if targetBranch == "" {
			targetBranch = project.DefaultBranch
			if targetBranch == "" {
				targetBranch = "main"
			}
		}

		enforcement := strings.ToLower(fileCfg.Enforcement)
		if enforcement == "" {
			enforcement = "direct_commit"
		}

		existingContent, fileExists, err := fetchFileContent(client, project.ID, cleanPath, targetBranch)
		if err != nil {
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
		}

		desiredContent, hasDrift, action, err := calculateFileDrift(ctx, fileCfg, existingContent, fileExists, enforcement)
		if err != nil {
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
		}

		if !hasDrift {
			continue
		}

		if enforcement == "audit_only" {
			builder := NewDiffBuilder()
			if !fileExists {
				builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionCreate))
			} else {
				builder.AddField("content", summarizeSnippet(existingContent), summarizeSnippet(desiredContent), ActionUpdate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionUpdate))
			}
			if overallAction == ActionNoop {
				overallAction = action
			}
			continue
		}

		if enforcement == "direct_commit" {
			commitMsg := fmt.Sprintf("chore: sync %s to policy", cleanPath)
			builder := NewDiffBuilder()
			if !fileExists {
				builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
				opt := &gogitlab.CreateFileOptions{
					Branch:        gogitlab.Ptr(targetBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				}
				_, resp, createErr := client.RepositoryFiles().CreateFile(project.ID, cleanPath, opt)
				if createErr != nil {
					if isForbidden(resp, createErr) {
						actionableErr := fmt.Errorf("direct commit to branch %s failed with HTTP 403 Forbidden: push permissions are restricted; please use 'enforcement: merge_request' for protected branches: %w", targetBranch, createErr)
						return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, actionableErr, start), actionableErr
					}
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create file %s on %s: %w", cleanPath, targetBranch, createErr), start), createErr
				}
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionCreate))
			} else {
				builder.AddField("content", summarizeSnippet(existingContent), summarizeSnippet(desiredContent), ActionUpdate)
				opt := &gogitlab.UpdateFileOptions{
					Branch:        gogitlab.Ptr(targetBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				}
				_, resp, updateErr := client.RepositoryFiles().UpdateFile(project.ID, cleanPath, opt)
				if updateErr != nil {
					if isForbidden(resp, updateErr) {
						actionableErr := fmt.Errorf("direct commit to branch %s failed with HTTP 403 Forbidden: push permissions are restricted; please use 'enforcement: merge_request' for protected branches: %w", targetBranch, updateErr)
						return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, actionableErr, start), actionableErr
					}
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to update file %s on %s: %w", cleanPath, targetBranch, updateErr), start), updateErr
				}
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionUpdate))
			}
			if overallAction == ActionNoop {
				overallAction = action
			}
		} else if enforcement == "merge_request" {
			featureBranch := fmt.Sprintf("governance/sync-%s", slugifyPath(cleanPath))

			// Check for existing open Merge Request
			mrs, _, listErr := client.MergeRequests().ListProjectMergeRequests(project.ID, &gogitlab.ListProjectMergeRequestsOptions{
				SourceBranch: gogitlab.Ptr(featureBranch),
				TargetBranch: gogitlab.Ptr(targetBranch),
				State:        gogitlab.Ptr("opened"),
			})
			if listErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to list merge requests for branch %s: %w", featureBranch, listErr), start), listErr
			}

			if len(mrs) > 0 {
				featContent, featExists, _ := fetchFileContent(client, project.ID, cleanPath, featureBranch)
				if featExists && normalizeContent(featContent) == normalizeContent(desiredContent) {
					// Open MR already has compliant file content and is pending review (0 drift / skipped)
					continue
				}

				// Outdated on existing branch: update file on feature branch (PUT/POST) rather than opening duplicate MR
				commitMsg := fmt.Sprintf("chore: sync %s to policy", cleanPath)
				builder := NewDiffBuilder()
				if !featExists {
					builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
					diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s (MR !%d)", cleanPath, mrs[0].IID), ActionCreate))
					_, _, createErr := client.RepositoryFiles().CreateFile(project.ID, cleanPath, &gogitlab.CreateFileOptions{
						Branch:        gogitlab.Ptr(featureBranch),
						Content:       gogitlab.Ptr(desiredContent),
						CommitMessage: gogitlab.Ptr(commitMsg),
					})
					if createErr != nil {
						return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create file on feature branch %s: %w", featureBranch, createErr), start), createErr
					}
				} else {
					builder.AddField("content", summarizeSnippet(featContent), summarizeSnippet(desiredContent), ActionUpdate)
					diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s (MR !%d)", cleanPath, mrs[0].IID), ActionUpdate))
					_, _, updateErr := client.RepositoryFiles().UpdateFile(project.ID, cleanPath, &gogitlab.UpdateFileOptions{
						Branch:        gogitlab.Ptr(featureBranch),
						Content:       gogitlab.Ptr(desiredContent),
						CommitMessage: gogitlab.Ptr(commitMsg),
					})
					if updateErr != nil {
						return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to update file on feature branch %s: %w", featureBranch, updateErr), start), updateErr
					}
				}
				if overallAction == ActionNoop {
					overallAction = ActionUpdate
				}
				continue
			}

			// Ensure feature branch exists
			_, _, branchErr := client.Branches().GetBranch(project.ID, featureBranch)
			if branchErr != nil {
				_, _, createBranchErr := client.Branches().CreateBranch(project.ID, &gogitlab.CreateBranchOptions{
					Branch: gogitlab.Ptr(featureBranch),
					Ref:    gogitlab.Ptr(targetBranch),
				})
				if createBranchErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create feature branch %s: %w", featureBranch, createBranchErr), start), createBranchErr
				}
			}

			// Commit updated file to feature branch
			commitMsg := fmt.Sprintf("chore: sync %s to policy", cleanPath)
			featContent, featExists, _ := fetchFileContent(client, project.ID, cleanPath, featureBranch)
			builder := NewDiffBuilder()

			if !featExists {
				builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionCreate))
				_, _, createErr := client.RepositoryFiles().CreateFile(project.ID, cleanPath, &gogitlab.CreateFileOptions{
					Branch:        gogitlab.Ptr(featureBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				})
				if createErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create file on feature branch %s: %w", featureBranch, createErr), start), createErr
				}
			} else if normalizeContent(featContent) != normalizeContent(desiredContent) {
				builder.AddField("content", summarizeSnippet(featContent), summarizeSnippet(desiredContent), ActionUpdate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionUpdate))
				_, _, updateErr := client.RepositoryFiles().UpdateFile(project.ID, cleanPath, &gogitlab.UpdateFileOptions{
					Branch:        gogitlab.Ptr(featureBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				})
				if updateErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to update file on feature branch %s: %w", featureBranch, updateErr), start), updateErr
				}
			} else {
				builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", cleanPath), ActionCreate))
			}
			if overallAction == ActionNoop {
				overallAction = action
			}

			// Open MR
			mrTitle := fileCfg.MRTitle
			if mrTitle == "" {
				mrTitle = fmt.Sprintf("chore: sync %s to enterprise policy", cleanPath)
			}
			mrLabels := fileCfg.MRLabels
			if len(mrLabels) == 0 {
				mrLabels = []string{"automated", "governance"}
			}

			mrOpt := &gogitlab.CreateMergeRequestOptions{
				SourceBranch: gogitlab.Ptr(featureBranch),
				TargetBranch: gogitlab.Ptr(targetBranch),
				Title:        gogitlab.Ptr(mrTitle),
				Labels:       gogitlab.Ptr(gogitlab.LabelOptions(mrLabels)),
			}

			mr, _, createMRErr := client.MergeRequests().CreateMergeRequest(project.ID, mrOpt)
			if createMRErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create merge request for %s: %w", cleanPath, createMRErr), start), createMRErr
			}

			if fileCfg.AutoMerge != nil && *fileCfg.AutoMerge && mr != nil {
				_, _, _ = client.MergeRequests().AcceptMergeRequest(project.ID, mr.IID, &gogitlab.AcceptMergeRequestOptions{})
			}
		}
	}

	if len(diffs) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusSuccess, diffs, nil, start), nil
}

// PlanGroup repository files on group is a clean skipped operation.
func (r *RepositoryFilesReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Repository files governance is not applicable to groups"), nil
}

// ApplyGroup repository files on group is a clean skipped operation.
func (r *RepositoryFilesReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Repository files governance is not applicable to groups"), nil
}

func fetchFileContent(client gitlab.GitLabClient, projectID int, path, ref string) (string, bool, error) {
	raw, resp, err := client.RepositoryFiles().GetRawFile(projectID, path, &gogitlab.GetRawFileOptions{Ref: gogitlab.Ptr(ref)})
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return "", false, nil
		}
		if strings.Contains(err.Error(), "404") {
			return "", false, nil
		}
		return "", false, err
	}
	return string(raw), true, nil
}

func isForbidden(resp *gogitlab.Response, err error) bool {
	if resp != nil && resp.StatusCode == 403 {
		return true
	}
	if err != nil && (strings.Contains(err.Error(), "403") || strings.Contains(strings.ToLower(err.Error()), "forbidden")) {
		return true
	}
	return false
}

func normalizeContent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimRight(s, "\n")
	if len(s) > 0 {
		s += "\n"
	}
	return s
}

func calculateFileDrift(ctx context.Context, fileCfg config.RepositoryFileConfig, existing string, fileExists bool, enforcement string) (desired string, hasDrift bool, action ActionType, err error) {
	desiredRaw := ""
	if fileCfg.Content != "" {
		desiredRaw = fileCfg.Content
	} else if fileCfg.ContentFile != "" {
		loader := config.NewLoader()
		data, _, loadErr := loader.LoadRaw(ctx, fileCfg.ContentFile)
		if loadErr != nil {
			return "", false, ActionNoop, fmt.Errorf("failed to load content_file '%s': %w", fileCfg.ContentFile, loadErr)
		}
		desiredRaw = string(data)
	} else if len(fileCfg.EnsureContains) > 0 {
		if !fileExists {
			desiredRaw = strings.Join(fileCfg.EnsureContains, "\n") + "\n"
		} else {
			missing := make([]string, 0)
			normExisting := normalizeContent(existing)
			for _, check := range fileCfg.EnsureContains {
				normCheck := strings.ReplaceAll(check, "\r\n", "\n")
				if !strings.Contains(normExisting, normCheck) {
					missing = append(missing, check)
				}
			}
			if len(missing) == 0 {
				return existing, false, ActionNoop, nil
			}
			updated := existing
			if !strings.HasSuffix(updated, "\n") && len(updated) > 0 {
				updated += "\n"
			}
			updated += strings.Join(missing, "\n") + "\n"
			desiredRaw = updated
		}
	}

	// Expand environment variables in content
	expanded, expErr := config.ExpandEnv(desiredRaw)
	if expErr != nil {
		return "", false, ActionNoop, fmt.Errorf("failed to expand env vars in file content: %w", expErr)
	}
	desiredRaw = expanded

	// Add managed-by comment header for direct_commit only when full content is specified
	if enforcement == "direct_commit" && (fileCfg.Content != "" || fileCfg.ContentFile != "") {
		desiredRaw = attachManagedHeader(fileCfg.Path, desiredRaw)
	}

	desiredRaw = normalizeContent(desiredRaw)

	if !fileExists {
		return desiredRaw, true, ActionCreate, nil
	}

	if normalizeContent(existing) == desiredRaw {
		return existing, false, ActionNoop, nil
	}

	return desiredRaw, true, ActionUpdate, nil
}

func attachManagedHeader(filePath, content string) string {
	if strings.Contains(content, "Auto-managed by gitlab-fleet-governor") {
		return content
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	header := "# Enterprise policy\n# Auto-managed by gitlab-fleet-governor — do not edit manually\n"

	if ext == ".md" || ext == ".html" {
		header = "<!-- Auto-managed by gitlab-fleet-governor — do not edit manually -->\n"
	}

	return header + content
}

func slugifyPath(p string) string {
	p = strings.TrimPrefix(p, ".")
	var sb strings.Builder
	for _, r := range strings.ToLower(p) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	res := sb.String()
	res = strings.Trim(res, "-")
	return slugifyRegex.ReplaceAllString(res, "-")
}

func summarizeSnippet(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 5 {
		return s
	}
	return strings.Join(lines[:5], "\n") + fmt.Sprintf("\n... (%d total lines)", len(lines))
}
