package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/spf13/cobra"
)

type runFlags struct {
	IncludeDiffs bool
	Timeout      time.Duration
}

func newRunCmd() *cobra.Command {
	var flags runFlags

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute fleet governance policies across target groups and projects",
		Long: `Run discovers target groups and projects according to selector rules, evaluates
governance policies (push rules, protected branches, merge request approvals,
project settings, pipeline retention, CI/CD variables, runners, compliance,
webhooks, and members), plans or applies changes, and renders a summary report.`,
		Example: `  # Dry-run policy execution with table report (default)
  gitlab-fleet-governor run -c config.yaml

  # Live apply policy changes with 20 parallel workers
  gitlab-fleet-governor run -c config.yaml --dry-run=false --concurrency=20

  # Run policies from S3 and export Markdown report to a file
  gitlab-fleet-governor run -c s3://my-bucket/policies.yaml --report-format markdown -o report.md

  # Pipe config from stdin with JSON report output
  cat policy.json | gitlab-fleet-governor run -c - --report-format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if flags.Timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, flags.Timeout)
				defer cancel()
			}

			return executeRun(ctx, cmd, flags)
		},
	}

	cmd.Flags().BoolVar(&flags.IncludeDiffs, "include-diffs", true, "Include granular field-level diffs in summary report")
	cmd.Flags().DurationVar(&flags.Timeout, "timeout", 0, "Global execution timeout duration (e.g. 15m, 1h; default: no timeout)")

	return cmd
}

func executeRun(ctx context.Context, cmd *cobra.Command, flags runFlags) error {
	// 1. Ingest, substitute env vars, unmarshal and validate configuration
	cfg, sourceDesc, err := config.Load(ctx, globalFlags.ConfigPath, config.LoadOptions{
		LoaderOptions: []config.LoaderOption{config.WithStdin(cmd.InOrStdin())},
	})
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// 2. Apply CLI Flag Overrides
	if cmd.Flags().Changed("dry-run") || cmd.InheritedFlags().Changed("dry-run") {
		cfg.Settings.DryRun = &globalFlags.DryRun
	}
	if cmd.Flags().Changed("concurrency") || cmd.InheritedFlags().Changed("concurrency") {
		cfg.Settings.Concurrency = globalFlags.Concurrency
	}
	if cmd.Flags().Changed("report-format") || cmd.InheritedFlags().Changed("report-format") {
		cfg.Settings.ReportFormat = globalFlags.ReportFormat
	}

	dryRun := true
	if cfg.Settings.DryRun != nil {
		dryRun = *cfg.Settings.DryRun
	}
	concurrency := cfg.Settings.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	slog.Info("Configuration loaded successfully",
		"source", sourceDesc,
		"dry_run", dryRun,
		"concurrency", concurrency,
	)

	// 3. Construct resilient GitLab API client
	client, err := gitlab.NewClientFromConfig(&cfg.Settings.GitLab)
	if err != nil {
		return fmt.Errorf("failed to initialize GitLab client: %w", err)
	}

	// 4. Initialize Governance Engine
	eng, err := engine.NewGovernanceEngine(client, cfg,
		engine.WithConcurrency(concurrency),
		engine.WithDryRun(dryRun),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize governance engine: %w", err)
	}

	// 5. Execute Fleet Discovery & Policy Reconciliation
	slog.Info("Executing fleet governance reconciliation",
		"dry_run", dryRun,
		"concurrency", concurrency,
	)

	result, execErr := eng.Execute(ctx, cfg)

	// 6. Convert execution result to canonical report DTO
	reportData := report.FromExecutionResult(result)

	// 7. Render Report to target writer
	outWriter, cleanup, outErr := getOutputWriter(cmd, globalFlags.OutputFile)
	if outErr != nil {
		return outErr
	}
	defer cleanup()

	reportFmt, err := report.ParseFormat(globalFlags.ReportFormat)
	if err != nil {
		reportFmt = report.FormatTable
	}

	reporter, err := report.NewReporter(reportFmt, outWriter,
		report.WithColor(!globalFlags.NoColor),
		report.WithDiffs(flags.IncludeDiffs),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize reporter: %w", err)
	}

	if renderErr := reporter.Render(reportData); renderErr != nil {
		slog.Error("Failed to render execution report", "error", renderErr)
	}

	if execErr != nil {
		return fmt.Errorf("governance execution failed: %w", execErr)
	}

	if result != nil && result.HasErrors() {
		return fmt.Errorf("governance execution completed with %d error(s)", len(result.Errors)+result.Metrics.TotalFailed)
	}

	return nil
}
