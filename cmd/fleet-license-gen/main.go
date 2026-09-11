package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "fleet-license-gen",
		Short: "DIVMORA Technologies: Fleet Governor License Minting & Verification Utility",
		Long: `Administrative utility for generating Ed25519 keypairs, issuing signed commercial
customer license tokens, and inspecting token claims for GitLab Fleet Governor under
Business Source License 1.1 terms.`,
	}

	rootCmd.AddCommand(newInitKeysCmd())
	rootCmd.AddCommand(newIssueCmd())
	rootCmd.AddCommand(newInspectCmd())

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

				fmt.Printf("Keypair successfully generated and saved to %s:\n", outDir)
				fmt.Printf("  Public Key : %s\n", pubPath)
				fmt.Printf("  Private Key: %s\n\n", privPath)
			}

			fmt.Println("Ed25519 Public Verification Key (embed in binary / DIVMORA_PUBLIC_KEY):")
			fmt.Println(pubB64)
			fmt.Println("\nEd25519 Private Signing Key (SECURE AND CONFIDENTIAL):")
			fmt.Println(privB64)

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
				fmt.Println(token)
				return nil
			}

			fmt.Println("================================================================================")
			fmt.Println("Commercial Enterprise License Token Generated")
			fmt.Println("================================================================================")
			fmt.Printf("License ID       : %s\n", claims.ID)
			fmt.Printf("Customer         : %s\n", claims.Customer.Name)
			if claims.Customer.Email != "" {
				fmt.Printf("Contact Email    : %s\n", claims.Customer.Email)
			}
			fmt.Printf("Subscription Tier: %s\n", claims.Tier)
			if claims.MaxProjects == 0 {
				fmt.Println("Fleet Capacity   : Unlimited Projects")
			} else {
				fmt.Printf("Fleet Capacity   : %d Managed Projects\n", claims.MaxProjects)
			}
			fmt.Printf("Issued At        : %s\n", claims.IssuedAt.Format(time.RFC3339))
			fmt.Printf("Expires At       : %s (%d days)\n", claims.ExpiresAt.Format(time.RFC3339), flags.ValidDays)
			fmt.Printf("Grace Period     : %d days\n", claims.EffectiveGracePeriodDays())
			fmt.Printf("Entitlements     : %s\n", strings.Join(claims.Features, ", "))
			fmt.Println("================================================================================")
			fmt.Println("\nFLEET_LICENSE_KEY Token:")
			fmt.Println(token)
			fmt.Println("\nTo use this license:")
			fmt.Printf("  export FLEET_LICENSE_KEY=\"%s\"\n", token)
			fmt.Printf("  gitlab-fleet-governor run --license-key=\"%s\"\n", token)

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.CustomerName, "customer", "c", "", "Customer / company organization name (required)")
	cmd.Flags().StringVar(&flags.CustomerEmail, "email", "", "Customer contact email address")
	cmd.Flags().StringVar(&flags.CustomerOrgID, "org-id", "", "Customer internal account / organization ID")
	cmd.Flags().StringVarP(&flags.Tier, "tier", "t", "enterprise", "Subscription tier: enterprise, pro, community")
	cmd.Flags().IntVarP(&flags.MaxProjects, "projects", "p", 500, "Maximum licensed fleet projects (0 for unlimited)")
	cmd.Flags().IntVar(&flags.ValidDays, "valid-days", 365, "Validity period duration in days (default: 365)")
	cmd.Flags().StringVar(&flags.Features, "features", "all", "Comma-separated feature entitlements or 'all'")
	cmd.Flags().StringVar(&flags.PrivateKey, "private-key", os.Getenv("DIVMORA_PRIVATE_KEY"), "Base64-encoded Ed25519 private signing key (env: DIVMORA_PRIVATE_KEY)")
	cmd.Flags().StringVar(&flags.PrivateKeyFile, "private-key-file", "", "Path to file containing Ed25519 private key")
	cmd.Flags().IntVar(&flags.GracePeriod, "grace-period", 14, "Grace period duration in days post-expiration (default: 14)")
	cmd.Flags().BoolVarP(&flags.TokenOnly, "token-only", "q", false, "Output only the raw signed license token string")

	return cmd
}

func newInspectCmd() *cobra.Command {
	var (
		keyToken string
		keyFile  string
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

			status, err := license.ParseAndVerify(token, nil)
			if err != nil {
				return fmt.Errorf("verification error: %w", err)
			}

			claims := status.Claims
			fmt.Println("================================================================================")
			fmt.Println("GitLab Fleet Governor License Inspection")
			fmt.Println("================================================================================")
			fmt.Printf("Signature Check  : VERIFIED (Ed25519)\n")
			fmt.Printf("License Status   : %s\n", status.Message)
			fmt.Printf("License ID       : %s\n", claims.ID)
			fmt.Printf("Customer         : %s\n", claims.Customer.Name)
			if claims.Customer.Email != "" {
				fmt.Printf("Contact Email    : %s\n", claims.Customer.Email)
			}
			if claims.Customer.OrgID != "" {
				fmt.Printf("Organization ID  : %s\n", claims.Customer.OrgID)
			}
			fmt.Printf("Tier             : %s\n", claims.Tier)
			if claims.MaxProjects == 0 {
				fmt.Println("Capacity Limit   : Unlimited Projects")
			} else {
				fmt.Printf("Capacity Limit   : %d Managed Projects\n", claims.MaxProjects)
			}
			fmt.Printf("Issued At        : %s\n", claims.IssuedAt.Format(time.RFC3339))
			fmt.Printf("Expires At       : %s\n", claims.ExpiresAt.Format(time.RFC3339))
			fmt.Printf("Days Remaining   : %d\n", status.DaysRemaining)
			fmt.Printf("In Grace Period  : %t\n", status.InGracePeriod)
			fmt.Printf("Entitlements     : %s\n", strings.Join(claims.Features, ", "))
			fmt.Println("================================================================================")

			return nil
		},
	}

	cmd.Flags().StringVar(&keyToken, "license-key", "", "Base64-encoded license token string")
	cmd.Flags().StringVar(&keyFile, "license-file", "", "Path to file containing license token string")

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
