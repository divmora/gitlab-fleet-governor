package config

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// GroupSettingsConfig declares group-level settings governed on every
// targeted group. Unset fields are left as they are.
type GroupSettingsConfig struct {
	// Visibility is the group visibility: "private", "internal" or "public".
	Visibility string `yaml:"visibility,omitempty" json:"visibility,omitempty"`

	// RequireTwoFactorAuth requires members to enable two-factor authentication.
	RequireTwoFactorAuth *bool `yaml:"require_two_factor_authentication,omitempty" json:"require_two_factor_authentication,omitempty"`

	// TwoFactorGracePeriod is the time in hours members have to enable 2FA.
	TwoFactorGracePeriod *int64 `yaml:"two_factor_grace_period,omitempty" json:"two_factor_grace_period,omitempty"`

	// ProjectCreationLevel is who may create projects: "noone", "owner",
	// "maintainer", "developer" or "administrator".
	ProjectCreationLevel string `yaml:"project_creation_level,omitempty" json:"project_creation_level,omitempty"`

	// SubGroupCreationLevel is who may create subgroups: "owner" or "maintainer".
	SubGroupCreationLevel string `yaml:"subgroup_creation_level,omitempty" json:"subgroup_creation_level,omitempty"`

	// MembershipLock prevents adding members to projects in the group (Premium).
	MembershipLock *bool `yaml:"membership_lock,omitempty" json:"membership_lock,omitempty"`

	// ShareWithGroupLock prevents sharing projects in the group with other groups.
	ShareWithGroupLock *bool `yaml:"share_with_group_lock,omitempty" json:"share_with_group_lock,omitempty"`

	// PreventForkingOutsideGroup blocks forks to namespaces outside the group (Premium).
	PreventForkingOutsideGroup *bool `yaml:"prevent_forking_outside_group,omitempty" json:"prevent_forking_outside_group,omitempty"`

	// RequestAccessEnabled lets users request access to the group.
	RequestAccessEnabled *bool `yaml:"request_access_enabled,omitempty" json:"request_access_enabled,omitempty"`

	// EmailsEnabled enables email notifications for the group.
	EmailsEnabled *bool `yaml:"emails_enabled,omitempty" json:"emails_enabled,omitempty"`

	// MentionsDisabled disables group mentions.
	MentionsDisabled *bool `yaml:"mentions_disabled,omitempty" json:"mentions_disabled,omitempty"`

	// IPRestrictionRanges limits access to these IP addresses or CIDR
	// ranges (Premium). Order does not matter; an empty list removes the
	// restriction.
	IPRestrictionRanges *[]string `yaml:"ip_restriction_ranges,omitempty" json:"ip_restriction_ranges,omitempty"`

	// AllowedEmailDomains restricts membership to users with these email
	// domains (Premium). Order does not matter; an empty list removes the
	// restriction.
	AllowedEmailDomains *[]string `yaml:"allowed_email_domains,omitempty" json:"allowed_email_domains,omitempty"`

	// DefaultBranchProtection sets the protection applied to the default
	// branch of new projects in the group.
	DefaultBranchProtection *DefaultBranchProtectionConfig `yaml:"default_branch_protection,omitempty" json:"default_branch_protection,omitempty"`
}

// DefaultBranchProtectionConfig mirrors GitLab's
// default_branch_protection_defaults. Unset fields are left as they are.
type DefaultBranchProtectionConfig struct {
	// AllowedToPush lists the access levels allowed to push (e.g. 30, 40).
	AllowedToPush *[]int `yaml:"allowed_to_push,omitempty" json:"allowed_to_push,omitempty"`

	// AllowedToMerge lists the access levels allowed to merge.
	AllowedToMerge *[]int `yaml:"allowed_to_merge,omitempty" json:"allowed_to_merge,omitempty"`

	// AllowForcePush allows force pushes to the default branch.
	AllowForcePush *bool `yaml:"allow_force_push,omitempty" json:"allow_force_push,omitempty"`

	// DeveloperCanInitialPush lets developers push the initial commit.
	DeveloperCanInitialPush *bool `yaml:"developer_can_initial_push,omitempty" json:"developer_can_initial_push,omitempty"`

	// CodeOwnerApprovalRequired requires code owner approval (Premium).
	CodeOwnerApprovalRequired *bool `yaml:"code_owner_approval_required,omitempty" json:"code_owner_approval_required,omitempty"`
}

var emailDomainRegex = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// Access levels accepted by default_branch_protection_defaults.
var branchProtectionAccessLevels = map[int]bool{0: true, 30: true, 40: true, 60: true}

func validateGroupSettings(g *GroupSettingsConfig, prefix string, errs *ValidationErrors) {
	if g == nil {
		return
	}
	add := func(field, msg string, value any) {
		*errs = append(*errs, ValidationError{Field: prefix + "." + field, Message: msg, Value: value})
	}

	switch g.Visibility {
	case "", "private", "internal", "public":
	default:
		add("visibility", "must be private, internal or public", g.Visibility)
	}
	switch g.ProjectCreationLevel {
	case "", "noone", "owner", "maintainer", "developer", "administrator":
	default:
		add("project_creation_level", "must be noone, owner, maintainer, developer or administrator", g.ProjectCreationLevel)
	}
	switch g.SubGroupCreationLevel {
	case "", "owner", "maintainer":
	default:
		add("subgroup_creation_level", "must be owner or maintainer", g.SubGroupCreationLevel)
	}
	if g.TwoFactorGracePeriod != nil && *g.TwoFactorGracePeriod < 0 {
		add("two_factor_grace_period", "must be zero or more hours", *g.TwoFactorGracePeriod)
	}

	if g.IPRestrictionRanges != nil {
		for i, r := range *g.IPRestrictionRanges {
			r = strings.TrimSpace(r)
			if _, _, err := net.ParseCIDR(r); err != nil && net.ParseIP(r) == nil {
				add(fmt.Sprintf("ip_restriction_ranges[%d]", i), "must be an IP address or CIDR range", r)
			}
		}
	}
	if g.AllowedEmailDomains != nil {
		for i, d := range *g.AllowedEmailDomains {
			if !emailDomainRegex.MatchString(strings.TrimSpace(d)) {
				add(fmt.Sprintf("allowed_email_domains[%d]", i), "must be a domain name such as example.com", d)
			}
		}
	}

	if b := g.DefaultBranchProtection; b != nil {
		for name, levels := range map[string]*[]int{"allowed_to_push": b.AllowedToPush, "allowed_to_merge": b.AllowedToMerge} {
			if levels == nil {
				continue
			}
			for i, l := range *levels {
				if !branchProtectionAccessLevels[l] {
					add(fmt.Sprintf("default_branch_protection.%s[%d]", name, i), "access level must be 0 (no one), 30 (developer), 40 (maintainer) or 60 (admin)", l)
				}
			}
		}
	}
}
