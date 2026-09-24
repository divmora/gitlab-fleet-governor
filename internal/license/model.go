package license

import (
	"fmt"
	"strings"

	liblicense "github.com/divmora/license-go/pkg/license"
)

// FreeTierMaxProjects defines the maximum number of cumulative managed GitLab projects
// permitted in production environments free of charge under the Business Source License 1.1.
const FreeTierMaxProjects = 25

// DefaultGracePeriodDays defines the default grace period duration (in days) after license
// expiration during which warning notices are emitted before hard-blocking execution.
const DefaultGracePeriodDays = 14

// Customer encapsulates customer identification metadata within a license token.
type Customer = liblicense.Customer

// Claims encapsulates the canonical cryptographic claims embedded in a signed license token.
type Claims = liblicense.Claims

// Scope defines operational boundaries restricting where and on what infrastructure the license is authorized.
type Scope = liblicense.Scope

// VerificationResult encapsulates verified license claims alongside explicit grace period dynamics.
type VerificationResult = liblicense.VerificationResult

// ValidationStatus represents the outcome of validating a license token against the current environment.
type ValidationStatus struct {
	// Valid indicates whether the cryptographic signature is authentic and the license has not expired past grace period.
	Valid bool `json:"valid"`

	// InGracePeriod indicates whether the license is past its ExpiresAt date but within its grace period window.
	InGracePeriod bool `json:"in_grace_period"`

	// DaysRemaining indicates the number of days until ExpiresAt (positive) or days remaining in grace period (negative).
	DaysRemaining int `json:"days_remaining"`

	// Message contains a human-readable diagnostic description of the license status.
	Message string `json:"message"`

	// Claims contains the verified license claims.
	Claims *Claims `json:"claims,omitempty"`
}

// GetClaimsPlan returns the subscription plan/tier from Claims.
func GetClaimsPlan(c *Claims) string {
	if c == nil || c.Plan == "" {
		return "community"
	}
	return c.Plan
}

// GetClaimsMaxProjects returns the max projects limit from Claims, or 0 if unlimited.
func GetClaimsMaxProjects(c *Claims) int {
	if c == nil {
		return 0
	}
	if lim, ok := c.GetLimit("max_projects"); ok && lim > 0 {
		return int(lim)
	}
	return 0
}

// ExtractHost normalizes a GitLab Base URL or hostname string into a lowercase host string.
func ExtractHost(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return "gitlab.com"
	}
	return liblicense.NormalizeHost(raw)
}

// DefaultTierFeatures defines the first-class entitlement matrix mapping commercial plans
// ("pro", "enterprise") to feature flags for GitLab Fleet Governor.
var DefaultTierFeatures = liblicense.TierFeatures{
	"pro": {
		"governance.push_rules",
		"governance.protected_branches",
		"governance.project_settings",
		"governance.members",
		"governance.variables",
		"governance.approval_rules",
		"governance.target_branch_rules",
		"governance.runners",
		"governance.webhooks",
		"governance.pipeline_retention",
		"report.*",
	},
	"enterprise": {
		"*",
	},
}

// IsCommunityFeature reports whether a feature belongs to the free Community Tier.
// Community features are inherently free and unencumbered; they completely bypass
// all license verification and feature assertion functions.
func IsCommunityFeature(feature string) bool {
	switch strings.TrimSpace(strings.ToLower(feature)) {
	case "governance.push_rules",
		"governance.protected_branches",
		"governance.project_settings",
		"governance.members",
		"governance.variables",
		"report.table", "report.json", "report.csv", "report.markdown", "report.summary",
		"report.*":
		return true
	default:
		return false
	}
}

// RequiredTierForFeature returns the minimum subscription tier ("community", "pro", or "enterprise")
// required to unlock the specified feature.
func RequiredTierForFeature(feature string) string {
	if IsCommunityFeature(feature) {
		return "community"
	}
	switch strings.TrimSpace(strings.ToLower(feature)) {
	case "governance.approval_rules",
		"governance.target_branch_rules",
		"governance.runners",
		"governance.webhooks",
		"governance.pipeline_retention":
		return "pro"
	default:
		return "enterprise"
	}
}

// AssertFeature checks if a feature is authorized.
// If the feature belongs to the Community Tier, it returns nil immediately without invoking
// any license verification functions.
// Otherwise, it verifies that valid commercial claims are present and that the active plan tier
// or explicit feature grants entitle the feature.
func AssertFeature(status *ValidationStatus, feature string) error {
	if IsCommunityFeature(feature) {
		return nil
	}
	tierReq := RequiredTierForFeature(feature)
	if status == nil || status.Claims == nil {
		return fmt.Errorf("COMMERCIAL LICENSE REQUIRED: Feature %q requires a commercial %s or higher subscription. Visit https://divmora.com or contact licensing@divmora.com",
			feature, strings.ToUpper(tierReq))
	}
	if err := status.Claims.AssertFeature(feature); err != nil {
		return fmt.Errorf("FEATURE NOT ENTITLED: Feature %q is not authorized under your current license plan %q (%w). Please upgrade to %s at https://divmora.com or contact licensing@divmora.com",
			feature, status.Claims.Plan, err, strings.ToUpper(tierReq))
	}
	return nil
}

// AssertFeature checks if a feature is authorized on this ValidationStatus instance.
func (v *ValidationStatus) AssertFeature(feature string) error {
	return AssertFeature(v, feature)
}
