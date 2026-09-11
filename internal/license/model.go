package license

import (
	"time"
)

// FreeTierMaxProjects defines the maximum number of cumulative managed GitLab projects
// permitted in production environments free of charge under the Business Source License 1.1.
const FreeTierMaxProjects = 25

// DefaultGracePeriodDays defines the default grace period duration (in days) after license
// expiration during which warning notices are emitted before hard-blocking execution.
const DefaultGracePeriodDays = 14

// Customer encapsulates customer identification metadata within a license token.
type Customer struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	OrgID string `json:"org_id,omitempty"`
}

// Claims encapsulates the canonical cryptographic claims embedded in a signed license token.
type Claims struct {
	// ID is the unique identifier for the issued license (e.g. "lic_9b1deb4d").
	ID string `json:"id"`

	// Customer contains licensee details.
	Customer Customer `json:"customer"`

	// Tier indicates the subscription tier: "enterprise", "pro", or "community".
	Tier string `json:"tier"`

	// MaxProjects specifies the maximum number of cumulative managed GitLab projects permitted.
	// A value of 0 indicates unlimited project capacity.
	MaxProjects int `json:"max_projects"`

	// Features lists the authorized feature flags or reconcilers enabled for this license.
	Features []string `json:"features,omitempty"`

	// IssuedAt is the timestamp when the license was minted.
	IssuedAt time.Time `json:"issued_at"`

	// ExpiresAt is the timestamp after which the license enters expiration / grace period.
	ExpiresAt time.Time `json:"expires_at"`

	// GracePeriodDays specifies how many days past ExpiresAt the software will continue
	// operating with warning logs before hard-blocking. If 0, DefaultGracePeriodDays is used.
	GracePeriodDays int `json:"grace_period_days,omitempty"`
}

// EffectiveGracePeriodDays returns the configured grace period days or DefaultGracePeriodDays.
func (c *Claims) EffectiveGracePeriodDays() int {
	if c.GracePeriodDays <= 0 {
		return DefaultGracePeriodDays
	}
	return c.GracePeriodDays
}

// HasFeature returns true if the license grants access to the specified feature name.
// Licenses containing "all" or "*" grant all features.
func (c *Claims) HasFeature(feature string) bool {
	for _, f := range c.Features {
		if f == "*" || f == "all" || f == feature {
			return true
		}
	}
	return false
}

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
