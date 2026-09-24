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

// Order returns the execution order sequence (55).
func (r *RepositoryFilesReconciler) Order() int {
	return 55
}

// Plan evaluates project repository files against policy config without state mutation.
func (r *RepositoryFilesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || len(cfg.Policies.RepositoryFiles) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	var diffs []Diff
	overallAction := ActionNoop

	for _, fileCfg := range cfg.Policies.RepositoryFiles {
		targetBranch := fileCfg.TargetBranch
		if targetBranch == "" {
			targetBranch = project.DefaultBranch
			if targetBranch == "" {
				targetBranch = "main"
			}
		}

		existingContent, fileExists, err := fetchFileContent(client, project.ID, fileCfg.Path, targetBranch)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch raw file %s on branch %s: %w", fileCfg.Path, targetBranch, err)
		}

		enforcement := strings.ToLower(fileCfg.Enforcement)
		if enforcement == "" {
			enforcement = "direct_commit"
		}

		desiredContent, hasDrift, _, err := calculateFileDrift(ctx, fileCfg, existingContent, fileExists, enforcement)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate drift for file %s: %w", fileCfg.Path, err)
		}

		if hasDrift {
			builder := NewDiffBuilder()
			if !fileExists {
				builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", fileCfg.Path), ActionCreate))
				if overallAction == ActionNoop {
					overallAction = ActionCreate
				}
			} else {
				builder.AddField("content", summarizeSnippet(existingContent), summarizeSnippet(desiredContent), ActionUpdate)
				diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", fileCfg.Path), ActionUpdate))
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

		existingContent, fileExists, err := fetchFileContent(client, project.ID, fileCfg.Path, targetBranch)
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

		builder := NewDiffBuilder()
		if !fileExists {
			builder.AddField("content", nil, summarizeSnippet(desiredContent), ActionCreate)
			diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", fileCfg.Path), ActionCreate))
		} else {
			builder.AddField("content", summarizeSnippet(existingContent), summarizeSnippet(desiredContent), ActionUpdate)
			diffs = append(diffs, builder.Build(fmt.Sprintf("file:%s", fileCfg.Path), ActionUpdate))
		}
		if overallAction == ActionNoop {
			overallAction = action
		}

		if enforcement == "audit_only" {
			continue
		}

		if enforcement == "direct_commit" {
			commitMsg := fmt.Sprintf("chore: sync %s to policy", fileCfg.Path)
			if !fileExists {
				opt := &gogitlab.CreateFileOptions{
					Branch:        gogitlab.Ptr(targetBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				}
				_, _, createErr := client.RepositoryFiles().CreateFile(project.ID, fileCfg.Path, opt)
				if createErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create file %s on %s: %w", fileCfg.Path, targetBranch, createErr), start), createErr
				}
			} else {
				opt := &gogitlab.UpdateFileOptions{
					Branch:        gogitlab.Ptr(targetBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				}
				_, _, updateErr := client.RepositoryFiles().UpdateFile(project.ID, fileCfg.Path, opt)
				if updateErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to update file %s on %s: %w", fileCfg.Path, targetBranch, updateErr), start), updateErr
				}
			}
		} else if enforcement == "merge_request" {
			featureBranch := fmt.Sprintf("governance/sync-%s", slugifyPath(fileCfg.Path))

			// 1. Ensure feature branch exists
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

			// 2. Commit updated file to feature branch if content differs
			commitMsg := fmt.Sprintf("chore: sync %s to policy", fileCfg.Path)
			featContent, featExists, _ := fetchFileContent(client, project.ID, fileCfg.Path, featureBranch)

			if !featExists {
				_, _, createErr := client.RepositoryFiles().CreateFile(project.ID, fileCfg.Path, &gogitlab.CreateFileOptions{
					Branch:        gogitlab.Ptr(featureBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				})
				if createErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create file on feature branch %s: %w", featureBranch, createErr), start), createErr
				}
			} else if strings.TrimSpace(featContent) != strings.TrimSpace(desiredContent) {
				_, _, updateErr := client.RepositoryFiles().UpdateFile(project.ID, fileCfg.Path, &gogitlab.UpdateFileOptions{
					Branch:        gogitlab.Ptr(featureBranch),
					Content:       gogitlab.Ptr(desiredContent),
					CommitMessage: gogitlab.Ptr(commitMsg),
				})
				if updateErr != nil {
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to update file on feature branch %s: %w", featureBranch, updateErr), start), updateErr
				}
			}

			// 3. Check for existing open Merge Request
			mrs, _, listErr := client.MergeRequests().ListProjectMergeRequests(project.ID, &gogitlab.ListProjectMergeRequestsOptions{
				SourceBranch: gogitlab.Ptr(featureBranch),
				TargetBranch: gogitlab.Ptr(targetBranch),
				State:        gogitlab.Ptr("opened"),
			})
			if listErr != nil {
				return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to list merge requests for branch %s: %w", featureBranch, listErr), start), listErr
			}

			if len(mrs) == 0 {
				// Create MR
				mrTitle := fileCfg.MRTitle
				if mrTitle == "" {
					mrTitle = fmt.Sprintf("chore: sync %s to enterprise policy", fileCfg.Path)
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
					return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, overallAction, StatusFailed, diffs, fmt.Errorf("failed to create merge request for %s: %w", fileCfg.Path, createMRErr), start), createMRErr
				}

				if fileCfg.AutoMerge != nil && *fileCfg.AutoMerge && mr != nil {
					_, _, _ = client.MergeRequests().AcceptMergeRequest(project.ID, mr.IID, &gogitlab.AcceptMergeRequestOptions{})
				}
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
			for _, check := range fileCfg.EnsureContains {
				if !strings.Contains(existing, check) {
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

	// Add managed-by comment header for direct_commit if not present
	if enforcement == "direct_commit" {
		desiredRaw = attachManagedHeader(fileCfg.Path, desiredRaw)
	}

	if !fileExists {
		return desiredRaw, true, ActionCreate, nil
	}

	if strings.TrimSpace(existing) == strings.TrimSpace(desiredRaw) {
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
