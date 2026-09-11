package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "fleet-license-gen",
		Short: "DIVMORA Technologies: Fleet Governor License Minting & Verification Utility",
		Long: `Administrative utility for generating Ed25519 keypairs, issuing signed commercial
customer license tokens, signing release metadata envelopes, and inspecting token claims for
GitLab Fleet Governor under Business Source License 1.1 terms.`,
	}

	rootCmd.AddCommand(newInitKeysCmd())
	rootCmd.AddCommand(newIssueCmd())
	rootCmd.AddCommand(newInspectCmd())
	rootCmd.AddCommand(newSignReleaseCmd())
	rootCmd.AddCommand(newVerifyReleaseCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newInitKeysCmd() *cobra.Command {
	var outDir string

	cmd := &cobra.Command{
		Use:   "init-keys",
		Short: "Generate a fresh Ed25519 cryptographic keypair",
		RunE: func(cmd *cobra.Command, args []string) error {
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return fmt.Errorf("failed to generate Ed25519 keypair: %w", err)
			}

			pubB64 := base64.StdEncoding.EncodeToString(pub)
			privB64 := base64.StdEncoding.EncodeToString(priv)

			if outDir != "" {
				if err := os.MkdirAll(outDir, 0700); err != nil {
					return fmt.Errorf("failed to create output directory: %w", err)
				}
				pubPath := filepath.Join(outDir, "divmora-public.key")
				privPath := filepath.Join(outDir, "divmora-private.key")

				if err := os.WriteFile(pubPath, []byte(pubB64+"\n"), 0644); err != nil {
					return fmt.Errorf("failed to write public key file: %w", err)
				}
				if err := os.WriteFile(privPath, []byte(privB64+"\n"), 0600); err != nil {
					return fmt.Errorf("failed to write private key file: %w", err)
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Keypair successfully generated and saved to %s:\n", outDir)
				fmt.Fprintf(cmd.OutOrStdout(), "  Public Key : %s\n", pubPath)
				fmt.Fprintf(cmd.OutOrStdout(), "  Private Key: %s\n\n", privPath)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Ed25519 Public Verification Key (embed in binary / DIVMORA_PUBLIC_KEY):")
			fmt.Fprintln(cmd.OutOrStdout(), pubB64)
			fmt.Fprintln(cmd.OutOrStdout(), "\nEd25519 Private Signing Key (SECURE AND CONFIDENTIAL):")
			fmt.Fprintln(cmd.OutOrStdout(), privB64)

			return nil
		},
	}

	cmd.Flags().StringVarP(&outDir, "out-dir", "o", "", "Optional directory to save divmora-public.key and divmora-private.key files")
	return cmd
}

type issueFlags struct {
	CustomerName   string
	CustomerEmail  string
	CustomerOrgID  string
	Tier           string
	MaxProjects    int
	ValidDays      int
	AllowedHosts   string
	AllowedGroups  string
	Features       string
	PrivateKey     string
	PrivateKeyFile string
	GracePeriod    int
	TokenOnly      bool
}

func newIssueCmd() *cobra.Command {
	var flags issueFlags

	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Issue and cryptographically sign a commercial customer license token",
		Example: `  fleet-license-gen issue \
    --customer="Acme Corp" \
    --email="devops@acme.com" \
    --tier="enterprise" \
    --hosts="gitlab.com" \
    --groups="acme-corp" \
    --projects=500 \
    --valid-days=365 \
    --private-key-file=divmora-private.key`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.CustomerName == "" {
				return fmt.Errorf("--customer is required")
			}

			privKeyBytes, err := resolvePrivateKey(flags.PrivateKey, flags.PrivateKeyFile)
			if err != nil {
				return err
			}

			now := time.Now().UTC()
			expiresAt := now.AddDate(0, 0, flags.ValidDays)

			var features []string
			if flags.Features != "" {
				for _, f := range strings.Split(flags.Features, ",") {
					trimmed := strings.TrimSpace(f)
					if trimmed != "" {
						features = append(features, trimmed)
					}
				}
			}
			if len(features) == 0 {
				features = []string{"all"}
			}

			var allowedHosts []string
			if flags.AllowedHosts != "" {
				for _, h := range strings.Split(flags.AllowedHosts, ",") {
					trimmed := strings.TrimSpace(h)
					if trimmed != "" {
						allowedHosts = append(allowedHosts, trimmed)
					}
				}
			}

			var allowedGroups []string
			if flags.AllowedGroups != "" {
				for _, g := range strings.Split(flags.AllowedGroups, ",") {
					trimmed := strings.TrimSpace(g)
					if trimmed != "" {
						allowedGroups = append(allowedGroups, trimmed)
					}
				}
			}

			idBytes := make([]byte, 4)
			_, _ = rand.Read(idBytes)
			licenseID := fmt.Sprintf("lic_%x", idBytes)

			claims := &license.Claims{
				ID: licenseID,
				Customer: license.Customer{
					Name:  flags.CustomerName,
					Email: flags.CustomerEmail,
					OrgID: flags.CustomerOrgID,
				},
				Tier:            flags.Tier,
				MaxProjects:     flags.MaxProjects,
				AllowedHosts:    allowedHosts,
				AllowedGroups:   allowedGroups,
				Features:        features,
				IssuedAt:        now,
				ExpiresAt:       expiresAt,
				GracePeriodDays: flags.GracePeriod,
			}

			token, err := license.SignLicense(claims, ed25519.PrivateKey(privKeyBytes))
			if err != nil {
				return fmt.Errorf("failed to sign license: %w", err)
			}

			if flags.TokenOnly {
				fmt.Fprintln(cmd.OutOrStdout(), token)
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintln(cmd.OutOrStdout(), "Commercial Enterprise License Token Generated")
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintf(cmd.OutOrStdout(), "License ID       : %s\n", claims.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Customer         : %s\n", claims.Customer.Name)
			if claims.Customer.Email != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Contact Email    : %s\n", claims.Customer.Email)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Subscription Tier: %s\n", claims.Tier)
			if claims.MaxProjects == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Fleet Capacity   : Unlimited Projects")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Fleet Capacity   : %d Managed Projects\n", claims.MaxProjects)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Issued At        : %s\n", claims.IssuedAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "Expires At       : %s (%d days)\n", claims.ExpiresAt.Format(time.RFC3339), flags.ValidDays)
			fmt.Fprintf(cmd.OutOrStdout(), "Grace Period     : %d days\n", claims.EffectiveGracePeriodDays())
			if len(claims.AllowedHosts) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Allowed Hosts    : %s\n", strings.Join(claims.AllowedHosts, ", "))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Allowed Hosts    : Any (*)")
			}
			if len(claims.AllowedGroups) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Allowed Groups   : %s\n", strings.Join(claims.AllowedGroups, ", "))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Allowed Groups   : Any (*)")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Entitlements     : %s\n", strings.Join(claims.Features, ", "))
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintln(cmd.OutOrStdout(), "\nFLEET_LICENSE_KEY Token:")
			fmt.Fprintln(cmd.OutOrStdout(), token)
			fmt.Fprintln(cmd.OutOrStdout(), "\nTo use this license:")
			fmt.Fprintf(cmd.OutOrStdout(), "  export FLEET_LICENSE_KEY=\"%s\"\n", token)
			fmt.Fprintf(cmd.OutOrStdout(), "  gitlab-fleet-governor run --license-key=\"%s\"\n", token)

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.CustomerName, "customer", "c", "", "Customer / company organization name (required)")
	cmd.Flags().StringVar(&flags.CustomerEmail, "email", "", "Customer contact email address")
	cmd.Flags().StringVar(&flags.CustomerOrgID, "org-id", "", "Customer internal account / organization ID")
	cmd.Flags().StringVarP(&flags.Tier, "tier", "t", "enterprise", "Subscription tier: enterprise, pro, community")
	cmd.Flags().IntVarP(&flags.MaxProjects, "projects", "p", 500, "Maximum licensed fleet projects (0 for unlimited)")
	cmd.Flags().IntVar(&flags.ValidDays, "valid-days", 365, "Validity period duration in days (default: 365)")
	cmd.Flags().StringVar(&flags.AllowedHosts, "hosts", "", "Comma-separated allowed GitLab hostnames (e.g. 'gitlab.com,gitlab.mycorp.com')")
	cmd.Flags().StringVar(&flags.AllowedGroups, "groups", "", "Comma-separated allowed root group hierarchies (e.g. 'acme-corp,fintech-division')")
	cmd.Flags().StringVar(&flags.Features, "features", "all", "Comma-separated feature entitlements or 'all'")
	cmd.Flags().StringVar(&flags.PrivateKey, "private-key", os.Getenv("DIVMORA_PRIVATE_KEY"), "Base64-encoded Ed25519 private signing key (env: DIVMORA_PRIVATE_KEY)")
	cmd.Flags().StringVar(&flags.PrivateKeyFile, "private-key-file", "", "Path to file containing Ed25519 private key")
	cmd.Flags().IntVar(&flags.GracePeriod, "grace-period", 14, "Grace period duration in days post-expiration (default: 14)")
	cmd.Flags().BoolVarP(&flags.TokenOnly, "token-only", "q", false, "Output only the raw signed license token string")

	return cmd
}

func newInspectCmd() *cobra.Command {
	var (
		keyToken  string
		keyFile   string
		publicKey string
	)

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect, decode, and verify a commercial license token",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := license.ResolveToken(keyToken, keyFile)
			if err != nil {
				return err
			}
			if token == "" && len(args) > 0 {
				token = args[0]
			}
			if token == "" {
				return fmt.Errorf("please provide a license token via argument, --license-key, or --license-file")
			}

			var pubKey ed25519.PublicKey
			if publicKey != "" {
				b, err := base64.StdEncoding.DecodeString(publicKey)
				if err != nil {
					b, err = base64.RawURLEncoding.DecodeString(publicKey)
				}
				if err == nil && len(b) == ed25519.PublicKeySize {
					pubKey = ed25519.PublicKey(b)
				}
			}

			status, err := license.ParseAndVerify(token, pubKey)
			if err != nil {
				return fmt.Errorf("verification error: %w", err)
			}

			claims := status.Claims
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintln(cmd.OutOrStdout(), "GitLab Fleet Governor License Inspection")
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintf(cmd.OutOrStdout(), "Signature Check  : VERIFIED (Ed25519)\n")
			fmt.Fprintf(cmd.OutOrStdout(), "License Status   : %s\n", status.Message)
			fmt.Fprintf(cmd.OutOrStdout(), "License ID       : %s\n", claims.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Customer         : %s\n", claims.Customer.Name)
			if claims.Customer.Email != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Contact Email    : %s\n", claims.Customer.Email)
			}
			if claims.Customer.OrgID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Organization ID  : %s\n", claims.Customer.OrgID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Tier             : %s\n", claims.Tier)
			if claims.MaxProjects == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Capacity Limit   : Unlimited Projects")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Capacity Limit   : %d Managed Projects\n", claims.MaxProjects)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Issued At        : %s\n", claims.IssuedAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "Expires At       : %s\n", claims.ExpiresAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "Days Remaining   : %d\n", status.DaysRemaining)
			fmt.Fprintf(cmd.OutOrStdout(), "In Grace Period  : %t\n", status.InGracePeriod)
			if len(claims.AllowedHosts) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Allowed Hosts    : %s\n", strings.Join(claims.AllowedHosts, ", "))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Allowed Hosts    : Any (*)")
			}
			if len(claims.AllowedGroups) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Allowed Groups   : %s\n", strings.Join(claims.AllowedGroups, ", "))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Allowed Groups   : Any (*)")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Entitlements     : %s\n", strings.Join(claims.Features, ", "))
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")

			return nil
		},
	}

	cmd.Flags().StringVar(&keyToken, "license-key", "", "Base64-encoded license token string")
	cmd.Flags().StringVar(&keyFile, "license-file", "", "Path to file containing license token string")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "Optional base64 public verification key")

	return cmd
}

func resolvePrivateKey(rawKey, keyFile string) ([]byte, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey != "" {
		return decodeKeyBytes(rawKey)
	}

	keyFile = strings.TrimSpace(keyFile)
	if keyFile != "" {
		content, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key file: %w", err)
		}
		return decodeKeyBytes(strings.TrimSpace(string(content)))
	}

	return nil, fmt.Errorf("private key is required (use --private-key or --private-key-file)")
}

func decodeKeyBytes(raw string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(raw)
	if err == nil && len(b) == ed25519.PrivateKeySize {
		return b, nil
	}
	b, err = base64.RawStdEncoding.DecodeString(raw)
	if err == nil && len(b) == ed25519.PrivateKeySize {
		return b, nil
	}
	b, err = base64.RawURLEncoding.DecodeString(raw)
	if err == nil && len(b) == ed25519.PrivateKeySize {
		return b, nil
	}
	b, err = base64.URLEncoding.DecodeString(raw)
	if err == nil && len(b) == ed25519.PrivateKeySize {
		return b, nil
	}
	return nil, fmt.Errorf("failed to decode private key (must be %d-byte base64 string)", ed25519.PrivateKeySize)
}

type signReleaseFlags struct {
	Version        string
	GitCommit      string
	BuildDate      string
	Authority      string
	AuthorityID    string
	PrivateKey     string
	PrivateKeyFile string
	OutputFile     string
	TokenOnly      bool
}

func newSignReleaseCmd() *cobra.Command {
	var flags signReleaseFlags

	cmd := &cobra.Command{
		Use:   "sign-release",
		Short: "Cryptographically sign release metadata envelope for Layer 3 provenance",
		Long: `Generate and sign an Ed25519 cryptographic release attestation token for official
GitLab Fleet Governor releases. The signed token certifies the authentic release version,
commit SHA, and build timestamp to enable autonomous 3-year Change Date conversion under BSL 1.1.`,
		Example: `  # Sign release with inline private key (token-only for CI/CD env var)
  bin/fleet-license-gen sign-release \
    --version="0.5.0" \
    --commit="e1e3d1c9842" \
    --build-date="2026-09-11T12:00:00Z" \
    --token-only \
    --private-key="$DIVMORA_PRIVATE_KEY"

  # Sign release and write to sidecar release.sig file
  bin/fleet-license-gen sign-release \
    --version="0.5.0" \
    --commit="e1e3d1c9842" \
    --private-key-file=/path/to/divmora-private.key \
    --out-file=release.sig`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.Version == "" {
				return fmt.Errorf("version is required (use --version)")
			}
			if flags.GitCommit == "" {
				return fmt.Errorf("git commit is required (use --commit)")
			}

			buildDate := flags.BuildDate
			if buildDate == "" {
				buildDate = time.Now().UTC().Format(time.RFC3339)
			} else {
				if _, err := time.Parse(time.RFC3339, buildDate); err != nil {
					if _, err := time.Parse("2006-01-02", buildDate); err != nil {
						return fmt.Errorf("invalid build-date format (expected RFC3339 e.g. 2026-09-11T12:00:00Z): %w", err)
					}
				}
			}

			privKeyBytes, err := resolvePrivateKey(flags.PrivateKey, flags.PrivateKeyFile)
			if err != nil {
				return err
			}
			privKey := ed25519.PrivateKey(privKeyBytes)

			authority := flags.Authority
			if authority == "" {
				authority = "DIVMORA Technologies Release Authority"
			}

			claims := &version.ReleaseClaims{
				Version:     flags.Version,
				GitCommit:   flags.GitCommit,
				BuildDate:   buildDate,
				Authority:   authority,
				AuthorityID: flags.AuthorityID,
			}

			token, err := version.SignRelease(claims, privKey)
			if err != nil {
				return fmt.Errorf("failed to sign release: %w", err)
			}

			if flags.OutputFile != "" {
				if err := os.WriteFile(flags.OutputFile, []byte(token+"\n"), 0644); err != nil {
					return fmt.Errorf("failed to write release signature to %s: %w", flags.OutputFile, err)
				}
			}

			if flags.TokenOnly {
				fmt.Fprintln(cmd.OutOrStdout(), token)
				return nil
			}

			parsedDate, _ := time.Parse(time.RFC3339, buildDate)
			changeDate := parsedDate.AddDate(version.BSLChangePeriodYears, 0, 0)

			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintln(cmd.OutOrStdout(), "DIVMORA Technologies: Cryptographic Release Attestation (Layer 3)")
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintf(cmd.OutOrStdout(), "Version         : %s\n", flags.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "Git Commit      : %s\n", flags.GitCommit)
			fmt.Fprintf(cmd.OutOrStdout(), "Build Date      : %s\n", buildDate)
			fmt.Fprintf(cmd.OutOrStdout(), "Authority       : %s\n", authority)
			if flags.AuthorityID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Authority ID    : %s\n", flags.AuthorityID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Apache 2.0 Date : %s (Converts under BSL 1.1 Change Date terms)\n", changeDate.Format("2006-01-02"))
			if flags.OutputFile != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Saved Signature : %s\n", flags.OutputFile)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\nRelease Signature Token:")
			fmt.Fprintln(cmd.OutOrStdout(), token)
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.Version, "version", "", "Release version (e.g. '0.5.0')")
	cmd.Flags().StringVar(&flags.GitCommit, "commit", "", "Git commit SHA (e.g. '4b825dc...')")
	cmd.Flags().StringVar(&flags.BuildDate, "build-date", "", "Release build timestamp RFC3339 (defaults to current UTC time)")
	cmd.Flags().StringVar(&flags.Authority, "authority", "DIVMORA Technologies Release Authority", "Release authority name")
	cmd.Flags().StringVar(&flags.AuthorityID, "authority-id", "", "Release authority identifier (e.g. 'divmora-prod-release-1')")
	cmd.Flags().StringVar(&flags.PrivateKey, "private-key", "", "Base64-encoded Ed25519 private signing key")
	cmd.Flags().StringVar(&flags.PrivateKeyFile, "private-key-file", "", "Path to file containing private signing key")
	cmd.Flags().StringVarP(&flags.OutputFile, "out-file", "o", "", "Path to write release.sig output file")
	cmd.Flags().BoolVarP(&flags.TokenOnly, "token-only", "q", false, "Output only the raw signature token")

	return cmd
}

type verifyReleaseFlags struct {
	Token         string
	TokenFile     string
	PublicKey     string
	PublicKeyFile string
	JSON          bool
}

func newVerifyReleaseCmd() *cobra.Command {
	var flags verifyReleaseFlags

	cmd := &cobra.Command{
		Use:   "verify-release",
		Short: "Inspect and cryptographically verify a release attestation token",
		RunE: func(cmd *cobra.Command, args []string) error {
			token := strings.TrimSpace(flags.Token)
			if token == "" && flags.TokenFile != "" {
				content, err := os.ReadFile(flags.TokenFile)
				if err != nil {
					return fmt.Errorf("failed to read token file: %w", err)
				}
				token = strings.TrimSpace(string(content))
			}
			if token == "" {
				return fmt.Errorf("token is required (use --token or --token-file)")
			}

			var pubKey ed25519.PublicKey
			if flags.PublicKey != "" {
				b, err := base64.StdEncoding.DecodeString(flags.PublicKey)
				if err != nil {
					b, err = base64.RawURLEncoding.DecodeString(flags.PublicKey)
					if err != nil {
						return fmt.Errorf("failed to decode public key: %w", err)
					}
				}
				pubKey = ed25519.PublicKey(b)
			} else if flags.PublicKeyFile != "" {
				content, err := os.ReadFile(flags.PublicKeyFile)
				if err != nil {
					return fmt.Errorf("failed to read public key file: %w", err)
				}
				b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(content)))
				if err != nil {
					return fmt.Errorf("failed to decode public key: %w", err)
				}
				pubKey = ed25519.PublicKey(b)
			}

			claims, err := version.ParseAndVerifyReleaseToken(token, pubKey)
			if err != nil {
				return fmt.Errorf("cryptographic verification failed: %w", err)
			}

			if flags.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(claims)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintln(cmd.OutOrStdout(), "DIVMORA Technologies: Cryptographic Release Verification")
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")
			fmt.Fprintf(cmd.OutOrStdout(), "Status           : VALID & VERIFIED (Ed25519 Asymmetric Signature)\n")
			fmt.Fprintf(cmd.OutOrStdout(), "Version          : %s\n", claims.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "Git Commit       : %s\n", claims.GitCommit)
			fmt.Fprintf(cmd.OutOrStdout(), "Build Date       : %s\n", claims.BuildDate)
			fmt.Fprintf(cmd.OutOrStdout(), "Release Authority: %s\n", claims.Authority)
			if claims.AuthorityID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Authority ID     : %s\n", claims.AuthorityID)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "================================================================================")

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.Token, "token", "", "Release signature token string")
	cmd.Flags().StringVarP(&flags.TokenFile, "token-file", "f", "", "Path to file containing release signature token")
	cmd.Flags().StringVar(&flags.PublicKey, "public-key", "", "Optional Ed25519 public key (defaults to embedded DIVMORA key)")
	cmd.Flags().StringVar(&flags.PublicKeyFile, "public-key-file", "", "Optional path to public key file")
	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output verified claims as formatted JSON")

	return cmd
}
