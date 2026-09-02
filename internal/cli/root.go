package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/divmora/gitlab-fleet-governor/internal/logging"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	"github.com/spf13/cobra"
)

// GlobalFlags holds all persistent global CLI flags.
type GlobalFlags struct {
	ConfigPath   string
	DryRun       bool
	Concurrency  int
	LogLevel     string
	LogFormat    string
	ReportFormat string
	OutputFile   string
	NoColor      bool
}

var globalFlags GlobalFlags

// NewRootCmd constructs and returns the root Cobra command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "gitlab-fleet-governor",
		Short: "GitLab Fleet Governor: Declarative policy-as-code and fleet governance engine",
		Long: `GitLab Fleet Governor (gitlab-fleet-governor) is a production-grade, declarative
policy-as-code and governance automation engine in Go for managing push rules,
protected branches, merge request approvals, project settings, CI/CD variables,
and pipeline retention across fleets of GitLab projects and groups.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Auto-detect NO_COLOR environment variable
			if os.Getenv("NO_COLOR") != "" {
				globalFlags.NoColor = true
			}

			// Validate log level
			if _, err := logging.ParseLevel(globalFlags.LogLevel); err != nil {
				return err
			}

			// Validate log format
			if _, err := logging.ParseFormat(globalFlags.LogFormat); err != nil {
				return err
			}

			// Validate report format
			if _, err := report.ParseFormat(globalFlags.ReportFormat); err != nil {
				return err
			}

			if globalFlags.Concurrency < 1 {
				return fmt.Errorf("concurrency must be at least 1 (got %d)", globalFlags.Concurrency)
			}

			// Initialize default slog logger
			logging.InitLogger(logging.Config{
				Level:   globalFlags.LogLevel,
				Format:  globalFlags.LogFormat,
				Output:  cmd.ErrOrStderr(),
				NoColor: globalFlags.NoColor,
			})

			return nil
		},
	}

	// Persistent Global Flags
	pflags := rootCmd.PersistentFlags()
	pflags.StringVarP(&globalFlags.ConfigPath, "config", "c", "", "Path to policy configuration file, stdin (-), or S3 URI (s3://bucket/key)")
	pflags.BoolVar(&globalFlags.DryRun, "dry-run", true, "Simulate governance changes without modifying remote GitLab resources")
	pflags.IntVar(&globalFlags.Concurrency, "concurrency", 10, "Maximum concurrent worker goroutines for group and project operations")
	pflags.StringVar(&globalFlags.LogLevel, "log-level", "info", "Logging verbosity level (debug, info, warn, error)")
	pflags.StringVar(&globalFlags.LogFormat, "log-format", "text", "Logging output format (text, json)")
	pflags.StringVar(&globalFlags.ReportFormat, "report-format", "table", "Summary report output format (table, json, csv, markdown, summary)")
	pflags.StringVarP(&globalFlags.OutputFile, "output-file", "o", "", "Destination file path for summary report (default: stdout)")
	pflags.BoolVar(&globalFlags.NoColor, "no-color", false, "Disable ANSI color formatting in output logs and tables")

	// Version string
	rootCmd.Version = version.Get().String()
	rootCmd.SetVersionTemplate("{{.Version}}\n")

	// Register subcommands
	rootCmd.AddCommand(newRunCmd())
	rootCmd.AddCommand(newValidateCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newLambdaCmd())

	return rootCmd
}

// Execute runs the root CLI command with the given context.
func Execute(ctx context.Context) error {
	cmd := NewRootCmd()
	return cmd.ExecuteContext(ctx)
}

func getOutputWriter(cmd *cobra.Command, outputFile string) (io.Writer, func(), error) {
	if outputFile == "" {
		return cmd.OutOrStdout(), func() {}, nil
	}
	f, err := os.Create(outputFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create output file '%s': %w", outputFile, err)
	}
	return f, func() { _ = f.Close() }, nil
}
