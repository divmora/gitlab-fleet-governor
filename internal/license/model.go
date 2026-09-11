package license

import (
	"fmt"
	"net/url"
	"strings"
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

	// AllowedHosts restricts license validity to specific GitLab instance hostnames (e.g. ["gitlab.mycorp.com"]).
	// If empty or containing "*", the license is valid across any host (including gitlab.com SaaS).
	AllowedHosts []string `json:"allowed_hosts,omitempty"`

	// AllowedGroups restricts license validity to specific top-level group hierarchies (e.g. ["acme-corp"]).
	// Especially critical for GitLab SaaS (gitlab.com) where multiple customers share the same host.
	// If empty or containing "*", all group hierarchies on the allowed hosts are permitted.
	AllowedGroups []string `json:"allowed_groups,omitempty"`

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

// ExtractHost normalizes a GitLab Base URL or hostname string into a lowercase host string.
func ExtractHost(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return "gitlab.com"
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(rawURL)
	}
	hostname := u.Hostname()
	if hostname == "" {
		hostname = u.Host
	}
	return strings.ToLower(hostname)
}

// IsHostAllowed checks if the provided GitLab instance URL/host satisfies the license's AllowedHosts.
func (c *Claims) IsHostAllowed(rawURL string) bool {
	if len(c.AllowedHosts) == 0 {
		return true
	}
	targetHost := ExtractHost(rawURL)
	for _, allowed := range c.AllowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" || allowed == "*" {
			return true
		}
		if allowed == targetHost {
			return true
		}
		// Subdomain wildcard matching (e.g. "*.mycorp.com")
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:] // ".mycorp.com"
			if strings.HasSuffix(targetHost, suffix) || targetHost == allowed[2:] {
				return true
			}
		}
	}
	return false
}

// IsGroupAllowed checks if the given group or project path falls within one of the AllowedGroups.
func (c *Claims) IsGroupAllowed(path string) bool {
	if len(c.AllowedGroups) == 0 {
		return true
	}
	normalizedPath := strings.ToLower(strings.Trim(path, "/"))
	for _, allowed := range c.AllowedGroups {
		allowed = strings.ToLower(strings.Trim(allowed, "/"))
		if allowed == "" || allowed == "*" {
			return true
		}
		if normalizedPath == allowed || strings.HasPrefix(normalizedPath, allowed+"/") {
			return true
		}
	}
	return false
}

// ValidateScope checks both GitLab host and target project/group hierarchies against license boundaries.
func (c *Claims) ValidateScope(rawURL string, targetPaths []string) error {
	if !c.IsHostAllowed(rawURL) {
		targetHost := ExtractHost(rawURL)
		return fmt.Errorf("COMMERCIAL LICENSE HOST MISMATCH: License is restricted to GitLab host(s) %v, but active target is '%s'. Please obtain a commercial license for this host or contact licensing@divmora.com", c.AllowedHosts, targetHost)
	}

	if len(c.AllowedGroups) > 0 && len(targetPaths) > 0 {
		for _, path := range targetPaths {
			if !c.IsGroupAllowed(path) {
				return fmt.Errorf("COMMERCIAL LICENSE GROUP MISMATCH: License is restricted to GitLab group hierarchy %v, but targeted resource '%s' falls outside permitted groups. Please contact licensing@divmora.com to extend your license scope", c.AllowedGroups, path)
			}
		}
	}
	return nil
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
