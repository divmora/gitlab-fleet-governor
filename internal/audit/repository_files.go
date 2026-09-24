package audit

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// RepositoryFilesAuditor evaluates repository files compliance for projects.
type RepositoryFilesAuditor struct{}

// NewRepositoryFilesAuditor constructs a new RepositoryFilesAuditor.
func NewRepositoryFilesAuditor() *RepositoryFilesAuditor {
	return &RepositoryFilesAuditor{}
}

// Name returns the canonical auditor identifier.
func (a *RepositoryFilesAuditor) Name() string {
	return string(ModuleRepositoryFiles)
}

// AuditProject evaluates a single project's repository files against policy.
func (a *RepositoryFilesAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject, cfg *config.PolicyConfig) ([]RepositoryFileFinding, error) {
	if client == nil || project == nil {
		return nil, nil
	}

	var findings []RepositoryFileFinding
	var lastActivity *time.Time
	webURL := ""
	if project.Raw != nil {
		webURL = project.Raw.WebURL
		if project.Raw.LastActivityAt != nil {
			t := time.Time(*project.Raw.LastActivityAt)
			lastActivity = &t
		}
	}

	stateStr, isArchived, isInactive := EvaluateProjectLifecycle(project.Archived, project.MarkedForDeletion, lastActivity)

	if cfg == nil || len(cfg.Policies.RepositoryFiles) == 0 {
		return nil, nil
	}
	fileConfigs := cfg.Policies.RepositoryFiles

	for _, fileCfg := range fileConfigs {
		targetBranch := fileCfg.TargetBranch
		if targetBranch == "" {
			targetBranch = project.DefaultBranch
			if targetBranch == "" {
				targetBranch = "main"
			}
		}

		raw, resp, err := client.RepositoryFiles().GetRawFile(project.ID, fileCfg.Path, &gitlab.GetRawFileOptions{Ref: gitlab.Ptr(targetBranch)})
		fileExists := true
		if err != nil {
			if (resp != nil && resp.StatusCode == 404) || strings.Contains(err.Error(), "404") {
				fileExists = false
			} else {
				return nil, fmt.Errorf("failed to fetch raw file %s: %w", fileCfg.Path, err)
			}
		}

		content := string(raw)

		if !fileExists {
			baseSev := SeverityHigh
			sev := AdjustSeverityForProject(baseSev, isArchived, isInactive)
			findings = append(findings, RepositoryFileFinding{
				ProjectID:     project.ID,
				ProjectName:   project.Name,
				ProjectPath:   project.PathWithNamespace,
				ProjectWebURL: webURL,
				ProjectStatus: stateStr,
				FilePath:      fileCfg.Path,
				TargetBranch:  targetBranch,
				FileExists:    false,
				Severity:      sev,
				ViolationType: "MISSING_FILE",
				Details:       fmt.Sprintf("Mandatory repository file '%s' is missing on branch '%s'.", fileCfg.Path, targetBranch),
				Remediation:   fmt.Sprintf("Run 'gitlab-fleet-governor run' to automatically synchronize %s to enterprise policy.", fileCfg.Path),
			})
			continue
		}

		// Check ensure_contains if configured
		if len(fileCfg.EnsureContains) > 0 {
			missing := make([]string, 0)
			for _, check := range fileCfg.EnsureContains {
				if !strings.Contains(content, check) {
					missing = append(missing, check)
				}
			}
			if len(missing) > 0 {
				baseSev := SeverityMedium
				sev := AdjustSeverityForProject(baseSev, isArchived, isInactive)
				findings = append(findings, RepositoryFileFinding{
					ProjectID:     project.ID,
					ProjectName:   project.Name,
					ProjectPath:   project.PathWithNamespace,
					ProjectWebURL: webURL,
					ProjectStatus: stateStr,
					FilePath:      fileCfg.Path,
					TargetBranch:  targetBranch,
					FileExists:    true,
					Severity:      sev,
					ViolationType: "MISSING_REQUIRED_STRINGS",
					Details:       fmt.Sprintf("Repository file '%s' is missing %d required substring(s): %s", fileCfg.Path, len(missing), strings.Join(missing, ", ")),
					Remediation:   fmt.Sprintf("Update %s to include required enterprise baseline configuration.", fileCfg.Path),
				})
				continue
			}
		}

		// Check full content if configured
		desiredRaw := ""
		if fileCfg.Content != "" {
			desiredRaw = fileCfg.Content
		} else if fileCfg.ContentFile != "" {
			loader := config.NewLoader()
			data, _, loadErr := loader.LoadRaw(ctx, fileCfg.ContentFile)
			if loadErr != nil {
				return nil, fmt.Errorf("failed to load content_file '%s' for audit: %w", fileCfg.ContentFile, loadErr)
			}
			desiredRaw = string(data)
		}

		if desiredRaw != "" {
			expanded, expErr := config.ExpandEnv(desiredRaw)
			if expErr == nil {
				desiredRaw = expanded
			}

			enforcement := strings.ToLower(fileCfg.Enforcement)
			if enforcement == "direct_commit" || enforcement == "" {
				desiredRaw = attachHeader(fileCfg.Path, desiredRaw)
			}

			if strings.TrimSpace(content) != strings.TrimSpace(desiredRaw) {
				baseSev := SeverityMedium
				sev := AdjustSeverityForProject(baseSev, isArchived, isInactive)
				findings = append(findings, RepositoryFileFinding{
					ProjectID:     project.ID,
					ProjectName:   project.Name,
					ProjectPath:   project.PathWithNamespace,
					ProjectWebURL: webURL,
					ProjectStatus: stateStr,
					FilePath:      fileCfg.Path,
					TargetBranch:  targetBranch,
					FileExists:    true,
					Severity:      sev,
					ViolationType: "CONTENT_DRIFT",
					Details:       fmt.Sprintf("Repository file '%s' has drifted from declarative enterprise policy.", fileCfg.Path),
					Remediation:   fmt.Sprintf("Run 'gitlab-fleet-governor run' to re-align %s with enterprise policy.", fileCfg.Path),
				})
			}
		}
	}

	return findings, nil
}

func attachHeader(filePath, content string) string {
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
