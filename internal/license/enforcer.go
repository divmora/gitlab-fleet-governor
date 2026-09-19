package license

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	liblicense "github.com/divmora/license-go/pkg/license"

	"github.com/divmora/gitlab-fleet-governor/pkg/version"
)

// EnforcementOptions encapsulates the operational parameters required to evaluate
// compliance with the Business Source License 1.1 terms.
type EnforcementOptions struct {
	// DiscoveredProjects is the count of projects targeted in the current execution.
	DiscoveredProjects int

	// IsDryRun specifies whether non-destructive simulation is active.
	IsDryRun bool

	// GitLabBaseURL is the base URL or host of the target GitLab instance.
	GitLabBaseURL string

	// TargetPaths contains the discovered project paths (e.g. "org/repo") and group paths.
	TargetPaths []string

	// LicenseKey is the raw license token string.
	LicenseKey string

	// LicenseFile is the path to a file containing the license token.
	LicenseFile string

	// Command identifies the calling command (e.g. "run", "audit").
	Command string

	// PublicKey optionally overrides the default public key (primarily for testing).
	PublicKey ed25519.PublicKey

	// EvaluationTime optionally overrides current time (primarily for testing Change Date conversion).
	EvaluationTime time.Time

	// GitLabServerTime is the authoritative timestamp parsed from the GitLab server's HTTP Date response header.
	// When provided, it serves as a tamper-resistant reference to detect local system clock manipulation.
	GitLabServerTime time.Time

	// RequiredFeatures lists the feature identifiers actively required by the current execution.
	// Community features bypass checking completely; non-community features require an entitled commercial license.
	RequiredFeatures []string
}

// ResolveToken determines the active license token from flags, file paths, or environment variables.
// If an explicit key is provided, it is returned directly.
// If an explicit file is provided, its contents are read from disk.
// Otherwise, it delegates to liblicense.ResolveToken to check environment variables and system paths.
// If no license is configured anywhere, it returns ("", nil) to permit Community Tier execution.
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

	token, err := liblicense.ResolveToken()
	if err != nil {
		if errors.Is(err, liblicense.ErrLicenseNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(token), nil
}

// Enforce evaluates the active execution context against the Business Source License 1.1 terms:
// 1. Non-production / dry-run simulation: Permitted free of charge under Additional Use Grant (a).
// 2. Production fleet size <= 25: Permitted free of charge under Additional Use Grant (b).
// 3. Production fleet size > 25: Strictly requires a valid, unexpired commercial license with sufficient capacity.
func Enforce(opts EnforcementOptions) (*ValidationStatus, error) {
	evalTime := opts.EvaluationTime
	if evalTime.IsZero() {
		evalTime = time.Now().UTC()
	}

	// Layer 2: GitLab Server HTTP Date Header Attestation & Clock Skew Defense
	vInfo := version.Get()
	if !opts.GitLabServerTime.IsZero() {
		serverTime := opts.GitLabServerTime.UTC()
		// Detect forward clock tampering: local clock claims Apache 2.0 conversion,
		// but authoritative GitLab server clock attests Change Date has not yet arrived.
		if vInfo.IsApacheConverted(evalTime) && !vInfo.IsApacheConverted(serverTime) {
			slog.Warn("SYSTEM CLOCK SKEW DETECTED: Local system clock indicates BSL 1.1 Change Date has passed, but authoritative GitLab server HTTP Date attests Change Date has not yet arrived. Enforcing BSL 1.1 based on server time.",
				"local_clock", evalTime.Format(time.RFC3339),
				"server_clock", serverTime.Format(time.RFC3339),
			)
			evalTime = serverTime
		} else {
			// Anchor to server time if evaluation clock drifts significantly (> 1 hour) from server time
			drift := evalTime.Sub(serverTime)
			if drift < -time.Hour || drift > time.Hour {
				slog.Warn("SYSTEM CLOCK SKEW DETECTED: System clock drifts significantly from GitLab server time. Anchoring license evaluation to authoritative server time.",
					"local_clock", evalTime.Format(time.RFC3339),
					"server_clock", serverTime.Format(time.RFC3339),
					"drift", drift.String(),
				)
				evalTime = serverTime
			}
		}
	}

	// 0. Automatic BSL 1.1 Change Date Check (Apache 2.0 Conversion after 3 years)
	if vInfo.IsApacheConverted(evalTime) {
		changeDate, _ := vInfo.ChangeDate()
		slog.Info("BSL 1.1 Change Date reached: software has automatically converted to Apache License 2.0",
			"version", vInfo.Version,
			"released_at", vInfo.BuildDate,
			"converted_at", changeDate.Format("2006-01-02"),
			"command", opts.Command,
		)
		return &ValidationStatus{
			Valid:   true,
			Message: fmt.Sprintf("Automatically converted to Apache License 2.0 on %s under BSL 1.1 terms. Unrestricted usage permitted.", changeDate.Format("2006-01-02")),
		}, nil
	}

	// 1. Resolve token (from flags, files, or environment)
	token, err := ResolveToken(opts.LicenseKey, opts.LicenseFile)
	if err != nil {
		return nil, err
	}

	// 2. If a commercial license token is provided, verify and enforce entitlements
	if token != "" {
		status, err := ParseAndVerifyAt(token, opts.PublicKey, evalTime)
		if err != nil {
			if opts.IsDryRun {
				slog.Warn("Commercial license verification failed, falling back to dry-run simulation exemption", "error", err)
				return &ValidationStatus{
					Valid:   true,
					Message: "Simulation / dry-run mode exempted under BSL 1.1 Additional Use Grant (a)",
				}, nil
			}
			return status, fmt.Errorf("commercial license verification failed: %w", err)
		}

		maxProjects := GetClaimsMaxProjects(status.Claims)
		planName := GetClaimsPlan(status.Claims)

		// Enforce fleet capacity limit (if not unlimited)
		if err := status.Claims.CheckLimit("max_projects", int64(opts.DiscoveredProjects)); err != nil {
			if opts.IsDryRun {
				slog.Warn("Fleet project count exceeds licensed capacity (allowed under dry-run simulation)",
					"discovered", opts.DiscoveredProjects,
					"capacity", maxProjects,
				)
			} else {
				return status, fmt.Errorf("FLEET CAPACITY EXCEEDED: Governing %d production projects exceeds your licensed capacity of %d projects (%s Tier). Please contact licensing@divmora.com to upgrade your fleet capacity.",
					opts.DiscoveredProjects, maxProjects, strings.ToUpper(planName))
			}
		}

		// Enforce GitLab host and group scope boundaries
		if !isHostAllowed(status.Claims, opts.GitLabBaseURL) {
			targetHost := ExtractHost(opts.GitLabBaseURL)
			var allowedHosts []string
			if status.Claims.Scope != nil {
				allowedHosts = status.Claims.Scope.Hosts
			}
			hostErr := fmt.Errorf("COMMERCIAL LICENSE HOST MISMATCH: License is restricted to GitLab host(s) %v, but active target is '%s'. Please obtain a commercial license for this host or contact licensing@divmora.com", allowedHosts, targetHost)
			if opts.IsDryRun {
				slog.Warn("GitLab host scope validation warning in dry-run mode", "error", hostErr)
			} else {
				return status, hostErr
			}
		}

		if len(opts.TargetPaths) > 0 {
			for _, path := range opts.TargetPaths {
				if !isNamespaceAllowed(status.Claims, path) {
					var allowedNamespaces []string
					if status.Claims.Scope != nil {
						allowedNamespaces = status.Claims.Scope.Namespaces
					}
					groupErr := fmt.Errorf("COMMERCIAL LICENSE GROUP MISMATCH: License is restricted to GitLab group hierarchy %v, but targeted resource '%s' falls outside permitted groups. Please contact licensing@divmora.com to extend your license scope", allowedNamespaces, path)
					if opts.IsDryRun {
						slog.Warn("GitLab group scope validation warning in dry-run mode", "error", groupErr)
					} else {
						return status, groupErr
					}
				}
			}
		}

		// Enforce required commercial features (if any)
		for _, feat := range opts.RequiredFeatures {
			if IsCommunityFeature(feat) {
				continue
			}
			if err := status.Claims.AssertFeature(feat); err != nil {
				reqTier := strings.ToUpper(RequiredTierForFeature(feat))
				if opts.IsDryRun {
					slog.Warn("Feature not entitled under active license tier (permitted in dry-run simulation)",
						"feature", feat,
						"current_plan", status.Claims.Plan,
						"required_tier", reqTier,
						"error", err,
					)
					continue
				}
				return status, fmt.Errorf("FEATURE NOT ENTITLED: Feature %q is not authorized under license tier %q (%w). Upgrade to %s at https://divmora.com or contact licensing@divmora.com",
					feat, status.Claims.Plan, err, reqTier)
			}
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
				"tier", planName,
				"customer", status.Claims.Customer.Name,
				"capacity", maxProjects,
				"active_projects", opts.DiscoveredProjects,
				"days_remaining", status.DaysRemaining,
			)
		}

		return status, nil
	}

	// 3. Evaluate non-community required features when no commercial token is provided
	for _, feat := range opts.RequiredFeatures {
		if IsCommunityFeature(feat) {
			continue
		}
		reqTier := strings.ToUpper(RequiredTierForFeature(feat))
		if opts.IsDryRun {
			slog.Warn("Feature requires a commercial subscription (permitted under dry-run simulation)",
				"feature", feat,
				"required_tier", reqTier,
			)
			continue
		}
		return nil, fmt.Errorf("COMMERCIAL LICENSE REQUIRED: Feature %q requires a %s subscription. Community Tier only includes baseline repository governance. Visit https://divmora.com or contact licensing@divmora.com",
			feat, reqTier)
	}

	// 4. Evaluate BSL 1.1 Entitlements (when no commercial token is provided)
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

	bslPolicy := GetBSLPolicy()
	env := os.Getenv("ENV")
	if env == "" {
		env = os.Getenv("ENVIRONMENT")
	}
	if env == "" {
		env = "production"
	}

	usageReq := liblicense.BSLUsageRequest{
		Environment: env,
		Usage: map[string]int64{
			"max_projects": int64(opts.DiscoveredProjects),
		},
		Time: evalTime,
		Metadata: map[string]string{
			"command": opts.Command,
		},
	}

	entitlement := bslPolicy.EvaluateEntitlement(usageReq)
	if entitlement.Authorized {
		slog.Info("License tier: Free Community Tier active",
			"managed_projects", opts.DiscoveredProjects,
			"limit", FreeTierMaxProjects,
		)
		return &ValidationStatus{
			Valid:   true,
			Message: fmt.Sprintf("Free Community Tier (%d/%d managed projects)", opts.DiscoveredProjects, FreeTierMaxProjects),
		}, nil
	}

	// 4. Production Fleet Size > 25 Projects without a license: Hard Block
	return nil, fmt.Errorf(`COMMERCIAL LICENSE REQUIRED: Governing %d projects in production exceeds the free Community Tier limit (%d projects) permitted under the Business Source License 1.1.

To continue managing fleets of this size:
  1. Obtain a commercial subscription at https://divmora.com or contact licensing@divmora.com
  2. Set your license key via environment variable:
       export DIVMORA_LICENSE_KEY="<token>"
     or provide it via CLI flag:
       --license-key="<token>"`, opts.DiscoveredProjects, FreeTierMaxProjects)
}

// GetBSLPolicy returns the canonical BSL 1.1 licensing policy for GitLab Fleet Governor,
// including autonomous Apache 2.0 Change Date conversion and Additional Use Grants.
func GetBSLPolicy() liblicense.BSLPolicy {
	vInfo := version.Get()
	var releaseDate time.Time
	// Layer 3: Only certified, officially attested releases convert to open source upon Change Date.
	// Unattested builds maintain ReleaseDate as zero so they do not convert based on self-reported timestamps.
	if vInfo.Provenance.Verified {
		if relTime, ok := vInfo.ReleaseTime(); ok {
			releaseDate = relTime
		}
	}

	nonProdGrant := liblicense.NewNonProductionGrant("Non-Production & Simulation Exemption")
	nonProdGrant.MatchFunc = func(req liblicense.BSLUsageRequest) (bool, string) {
		if req.Metadata != nil && req.Metadata["dry_run"] == "true" {
			return true, "Execution is in non-destructive dry-run simulation mode"
		}
		return false, ""
	}

	freeTierGrant := liblicense.NewFreeTierGrant("Free Community Tier", map[string]int64{
		"max_projects": int64(FreeTierMaxProjects),
	})

	return liblicense.BSLPolicy{
		Product:           "gitlab-fleet-governor",
		ReleaseDate:       releaseDate,
		ChangePeriodYears: 3,
		AdditionalUseGrants: []liblicense.BSLAdditionalUseGrant{
			nonProdGrant,
			freeTierGrant,
		},
	}
}

func isHostAllowed(claims *Claims, rawURL string) bool {
	if claims == nil || claims.Scope == nil || len(claims.Scope.Hosts) == 0 {
		return true
	}
	return claims.IsHostAllowed(rawURL)
}

func isNamespaceAllowed(claims *Claims, path string) bool {
	if claims == nil || claims.Scope == nil || len(claims.Scope.Namespaces) == 0 {
		return true
	}
	return claims.IsNamespaceAllowed(path)
}
