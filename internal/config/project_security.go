package config

import (
	"fmt"
	"strings"
)

// JobTokenScopeConfig governs a project's CI/CD job token access: whether
// access is limited to an allowlist, and which projects and groups are on
// it.
type JobTokenScopeConfig struct {
	// LimitAccess turns on "Limit access to this project", so only
	// allowlisted projects and groups can use their job tokens here.
	LimitAccess *bool `yaml:"limit_access,omitempty" json:"limit_access,omitempty"`

	// AllowedProjects lists projects (full path or numeric ID) whose job
	// tokens may access the project. Unset leaves the list unmanaged.
	AllowedProjects *[]string `yaml:"allowed_projects,omitempty" json:"allowed_projects,omitempty"`

	// AllowedGroups lists groups (full path or numeric ID) whose projects'
	// job tokens may access the project. Unset leaves the list unmanaged.
	AllowedGroups *[]string `yaml:"allowed_groups,omitempty" json:"allowed_groups,omitempty"`

	// Prune removes allowlist entries that aren't listed. The project itself
	// is never removed.
	Prune *bool `yaml:"prune,omitempty" json:"prune,omitempty"`
}

// SecretPushProtectionConfig governs secret push protection (Ultimate).
type SecretPushProtectionConfig struct {
	// Enabled turns secret push protection on or off.
	Enabled *bool `yaml:"enabled" json:"enabled"`
}

func validateJobTokenScope(j *JobTokenScopeConfig, prefix string, errs *ValidationErrors) {
	if j == nil {
		return
	}
	for name, list := range map[string]*[]string{"allowed_projects": j.AllowedProjects, "allowed_groups": j.AllowedGroups} {
		if list == nil {
			continue
		}
		seen := map[string]bool{}
		for i, ref := range *list {
			ref = strings.Trim(strings.TrimSpace(ref), "/")
			field := fmt.Sprintf("%s.%s[%d]", prefix, name, i)
			switch {
			case ref == "":
				*errs = append(*errs, ValidationError{Field: field, Message: "must be a full path or numeric ID"})
			case seen[strings.ToLower(ref)]:
				*errs = append(*errs, ValidationError{Field: field, Message: fmt.Sprintf("duplicate entry '%s'", ref), Value: ref})
			}
			seen[strings.ToLower(ref)] = true
		}
	}
	if j.Prune != nil && *j.Prune && j.AllowedProjects == nil && j.AllowedGroups == nil {
		*errs = append(*errs, ValidationError{Field: prefix + ".prune", Message: "prune needs allowed_projects or allowed_groups to know what to keep"})
	}
}

func validateSecretPushProtection(s *SecretPushProtectionConfig, prefix string, errs *ValidationErrors) {
	if s != nil && s.Enabled == nil {
		*errs = append(*errs, ValidationError{Field: prefix + ".enabled", Message: "enabled is required (true or false)"})
	}
}
