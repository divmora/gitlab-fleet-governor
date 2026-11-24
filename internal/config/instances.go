package config

import (
	"fmt"
	"regexp"
	"strings"
)

// instanceNameRegex restricts instance names to short identifiers that are
// safe in report columns, log fields and file names.
var instanceNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// InstanceConfig describes one GitLab instance in a multi-instance policy.
// Its connection fields are the same as settings.gitlab; any left unset
// inherit from settings.gitlab, except base_url and token, which are per
// instance.
type InstanceConfig struct {
	// Name identifies the instance in reports and logs (e.g. "saas").
	Name string `yaml:"name" json:"name"`

	// GitLabSettingsConfig holds the instance's API URL, token, timeouts,
	// rate limits and retry settings.
	GitLabSettingsConfig `yaml:",inline"`

	// Targets selects groups and projects on this instance. When unset, the
	// top-level targets apply.
	Targets *TargetSelectors `yaml:"targets,omitempty" json:"targets,omitempty"`
}

// RunContext carries per-run information that is not part of the policy
// file. It is set on the per-instance copies returned by InstanceRuns.
type RunContext struct {
	// Instance is the name of the instance this copy targets, or "" for a
	// single-instance (settings.gitlab) policy.
	Instance string

	// InstanceCount is the number of instances in the policy.
	InstanceCount int

	// PriorProjects is the number of projects already discovered on earlier
	// instances in the same run, so licence limits apply to the whole fleet.
	PriorProjects int
}

// InstanceRun is one GitLab instance to govern, with the policy as it applies
// to that instance.
type InstanceRun struct {
	Name   string
	Config *PolicyConfig
}

// IsMultiInstance reports whether the policy uses the instances block.
func (p *PolicyConfig) IsMultiInstance() bool {
	return p != nil && len(p.Instances) > 0
}

// InstanceRuns returns one entry per GitLab instance. A single-instance
// policy returns the receiver itself with an empty name. For a policy with an
// instances block, each entry is a shallow copy whose settings.gitlab and
// targets are those of the instance; policies are shared and must be treated
// as read-only.
func (p *PolicyConfig) InstanceRuns() []InstanceRun {
	if !p.IsMultiInstance() {
		return []InstanceRun{{Config: p}}
	}
	runs := make([]InstanceRun, 0, len(p.Instances))
	for _, inst := range p.Instances {
		c := *p
		c.Instances = nil
		c.Settings.GitLab = inst.GitLabSettingsConfig
		if inst.Targets != nil {
			c.Targets = *inst.Targets
		}
		c.Run = RunContext{Instance: inst.Name, InstanceCount: len(p.Instances)}
		runs = append(runs, InstanceRun{Name: inst.Name, Config: &c})
	}
	return runs
}

// setInstanceDefaults fills unset connection fields of each instance from
// settings.gitlab and then from the built-in defaults.
func (p *PolicyConfig) setInstanceDefaults() {
	base := p.Settings.GitLab
	for i := range p.Instances {
		g := &p.Instances[i].GitLabSettingsConfig
		if g.TokenType == "" {
			g.TokenType = base.TokenType
		}
		if g.TimeoutSeconds <= 0 {
			g.TimeoutSeconds = base.TimeoutSeconds
		}
		if g.RateLimitRPS <= 0 {
			g.RateLimitRPS = base.RateLimitRPS
		}
		if g.RateLimitBurst <= 0 {
			g.RateLimitBurst = base.RateLimitBurst
		}
		if g.MaxRetries <= 0 {
			g.MaxRetries = base.MaxRetries
		}
		if g.RetryBaseDelayMs <= 0 {
			g.RetryBaseDelayMs = base.RetryBaseDelayMs
		}
		if g.RetryMaxDelayMs <= 0 {
			g.RetryMaxDelayMs = base.RetryMaxDelayMs
		}
		g.SetDefaults()
	}
}

func validateInstances(cfg *PolicyConfig, errs *ValidationErrors) {
	if !cfg.IsMultiInstance() {
		return
	}
	if strings.TrimSpace(cfg.Settings.GitLab.Token) != "" {
		*errs = append(*errs, ValidationError{
			Field:   "settings.gitlab.token",
			Message: "settings.gitlab.token cannot be combined with instances; set a token on each instance",
		})
	}

	seen := make(map[string]bool, len(cfg.Instances))
	for i := range cfg.Instances {
		inst := &cfg.Instances[i]
		prefix := fmt.Sprintf("instances[%d]", i)
		switch {
		case !instanceNameRegex.MatchString(inst.Name):
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".name",
				Message: "name must be 1-63 lowercase letters, digits, '-' or '_', starting with a letter or digit",
				Value:   inst.Name,
			})
		case seen[inst.Name]:
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".name",
				Message: fmt.Sprintf("duplicate instance name %q", inst.Name),
				Value:   inst.Name,
			})
		}
		seen[inst.Name] = true

		// Each instance needs its own credential. Falling back to
		// GITLAB_TOKEN would send one instance's token to every other one.
		if strings.TrimSpace(inst.Token) == "" {
			*errs = append(*errs, ValidationError{
				Field:   prefix + ".token",
				Message: "token is required for each instance (for example \"${GITLAB_SAAS_TOKEN}\")",
			})
		}
		validateGitLabSettings(&inst.GitLabSettingsConfig, prefix, errs)
		validateTargets(inst.Targets, prefix+".targets", errs)
	}
}
