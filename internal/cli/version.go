package cli

import (
	"fmt"

	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	"github.com/spf13/cobra"
)

type versionFlags struct {
	JSON  bool
	Short bool
}

func newVersionCmd() *cobra.Command {
	var flags versionFlags

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build and version metadata",
		Long:  `Print comprehensive version metadata including semver release, git commit SHA, build date, Go compiler version, and platform architecture.`,
		Example: `  # Print standard version string
  gitlab-fleet-governor version

  # Print short version number only
  gitlab-fleet-governor version --short

  # Print machine-readable JSON metadata
  gitlab-fleet-governor version --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()

			if flags.JSON {
				jsonStr, err := info.JSON()
				if err != nil {
					return fmt.Errorf("failed to format version JSON: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonStr)
				return nil
			}

			if flags.Short {
				fmt.Fprintln(cmd.OutOrStdout(), info.Version)
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), info.String())
			return nil
		},
	}

	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output version metadata as formatted JSON")
	cmd.Flags().BoolVarP(&flags.Short, "short", "s", false, "Output semver version string only")

	return cmd
}
