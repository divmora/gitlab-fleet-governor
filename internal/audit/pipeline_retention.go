package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// PipelineRetentionAuditor audits project pipeline retention policies and flags
// projects that have automatic deletion configured but still contain older, unpruned pipelines.
type PipelineRetentionAuditor struct{}

// NewPipelineRetentionAuditor instantiates a new pipeline retention auditor.
func NewPipelineRetentionAuditor() *PipelineRetentionAuditor {
	return &PipelineRetentionAuditor{}
}

// Name returns the module identifier.
func (a *PipelineRetentionAuditor) Name() string {
	return string(ModulePipelineRetention)
}

// AuditProject inspects pipeline retention settings and queries for stale pipelines.
func (a *PipelineRetentionAuditor) AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]PipelineRetentionFinding, error) {
	if client == nil || project == nil {
		return nil, nil
	}

	// 1. Evaluate project active/archived/inactive state
	var lastAct *time.Time
	var webURL string
	if project.Raw != nil {
		lastAct = project.Raw.LastActivityAt
		webURL = project.Raw.WebURL
	}
	projState, isArchived, isInactive := EvaluateProjectState(project.Archived, lastAct)

	// 2. Fetch project pipeline retention setting (ci_delete_pipelines_in_seconds)
	retentionSec, _, err := client.Projects().GetProjectPipelineRetention(project.ID, gitlab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline retention for project %s: %w", project.PathWithNamespace, err)
	}

	var findings []PipelineRetentionFinding

	if retentionSec > 0 {
		// Case A: Retention is configured (e.g. 7776000s = 90d, 2592000s = 30d)
		retentionDays := retentionSec / 86400
		cutoff := time.Now().Add(-time.Duration(retentionSec) * time.Second)

		opts := &gitlab.ListProjectPipelinesOptions{
			UpdatedBefore: &cutoff,
			OrderBy:       gitlab.Ptr("id"),
			Sort:          gitlab.Ptr("asc"),
			ListOptions: gitlab.ListOptions{
				Page:    1,
				PerPage: 20,
			},
		}

		pipelines, resp, err := client.Pipelines().ListProjectPipelines(project.ID, opts, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list project pipelines for project %s: %w", project.PathWithNamespace, err)
		}

		if len(pipelines) > 0 {
			oldest := pipelines[0]
			createdTime := oldest.CreatedAt
			if createdTime == nil {
				createdTime = oldest.UpdatedAt
			}

			oldestAgeDays := 0
			createdStr := "Unknown"
			if createdTime != nil {
				oldestAgeDays = int(time.Since(*createdTime).Hours() / 24)
				createdStr = createdTime.UTC().Format("2006-01-02 15:04:05 UTC")
			}

			overdueDays := oldestAgeDays - retentionDays
			if overdueDays < 0 {
				overdueDays = 0
			}

			staleCount := len(pipelines)
			if resp != nil && resp.TotalItems > 0 {
				staleCount = resp.TotalItems
			}

			baseSev := SeverityMedium
			if overdueDays > 60 {
				baseSev = SeverityHigh
			}
			sev := AdjustSeverityForProject(baseSev, isArchived, isInactive)

			findings = append(findings, PipelineRetentionFinding{
				ProjectID:              project.ID,
				ProjectName:            project.Name,
				ProjectPath:            project.PathWithNamespace,
				ProjectWebURL:          webURL,
				ProjectStatus:          projState,
				RetentionSeconds:       retentionSec,
				RetentionDays:          retentionDays,
				HasRetentionConfigured: true,
				OldestPipelineID:       oldest.ID,
				OldestPipelineRef:      oldest.Ref,
				OldestPipelineStatus:   oldest.Status,
				OldestPipelineCreated:  createdStr,
				OldestPipelineAgeDays:  oldestAgeDays,
				StalePipelinesCount:    staleCount,
				Severity:               sev,
				ViolationType:          "Retention Cleanup Failure",
				Details:                fmt.Sprintf("Pipeline retention configured to %dd (%ds), but %d stale pipeline(s) older than %dd still exist (oldest pipeline #%d created %s, %dd old, %dd past retention cutoff).", retentionDays, retentionSec, staleCount, retentionDays, oldest.ID, createdStr, oldestAgeDays, overdueDays),
				Remediation:            "GitLab background pruning worker is failing or queued. Trigger manual pipeline cleanup via API or re-save CI/CD retention settings.",
			})
		} else {
			// Compliant
			findings = append(findings, PipelineRetentionFinding{
				ProjectID:              project.ID,
				ProjectName:            project.Name,
				ProjectPath:            project.PathWithNamespace,
				ProjectWebURL:          webURL,
				ProjectStatus:          projState,
				RetentionSeconds:       retentionSec,
				RetentionDays:          retentionDays,
				HasRetentionConfigured: true,
				Severity:               SeverityPass,
				ViolationType:          "None",
				Details:                fmt.Sprintf("Pipeline retention active (%dd / %ds) and all stale pipelines pruned successfully.", retentionDays, retentionSec),
				Remediation:            "None - project conforms to retention policy.",
			})
		}
	} else {
		// Case B: Retention is disabled (ci_delete_pipelines_in_seconds == 0)
		cutoff := time.Now().Add(-90 * 24 * time.Hour)
		opts := &gitlab.ListProjectPipelinesOptions{
			UpdatedBefore: &cutoff,
			OrderBy:       gitlab.Ptr("id"),
			Sort:          gitlab.Ptr("asc"),
			ListOptions: gitlab.ListOptions{
				Page:    1,
				PerPage: 20,
			},
		}

		pipelines, resp, err := client.Pipelines().ListProjectPipelines(project.ID, opts, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list project pipelines for project %s: %w", project.PathWithNamespace, err)
		}

		if len(pipelines) > 0 {
			oldest := pipelines[0]
			createdTime := oldest.CreatedAt
			if createdTime == nil {
				createdTime = oldest.UpdatedAt
			}

			oldestAgeDays := 0
			createdStr := "Unknown"
			if createdTime != nil {
				oldestAgeDays = int(time.Since(*createdTime).Hours() / 24)
				createdStr = createdTime.UTC().Format("2006-01-02 15:04:05 UTC")
			}

			staleCount := len(pipelines)
			if resp != nil && resp.TotalItems > 0 {
				staleCount = resp.TotalItems
			}

			sev := AdjustSeverityForProject(SeverityLow, isArchived, isInactive)

			findings = append(findings, PipelineRetentionFinding{
				ProjectID:              project.ID,
				ProjectName:            project.Name,
				ProjectPath:            project.PathWithNamespace,
				ProjectWebURL:          webURL,
				ProjectStatus:          projState,
				RetentionSeconds:       0,
				RetentionDays:          0,
				HasRetentionConfigured: false,
				OldestPipelineID:       oldest.ID,
				OldestPipelineRef:      oldest.Ref,
				OldestPipelineStatus:   oldest.Status,
				OldestPipelineCreated:  createdStr,
				OldestPipelineAgeDays:  oldestAgeDays,
				StalePipelinesCount:    staleCount,
				Severity:               sev,
				ViolationType:          "Pipeline Retention Disabled",
				Details:                fmt.Sprintf("Pipeline deletion is disabled (0s); found %d pipeline(s) older than 90 days (oldest pipeline #%d created %s, %dd old). CI artifacts and job logs will accumulate indefinitely.", staleCount, oldest.ID, createdStr, oldestAgeDays),
				Remediation:            "Enable pipeline retention (e.g. ci_delete_pipelines_in_seconds = 7776000 for 90 days) in Project Settings -> CI/CD -> General pipelines.",
			})
		} else {
			// No stale pipelines found
			findings = append(findings, PipelineRetentionFinding{
				ProjectID:              project.ID,
				ProjectName:            project.Name,
				ProjectPath:            project.PathWithNamespace,
				ProjectWebURL:          webURL,
				ProjectStatus:          projState,
				RetentionSeconds:       0,
				RetentionDays:          0,
				HasRetentionConfigured: false,
				Severity:               SeverityPass,
				ViolationType:          "None",
				Details:                "Pipeline retention disabled, but no stale pipelines (>90d) detected.",
				Remediation:            "Consider configuring pipeline retention (e.g. 90 days) to prevent future artifact accumulation.",
			})
		}
	}

	return findings, nil
}
