package license

import (
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// EnforcementOptions encapsulates the operational parameters required to evaluate
// compliance with the Business Source License 1.1 terms.
type EnforcementOptions struct {
	// DiscoveredProjects is the count of projects targeted in the current execution.
	DiscoveredProjects int

	// IsDryRun specifies whether non-destructive simulation is active.
	IsDryRun bool

	// LicenseKey is the raw license token string.
	LicenseKey string

	// LicenseFile is the path to a file containing the license token.
	LicenseFile string

	// Command identifies the calling command (e.g. "run", "audit").
	Command string

	// PublicKey optionally overrides the default public key (primarily for testing).
	PublicKey ed25519.PublicKey
}

// ResolveToken determines the active license token from flags, file paths, or environment variables.
func ResolveToken(key, file string) (string, error) {
	key = strings.TrimSpace(key)
	if key != "" {
		return key, nil
	}

	file = strings.TrimSpace(file)
	if file != "" {
		content, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("failed to read license file %s: %w", file, err)
		}
		return strings.TrimSpace(string(content)), nil
	}

	envKey := strings.TrimSpace(os.Getenv("FLEET_LICENSE_KEY"))
	if envKey != "" {
		return envKey, nil
	}

	envFile := strings.TrimSpace(os.Getenv("FLEET_LICENSE_FILE"))
	if envFile != "" {
		content, err := os.ReadFile(envFile)
		if err != nil {
			return "", fmt.Errorf("failed to read license file from FLEET_LICENSE_FILE (%s): %w", envFile, err)
		}
		return strings.TrimSpace(string(content)), nil
	}

	return "", nil
}

// Enforce evaluates the active execution context against the Business Source License 1.1 terms:
// 1. Non-production / dry-run simulation: Permitted free of charge under Additional Use Grant (a).
// 2. Production fleet size <= 25: Permitted free of charge under Additional Use Grant (b).
// 3. Production fleet size > 25: Strictly requires a valid, unexpired commercial license with sufficient capacity.
func Enforce(opts EnforcementOptions) (*ValidationStatus, error) {
	// 1. Non-Production & Dry-Run Simulation Exemption
	if opts.IsDryRun {
		slog.Debug("License check: execution is in dry-run simulation mode (permitted free of charge under BSL 1.1 Additional Use Grant a)",
			"command", opts.Command,
			"discovered_projects", opts.DiscoveredProjects,
		)
		return &ValidationStatus{
			Valid:   true,
			Message: "Simulation / dry-run mode exempted under BSL 1.1 Additional Use Grant (a)",
		}, nil
	}

	// 2. Free Production Fleet Tier (<= 25 Projects)
	if opts.DiscoveredProjects <= FreeTierMaxProjects {
		slog.Info("License tier: Free Community Tier active",
			"managed_projects", opts.DiscoveredProjects,
			"limit", FreeTierMaxProjects,
		)
		return &ValidationStatus{
			Valid:   true,
			Message: fmt.Sprintf("Free Community Tier (%d/%d managed projects)", opts.DiscoveredProjects, FreeTierMaxProjects),
		}, nil
	}

	// 3. Production Fleet Size > 25 Projects: Commercial Subscription Strictly Required
	token, err := ResolveToken(opts.LicenseKey, opts.LicenseFile)
	if err != nil {
		return nil, err
	}

	if token == "" {
		return nil, fmt.Errorf(`COMMERCIAL LICENSE REQUIRED: Governing %d projects in production exceeds the free Community Tier limit (%d projects) permitted under the Business Source License 1.1.

To continue managing fleets of this size:
  1. Obtain a commercial subscription at https://divmora.com or contact licensing@divmora.com
  2. Set your license key via environment variable:
       export FLEET_LICENSE_KEY="<token>"
     or provide it via CLI flag:
       --license-key="<token>"`, opts.DiscoveredProjects, FreeTierMaxProjects)
	}

	status, err := ParseAndVerify(token, opts.PublicKey)
	if err != nil {
		return status, fmt.Errorf("commercial license verification failed: %w", err)
	}

	// Enforce fleet capacity limit (if not unlimited)
	if status.Claims.MaxProjects > 0 && opts.DiscoveredProjects > status.Claims.MaxProjects {
		return status, fmt.Errorf("FLEET CAPACITY EXCEEDED: Governing %d production projects exceeds your licensed capacity of %d projects (%s Tier). Please contact licensing@divmora.com to upgrade your fleet capacity.",
			opts.DiscoveredProjects, status.Claims.MaxProjects, status.Claims.Tier)
	}

	// Log warnings if operating in grace period
	if status.InGracePeriod {
		slog.Warn("COMMERCIAL LICENSE NOTICE: License has expired but is operating within its grace period",
			"customer", status.Claims.Customer.Name,
			"expires_at", status.Claims.ExpiresAt.Format("2006-01-02"),
			"days_remaining_in_grace", status.DaysRemaining,
			"contact", "licensing@divmora.com",
		)
	} else {
		slog.Info("Commercial enterprise license verified",
			"tier", status.Claims.Tier,
			"customer", status.Claims.Customer.Name,
			"capacity", status.Claims.MaxProjects,
			"active_projects", opts.DiscoveredProjects,
			"days_remaining", status.DaysRemaining,
		)
	}

	return status, nil
}
