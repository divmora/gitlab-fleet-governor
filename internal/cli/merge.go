package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
)

type mergeFlags struct {
	Project      string
	SourceBranch string
	TargetBranch string
	Title        string
	Description  string
	AutoMerge    bool
	JSON         bool
}

func newMergeCmd() *cobra.Command {
	var flags mergeFlags

	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge branch X into branch Y in a GitLab project using a Merge Request",
		Long: `Merge creates and accepts a Merge Request between two branches in a given GitLab project.
If conflicts are detected, the Merge Request is safely left open for manual resolution
without attempting to merge.`,
		Example: `  # Merge staging into main in dry-run mode (default)
  gitlab-fleet-governor merge --project platform/core --source staging --target main

  # Live apply branch merge with auto-merge enabled
  gitlab-fleet-governor merge --project 101 --source staging --target main --dry-run=false

  # Create MR but leave it open without auto-merging
  gitlab-fleet-governor merge --project platform/core --source staging --target main --auto-merge=false --dry-run=false`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeMerge(cmd.Context(), cmd, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Project, "project", "p", "", "Target project ID or path (e.g. '101' or 'group/project') (required)")
	cmd.Flags().StringVarP(&flags.SourceBranch, "source", "s", "", "Source branch to merge from (branch X) (required)")
	cmd.Flags().StringVarP(&flags.TargetBranch, "target", "t", "", "Target branch to merge into (branch Y) (required)")
	cmd.Flags().StringVar(&flags.Title, "title", "", "Custom merge request title (optional)")
	cmd.Flags().StringVar(&flags.Description, "description", "", "Custom merge request description (optional)")
	cmd.Flags().BoolVar(&flags.AutoMerge, "auto-merge", true, "Automatically accept/merge the MR if clean with no conflicts")
	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output execution outcome in JSON format")

	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("source")
	_ = cmd.MarkFlagRequired("target")

	return cmd
}

func executeMerge(ctx context.Context, cmd *cobra.Command, flags mergeFlags) error {
	projectPathOrID := strings.TrimSpace(flags.Project)
	sourceBranch := strings.TrimSpace(flags.SourceBranch)
	targetBranch := strings.TrimSpace(flags.TargetBranch)

	if projectPathOrID == "" {
		return fmt.Errorf("project is required")
	}
	if sourceBranch == "" {
		return fmt.Errorf("source branch is required")
	}
	if targetBranch == "" {
		return fmt.Errorf("target branch is required")
	}
	if sourceBranch == targetBranch {
		return fmt.Errorf("target branch cannot be identical to source branch ('%s')", sourceBranch)
	}

	dryRun := globalFlags.DryRun
	if cmd.Flags().Changed("dry-run") || cmd.InheritedFlags().Changed("dry-run") {
		dryRun = globalFlags.DryRun
	}

	var gitlabCfg config.GitLabSettingsConfig
	if globalFlags.ConfigPath != "" {
		cfg, _, err := config.Load(ctx, globalFlags.ConfigPath)
		if err == nil && cfg != nil {
			gitlabCfg = cfg.Settings.GitLab
		}
	}

	client, err := gitlab.NewClientFromConfig(&gitlabCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize GitLab client: %w", err)
	}

	proj, _, err := client.Projects().GetProject(projectPathOrID, &gogitlab.GetProjectOptions{}, gogitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to retrieve project '%s': %w", projectPathOrID, err)
	}

	policyCfg := &config.PolicyConfig{
		Version: "v1",
		Policies: config.PoliciesConfig{
			BranchMerges: []config.BranchMergeRuleConfig{
				{
					SourceBranch: sourceBranch,
					TargetBranch: targetBranch,
					Title:        flags.Title,
					Description:  flags.Description,
					AutoMerge:    &flags.AutoMerge,
				},
			},
		},
	}

	reconciler := governance.NewBranchMergesReconciler()

	if dryRun {
		planRes, planErr := reconciler.Plan(ctx, client, proj, policyCfg)
		if planErr != nil {
			return fmt.Errorf("merge plan failed: %w", planErr)
		}
		if flags.JSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"project":     proj.PathWithNamespace,
				"dry_run":     true,
				"action":      planRes.Action,
				"has_changes": planRes.HasChanges,
				"diffs":       planRes.Diffs,
			})
		}
		if !planRes.HasChanges {
			cmd.Printf("Project %s: branches '%s' and '%s' are already in sync (no-op).\n", proj.PathWithNamespace, sourceBranch, targetBranch)
			return nil
		}
		cmd.Printf("SIMULATION (Dry-Run): Would merge branch '%s' into '%s' on project %s.\n", sourceBranch, targetBranch, proj.PathWithNamespace)
		for _, d := range planRes.Diffs {
			for _, f := range d.Fields {
				cmd.Printf("  • %s: %v -> %v (%s)\n", f.Field, f.OldValue, f.NewValue, f.Action)
			}
		}
		return nil
	}

	applyRes, applyErr := reconciler.Apply(ctx, client, proj, policyCfg)
	if applyErr != nil {
		return fmt.Errorf("merge apply failed: %w", applyErr)
	}

	if flags.JSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"project": proj.PathWithNamespace,
			"dry_run": false,
			"action":  applyRes.Action,
			"status":  applyRes.Status,
			"success": applyRes.Success,
			"diffs":   applyRes.Diffs,
		})
	}

	if !applyRes.Success {
		return fmt.Errorf("branch merge failed: %v", applyRes.Error)
	}

	if applyRes.Action == governance.ActionNoop {
		cmd.Printf("Project %s: branches '%s' and '%s' are already in sync.\n", proj.PathWithNamespace, sourceBranch, targetBranch)
		return nil
	}

	cmd.Printf("Project %s: branch merge applied (%s).\n", proj.PathWithNamespace, applyRes.Status)
	for _, d := range applyRes.Diffs {
		for _, f := range d.Fields {
			cmd.Printf("  • %s: %v -> %v (%s)\n", f.Field, f.OldValue, f.NewValue, f.Action)
		}
	}

	return nil
}
