package cli

import (
	"fmt"

	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	"github.com/spf13/cobra"
)

type versionFlags struct {
	JSON   bool
	Short  bool
	Verify bool
}

func newVersionCmd() *cobra.Command {
	var flags versionFlags

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build and version metadata",
		Long: `Print comprehensive version metadata including semver release, git commit SHA,
build date, Go compiler version, platform architecture, and Layer 3 cryptographic release provenance.`,
		Example: `  # Print standard version string
  gitlab-fleet-governor version

  # Print short version number only
  gitlab-fleet-governor version --short

  # Verify cryptographic release provenance
  gitlab-fleet-governor version --verify

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
				if flags.Verify && !info.Provenance.Verified {
					return fmt.Errorf("cryptographic release provenance unverified: %s", info.Provenance.Status)
				}
				return nil
			}

			if flags.Short {
				fmt.Fprintln(cmd.OutOrStdout(), info.Version)
				return nil
			}

			if flags.Verify {
				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				fmt.Fprintln(cmd.OutOrStdout(), "GitLab Fleet Governor: Cryptographic Release Provenance (Layer 3)")
				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				fmt.Fprintf(cmd.OutOrStdout(), "Version         : %s\n", info.Version)
				fmt.Fprintf(cmd.OutOrStdout(), "Git Commit      : %s\n", info.GitCommit)
				fmt.Fprintf(cmd.OutOrStdout(), "Build Date      : %s\n", info.BuildDate)
				fmt.Fprintf(cmd.OutOrStdout(), "Go Version      : %s\n", info.GoVersion)
				fmt.Fprintf(cmd.OutOrStdout(), "Platform        : %s\n", info.Platform)
				fmt.Fprintf(cmd.OutOrStdout(), "Provenance      : %s\n", info.Provenance.Status)
				if info.Provenance.Verified {
					fmt.Fprintf(cmd.OutOrStdout(), "Release Signer  : %s\n", info.Provenance.Authority)
					if info.Provenance.Source != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "Signature Source: %s\n", info.Provenance.Source)
					}
					if changeDate, ok := info.ChangeDate(); ok {
						fmt.Fprintf(cmd.OutOrStdout(), "Apache 2.0 Date : %s (Converts under BSL 1.1 Change Date terms)\n", changeDate.Format("2006-01-02"))
					}
					fmt.Fprintln(cmd.OutOrStdout(), "Verification    : PASSED (Cryptographically verified official release)")
					fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
					return nil
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Verification    : UNVERIFIED (%s)\n", info.Provenance.Error)
				fmt.Fprintln(cmd.OutOrStdout(), "Governing Terms : Standard Business Source License 1.1 (Custom/unattested build)")
				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				return fmt.Errorf("cryptographic release provenance unverified: %s", info.Provenance.Status)
			}

			fmt.Fprintln(cmd.OutOrStdout(), info.String())
			return nil
		},
	}

	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output version metadata as formatted JSON")
	cmd.Flags().BoolVarP(&flags.Short, "short", "s", false, "Output semver version string only")
	cmd.Flags().BoolVar(&flags.Verify, "verify", false, "Inspect and verify cryptographic release provenance (Layer 3)")

	return cmd
}
