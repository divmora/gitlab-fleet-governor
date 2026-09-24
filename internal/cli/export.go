package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/export"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

type exportFlags struct {
	GroupPath                 string
	ProjectID                 int
	Recursive                 bool
	Output                    string
	IncludeSecretsPlaceholder bool
	SkipReconcilers           string
}

func newExportCmd() *cobra.Command {
	var flags exportFlags

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export live fleet configuration into a declarative policy configuration baseline",
		Long: `Export inspects the live configuration of a target GitLab group or project hierarchy
and emits a normalized declarative policy.yaml baseline that can be used for fleet governance.`,
		Example: `  # Export current state of a group hierarchy to policy.yaml
  gitlab-fleet-governor export --group-path "enterprise-fleet" --recursive --output baseline-policy.yaml

  # Export a single project
  gitlab-fleet-governor export --project-id 42 --output project-42-policy.yaml

  # Export to stdout (pipe to file)
  gitlab-fleet-governor export --group-path "platform/services" --recursive --output -`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return executeExport(ctx, cmd, flags)
		},
	}

	cmd.Flags().StringVar(&flags.GroupPath, "group-path", "", "Full path of the group to export")
	cmd.Flags().IntVar(&flags.ProjectID, "project-id", 0, "ID of a single project to export")
	cmd.Flags().BoolVar(&flags.Recursive, "recursive", true, "Include all subgroups when exporting a group")
	cmd.Flags().StringVar(&flags.Output, "output", "", "Output file path, - for stdout")
	cmd.Flags().BoolVar(&flags.IncludeSecretsPlaceholder, "include-secrets-placeholder", true, "Replace masked variable values with ${VAR:-PLACEHOLDER}")
	cmd.Flags().StringVar(&flags.SkipReconcilers, "skip-reconcilers", "", "Comma-separated list of reconcilers to skip (e.g., variables,webhooks)")

	return cmd
}

func executeExport(ctx context.Context, cmd *cobra.Command, flags exportFlags) error {
	if strings.TrimSpace(flags.GroupPath) == "" && flags.ProjectID <= 0 {
		return fmt.Errorf("either --group-path or --project-id must be specified")
	}

	var cfg *config.PolicyConfig
	if globalFlags.ConfigPath != "" {
		loadedCfg, sourceDesc, err := config.Load(ctx, globalFlags.ConfigPath, config.LoadOptions{
			LoaderOptions: []config.LoaderOption{config.WithStdin(cmd.InOrStdin())},
		})
		if err != nil {
			slog.Warn("Failed to load provided config file, using default settings", "source", sourceDesc, "error", err)
			cfg = &config.PolicyConfig{}
			cfg.SetDefaults()
		} else {
			cfg = loadedCfg
		}
	} else {
		cfg = &config.PolicyConfig{}
		cfg.SetDefaults()
	}

	// Override targets from CLI flags
	if strings.TrimSpace(flags.GroupPath) != "" {
		cfg.Targets.GroupSelector = &config.GroupSelector{
			GroupPathsInclude: []string{strings.TrimSpace(flags.GroupPath)},
			Recursive:         &flags.Recursive,
		}
		cfg.Targets.ProjectSelector = nil
	} else if flags.ProjectID > 0 {
		cfg.Targets.ProjectSelector = &config.ProjectSelector{
			IDRange: &config.IDRange{
				Min: flags.ProjectID,
				Max: flags.ProjectID,
			},
		}
		cfg.Targets.GroupSelector = nil
	}

	// Initialize GitLab Client
	client, err := gitlab.NewClientFromConfig(&cfg.Settings.GitLab)
	if err != nil {
		return fmt.Errorf("failed to initialize GitLab client: %w", err)
	}

	// Parse skip reconcilers
	skipSet := make(map[string]bool)
	if flags.SkipReconcilers != "" {
		for _, s := range strings.Split(flags.SkipReconcilers, ",") {
			trimmed := strings.ToLower(strings.TrimSpace(s))
			if trimmed != "" {
				skipSet[trimmed] = true
			}
		}
	}

	opts := export.ExportOptions{
		Client:                    client,
		Config:                    cfg,
		IncludeSecretsPlaceholder: flags.IncludeSecretsPlaceholder,
		SkipReconcilers:           skipSet,
		ErrOut:                    cmd.ErrOrStderr(),
	}

	yamlBytes, err := export.ExportFleetPolicy(ctx, opts)
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	// Determine output destination
	if flags.Output == "" || flags.Output == "-" {
		_, err := cmd.OutOrStdout().Write(yamlBytes)
		return err
	}

	if err := os.WriteFile(flags.Output, yamlBytes, 0644); err != nil {
		return fmt.Errorf("failed to write export output to file '%s': %w", flags.Output, err)
	}

	slog.Info("Successfully exported baseline policy", "output", flags.Output)
	return nil
}
