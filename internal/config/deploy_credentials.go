package config

import (
	"fmt"
	"regexp"
	"strings"
)

// DeployKeysConfig governs the deploy keys enabled on each project.
type DeployKeysConfig struct {
	// Keys lists the managed deploy keys.
	Keys []DeployKeyConfig `yaml:"keys,omitempty" json:"keys,omitempty"`

	// DenyPush makes every deploy key that isn't listed read-only.
	DenyPush *bool `yaml:"deny_push,omitempty" json:"deny_push,omitempty"`

	// MaxAgeDays reports keys older than this many days for rotation.
	MaxAgeDays *int `yaml:"max_age_days,omitempty" json:"max_age_days,omitempty"`

	// Prune removes deploy keys that aren't listed from the project.
	Prune *bool `yaml:"prune,omitempty" json:"prune,omitempty"`
}

// DeployKeyConfig is one managed deploy key. It is matched by fingerprint
// when Key or Fingerprint is set, otherwise by title.
type DeployKeyConfig struct {
	// Title is the key's title. Required.
	Title string `yaml:"title" json:"title"`

	// Key is the SSH public key. Needed to add the key to projects that
	// don't have it; without it a missing key is only reported.
	Key string `yaml:"key,omitempty" json:"key,omitempty"`

	// Fingerprint is the key's SHA256 fingerprint ("SHA256:..."), used for
	// matching when Key is not given.
	Fingerprint string `yaml:"fingerprint,omitempty" json:"fingerprint,omitempty"`

	// CanPush grants write access. Defaults to false (read-only).
	CanPush *bool `yaml:"can_push,omitempty" json:"can_push,omitempty"`
}

// DeployTokensConfig sets the rules every project and group deploy token
// must meet. Tokens are never created: GitLab shows a token's secret only
// once.
type DeployTokensConfig struct {
	// AllowedScopes lists the scopes tokens may have. A token with any
	// other scope is non-compliant.
	AllowedScopes []string `yaml:"allowed_scopes,omitempty" json:"allowed_scopes,omitempty"`

	// RequireExpiresAt makes tokens without an expiry date non-compliant.
	RequireExpiresAt *bool `yaml:"require_expires_at,omitempty" json:"require_expires_at,omitempty"`

	// MaxExpirationDays makes tokens that expire more than this many days
	// from now non-compliant.
	MaxExpirationDays *int `yaml:"max_expiration_days,omitempty" json:"max_expiration_days,omitempty"`

	// RevokeExpired revokes tokens that have expired. Otherwise they are
	// reported.
	RevokeExpired *bool `yaml:"revoke_expired,omitempty" json:"revoke_expired,omitempty"`

	// RevokeNoncompliant revokes tokens that break the rules above.
	// Otherwise they are reported.
	RevokeNoncompliant *bool `yaml:"revoke_noncompliant,omitempty" json:"revoke_noncompliant,omitempty"`
}

// DeployTokenScopes are the scopes GitLab accepts for deploy tokens.
var DeployTokenScopes = map[string]bool{
	"read_repository":        true,
	"read_registry":          true,
	"write_registry":         true,
	"read_package_registry":  true,
	"write_package_registry": true,
	"read_virtual_registry":  true,
	"write_virtual_registry": true,
}

var sha256FingerprintRegex = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}=?$`)

func validateDeployKeys(d *DeployKeysConfig, prefix string, errs *ValidationErrors) {
	if d == nil {
		return
	}
	add := func(field, msg string, value any) {
		*errs = append(*errs, ValidationError{Field: prefix + "." + field, Message: msg, Value: value})
	}
	if d.MaxAgeDays != nil && *d.MaxAgeDays <= 0 {
		add("max_age_days", "must be at least 1", *d.MaxAgeDays)
	}
	titles := map[string]bool{}
	for i, k := range d.Keys {
		f := fmt.Sprintf("keys[%d]", i)
		title := strings.TrimSpace(k.Title)
		if title == "" {
			add(f+".title", "title is required", nil)
		} else if titles[strings.ToLower(title)] {
			add(f+".title", fmt.Sprintf("duplicate key title '%s'", title), title)
		}
		titles[strings.ToLower(title)] = true
		if k.Fingerprint != "" && !sha256FingerprintRegex.MatchString(k.Fingerprint) {
			add(f+".fingerprint", "must be a SHA256 fingerprint such as SHA256:abc...", k.Fingerprint)
		}
		if k.Key != "" {
			if _, err := SSHKeyFingerprint(k.Key); err != nil {
				add(f+".key", err.Error(), nil)
			} else if k.Fingerprint != "" {
				if fp, _ := SSHKeyFingerprint(k.Key); fp != strings.TrimRight(k.Fingerprint, "=") {
					add(f+".fingerprint", "does not match key", k.Fingerprint)
				}
			}
		}
	}
}

func validateDeployTokens(d *DeployTokensConfig, prefix string, errs *ValidationErrors) {
	if d == nil {
		return
	}
	add := func(field, msg string, value any) {
		*errs = append(*errs, ValidationError{Field: prefix + "." + field, Message: msg, Value: value})
	}
	for i, s := range d.AllowedScopes {
		if !DeployTokenScopes[s] {
			add(fmt.Sprintf("allowed_scopes[%d]", i), "unknown deploy token scope", s)
		}
	}
	if d.MaxExpirationDays != nil && *d.MaxExpirationDays <= 0 {
		add("max_expiration_days", "must be at least 1", *d.MaxExpirationDays)
	}
	if d.RevokeNoncompliant != nil && *d.RevokeNoncompliant && len(d.AllowedScopes) == 0 &&
		(d.RequireExpiresAt == nil || !*d.RequireExpiresAt) && d.MaxExpirationDays == nil {
		add("revoke_noncompliant", "needs allowed_scopes, require_expires_at or max_expiration_days to decide what is non-compliant", true)
	}
}
