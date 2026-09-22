package cli

import (
	"context"
	"encoding/json"
	"fmt"

	liblicense "github.com/divmora/license-go/pkg/license"
	"github.com/spf13/cobra"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
)

func newLicenseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "Manage and inspect commercial enterprise license tokens",
		Long: `Inspect, verify, and monitor Business Source License 1.1 entitlements,
fleet capacity, and commercial subscription status for GitLab Fleet Governor.

For centralized multi-product license management (inspections, offline node
requests, and status cards), install the official license-cli:
  go install github.com/divmora/license-go/cmd/license-cli@v1.3.1`,
	}

	cmd.AddCommand(newLicenseStatusCmd())
	cmd.AddCommand(newLicenseCheckCmd())

	return cmd
}

func newLicenseStatusCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display active license status, tier, expiration, and fleet capacity",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve license token
			token, err := resolveActiveLicenseToken(cmd.Context())
			if err != nil {
				return err
			}

			vInfo := version.Get()
			if vInfo.IsApacheConvertedNow() {
				changeDate, _ := vInfo.ChangeDate()
				if jsonOutput {
					out := map[string]interface{}{
						"status":      "apache_2_converted",
						"license":     "Apache-2.0",
						"valid":       true,
						"change_date": changeDate.Format("2006-01-02"),
						"message":     fmt.Sprintf("Version %s converted to Apache License 2.0 on %s under BSL 1.1 Change Date terms.", vInfo.Version, changeDate.Format("2006-01-02")),
					}
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(out)
				}

				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				fmt.Fprintln(cmd.OutOrStdout(), "GitLab Fleet Governor License Status")
				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				fmt.Fprintln(cmd.OutOrStdout(), "Active License   : Apache License, Version 2.0")
				fmt.Fprintf(cmd.OutOrStdout(), "Converted On     : %s (under BSL 1.1 Change Date terms)\n", changeDate.Format("2006-01-02"))
				fmt.Fprintln(cmd.OutOrStdout(), "Status           : 100% Free & Open Source (Zero capacity limits or token requirements)")
				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				return nil
			}

			if token == "" {
				changeDateStr := "Unknown (dev build)"
				if changeDate, ok := vInfo.ChangeDate(); ok {
					changeDateStr = changeDate.Format("2006-01-02")
				}

				if jsonOutput {
					out := map[string]interface{}{
						"status":           "community_tier",
						"tier":             "community",
						"license":          "BSL-1.1",
						"change_date":      changeDateStr,
						"free_tier_limit":  license.FreeTierMaxProjects,
						"license_required": false,
						"message":          "No commercial license configured. Running under free Community Tier (up to 25 production projects).",
					}
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(out)
				}

				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				fmt.Fprintln(cmd.OutOrStdout(), "GitLab Fleet Governor License Status")
				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				fmt.Fprintln(cmd.OutOrStdout(), "Active Tier      : Free Community Tier (BSL 1.1)")
				if relTime, ok := vInfo.ReleaseTime(); ok {
					fmt.Fprintf(cmd.OutOrStdout(), "Release Date     : %s\n", relTime.Format("2006-01-02"))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Change Date      : %s (Converts to Apache License 2.0)\n", changeDateStr)
				fmt.Fprintf(cmd.OutOrStdout(), "Production Quota : Up to %d managed projects/repositories\n", license.FreeTierMaxProjects)
				fmt.Fprintln(cmd.OutOrStdout(), "Non-Production   : Free and unrestricted (local dev, staging, QA, CI/CD dry-run)")
				fmt.Fprintln(cmd.OutOrStdout(), "Status           : ACTIVE (No commercial license required for <=25 projects)")
				fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
				fmt.Fprintln(cmd.OutOrStdout(), "\nTo configure a commercial license for larger fleets:")
				fmt.Fprintln(cmd.OutOrStdout(), "  export DIVMORA_LICENSE_KEY=\"<your-license-token>\"")
				fmt.Fprintln(cmd.OutOrStdout(), "  or visit https://divmora.com / contact licensing@divmora.com")
				fmt.Fprintln(cmd.OutOrStdout(), "\nFor centralized license management and air-gapped requests:")
				fmt.Fprintln(cmd.OutOrStdout(), "  go install github.com/divmora/license-go/cmd/license-cli@v1.3.1")
				fmt.Fprintln(cmd.OutOrStdout(), "  license-cli request -product \"gitlab-fleet-governor\" -customer \"<Company>\" -out ./node.divreq")
				return nil
			}

			crl := resolveActiveCRL(cmd.Context())
			status, err := license.ParseAndVerify(token, nil, crl)
			if err != nil {
				if jsonOutput {
					out := map[string]interface{}{
						"status":  "invalid",
						"valid":   false,
						"error":   err.Error(),
						"message": "Cryptographic verification failed: invalid, expired, or revoked license token.",
					}
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					_ = enc.Encode(out)
					return err
				}
				return fmt.Errorf("license verification failed: %w", err)
			}

			claims := status.Claims

			if jsonOutput {
				out := map[string]interface{}{
					"status":          "valid",
					"valid":           status.Valid,
					"in_grace_period": status.InGracePeriod,
					"days_remaining":  status.DaysRemaining,
					"message":         status.Message,
					"claims":          claims,
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			fmt.Fprint(cmd.OutOrStdout(), claims.FormatStatus(
				liblicense.WithStatusBannerTitle("GitLab Fleet Governor Commercial License Status"),
			))
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output license status in structured JSON format")
	return cmd
}

func newLicenseCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Perform headless verification of the active commercial license",
		RunE: func(cmd *cobra.Command, args []string) error {
			vInfo := version.Get()
			if vInfo.IsApacheConvertedNow() {
				changeDate, _ := vInfo.ChangeDate()
				fmt.Fprintf(cmd.OutOrStdout(), "OK: Version %s converted to Apache License 2.0 on %s (100%% unrestricted usage permitted)\n",
					vInfo.Version, changeDate.Format("2006-01-02"))
				return nil
			}

			token, err := resolveActiveLicenseToken(cmd.Context())
			if err != nil {
				return err
			}

			if token == "" {
				// Free community tier check
				fmt.Fprintln(cmd.OutOrStdout(), "OK: No commercial license provided (operating under Free Community Tier for <=25 projects)")
				return nil
			}

			crl := resolveActiveCRL(cmd.Context())
			status, err := license.ParseAndVerify(token, nil, crl)
			if err != nil {
				return fmt.Errorf("license check failed: %w", err)
			}

			if status.InGracePeriod {
				fmt.Fprintf(cmd.OutOrStdout(), "WARNING: %s\n", status.Message)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "OK: License %s is valid for %s (%s tier, %d days remaining)\n",
				status.Claims.ID, status.Claims.Customer.Name, license.GetClaimsPlan(status.Claims), status.DaysRemaining)
			return nil
		},
	}

	return cmd
}

func resolveActiveLicenseToken(ctx context.Context) (string, error) {
	if globalFlags.LicenseKey != "" || globalFlags.LicenseFile != "" {
		return license.ResolveToken(globalFlags.LicenseKey, globalFlags.LicenseFile)
	}

	// If a config file was specified, try loading it to check settings.license
	if globalFlags.ConfigPath != "" {
		if ctx == nil {
			ctx = context.Background()
		}
		cfg, _, err := config.Load(ctx, globalFlags.ConfigPath, config.LoadOptions{})
		if err == nil && cfg != nil {
			if cfg.Settings.License.Key != "" || cfg.Settings.License.File != "" {
				return license.ResolveToken(cfg.Settings.License.Key, cfg.Settings.License.File)
			}
		}
	}

	// Fallback to environment variables / default path
	return license.ResolveToken("", "")
}

func resolveActiveCRL(ctx context.Context) string {
	if globalFlags.LicenseCRLURL != "" {
		return globalFlags.LicenseCRLURL
	}
	if globalFlags.LicenseCRL != "" || globalFlags.LicenseCRLFile != "" {
		if crl, err := license.ResolveCRL(globalFlags.LicenseCRL, globalFlags.LicenseCRLFile); err == nil && crl != "" {
			return crl
		}
	}

	// If a config file was specified, try loading it to check settings.license
	if globalFlags.ConfigPath != "" {
		if ctx == nil {
			ctx = context.Background()
		}
		cfg, _, err := config.Load(ctx, globalFlags.ConfigPath, config.LoadOptions{})
		if err == nil && cfg != nil {
			if cfg.Settings.License.CRLURL != "" {
				return cfg.Settings.License.CRLURL
			}
			if cfg.Settings.License.CRL != "" || cfg.Settings.License.CRLFile != "" {
				if crl, err := license.ResolveCRL(cfg.Settings.License.CRL, cfg.Settings.License.CRLFile); err == nil && crl != "" {
					return crl
				}
			}
		}
	}

	// Fallback to environment variables / default path
	crl, _ := license.ResolveCRL()
	return crl
}
