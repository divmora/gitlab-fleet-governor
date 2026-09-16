package license

import (
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
