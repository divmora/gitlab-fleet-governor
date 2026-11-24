package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// defaultGitLabBaseURL mirrors the default applied by GitLabSettingsConfig.SetDefaults.
const defaultGitLabBaseURL = "https://gitlab.com/api/v4"

// UntrustedPolicy restricts a configuration that arrives from an untrusted
// source, such as an event payload, as opposed to the operator's own config
// file, ConfigMap or CONFIG_* environment. Such a config must not be able to
// read local files, expand the process environment, or send the operator's
// GitLab token to another host.
type UntrustedPolicy struct {
	// AllowedEnv lists the environment variables that ${VAR} references may
	// expand. Any other reference resolves as unset, so secrets such as
	// GITLAB_TOKEN or DIVMORA_LICENSE_KEY are never copied into the config;
	// they still reach the client and licence check through their usual
	// environment fallbacks when the corresponding fields are left empty.
	AllowedEnv []string

	// TrustedGitLabBaseURL is the GitLab API URL the operator configured. An
	// untrusted config may leave settings.gitlab.base_url unset or repeat
	// this value, but may not point the client anywhere else.
	TrustedGitLabBaseURL string
}

// lookup wraps base so that only allow-listed variables are visible.
func (p *UntrustedPolicy) lookup(base EnvLookupFunc) EnvLookupFunc {
	allowed := make(map[string]bool, len(p.AllowedEnv))
	for _, name := range p.AllowedEnv {
		if name = strings.TrimSpace(name); name != "" {
			allowed[name] = true
		}
	}
	return func(name string) (string, bool) {
		if !allowed[name] || base == nil {
			return "", false
		}
		return base(name)
	}
}

// apply checks cfg against the policy and resolves s3:// content_file
// references into inline content, so no local path from an untrusted config
// is ever opened later on.
func (p *UntrustedPolicy) apply(ctx context.Context, loader *Loader, cfg *PolicyConfig) error {
	var problems []string

	// With no token of its own, the client falls back to the operator's
	// GITLAB_TOKEN (or PRIVATE_TOKEN, OAUTH_TOKEN, CI_JOB_TOKEN), so the
	// config must not choose where that token is sent. A config that brings
	// its own token may target any GitLab instance.
	if strings.TrimSpace(cfg.Settings.GitLab.Token) == "" {
		if baseURL := normalizeBaseURL(cfg.Settings.GitLab.BaseURL); baseURL != "" &&
			baseURL != normalizeBaseURL(defaultGitLabBaseURL) &&
			baseURL != normalizeBaseURL(p.TrustedGitLabBaseURL) {
			problems = append(problems, "settings.gitlab.base_url must be unset or match the operator's GitLab URL unless the config supplies its own settings.gitlab.token")
		}
	}

	// An event config must not choose where findings are sent: anyone who
	// can invoke the function could otherwise collect them.
	if !cfg.Settings.Notifications.IsEmpty() {
		problems = append(problems, "settings.notifications is not allowed; configure notifications in the operator's own config or environment")
	}

	lic := cfg.Settings.License
	if strings.TrimSpace(lic.File) != "" {
		problems = append(problems, "settings.license.file is not allowed; provide the licence through the operator's environment")
	}
	if strings.TrimSpace(lic.CRLFile) != "" {
		problems = append(problems, "settings.license.crl_file is not allowed")
	}
	if strings.TrimSpace(lic.CRLURL) != "" {
		problems = append(problems, "settings.license.crl_url is not allowed")
	}

	files := cfg.Policies.RepositoryFiles
	for i := range files {
		ref := strings.TrimSpace(files[i].ContentFile)
		if ref == "" {
			continue
		}
		if !strings.HasPrefix(ref, "s3://") {
			problems = append(problems, fmt.Sprintf("policies.repository_files[%d].content_file must be an s3:// URI", i))
			continue
		}
		data, err := loader.readFromS3(ctx, ref)
		if err != nil {
			problems = append(problems, fmt.Sprintf("policies.repository_files[%d].content_file: %v", i, err))
			continue
		}
		files[i].Content = string(data)
		files[i].ContentFile = ""
	}

	// Instance policy overrides are merged over the top-level policies later,
	// so their repository files need the same treatment here.
	for i := range cfg.Instances {
		problems = append(problems, p.resolveOverrideFiles(ctx, loader, i, cfg.Instances[i].Policies)...)
	}
	if len(problems) == 0 {
		// Backstop: after merging, no instance may be left with a file
		// reference. Top-level references were inlined above and override
		// references by resolveOverrideFiles.
		for i, inst := range cfg.Instances {
			if len(inst.Policies) == 0 {
				continue
			}
			merged, err := MergePolicies(cfg.Policies, inst.Policies)
			if err != nil {
				continue // reported by validation
			}
			for j, f := range merged.RepositoryFiles {
				if strings.TrimSpace(f.ContentFile) != "" {
					problems = append(problems, fmt.Sprintf("instances[%d].policies.repository_files[%d].content_file must be an s3:// URI", i, j))
				}
			}
		}
	}

	if len(problems) > 0 {
		return errors.New("untrusted configuration rejected: " + strings.Join(problems, "; "))
	}
	return nil
}

// resolveOverrideFiles applies the content_file rule to the repository_files
// list of one instance's policy overrides, inlining s3:// objects.
func (p *UntrustedPolicy) resolveOverrideFiles(ctx context.Context, loader *Loader, idx int, overrides map[string]any) []string {
	raw, ok := overrides["repository_files"]
	if !ok || raw == nil {
		return nil
	}
	if m, isMap := asStringMap(raw); isMap {
		raw = m[ReplaceMarker] // {$replace: [...]}
	}
	list, ok := raw.([]any)
	if !ok {
		return nil // anything else is rejected when the policies are merged
	}
	var problems []string
	for j, item := range list {
		entry, ok := asStringMap(item)
		if !ok {
			continue
		}
		field := fmt.Sprintf("instances[%d].policies.repository_files[%d].content_file", idx, j)
		val, present := entry["content_file"]
		if !present || val == nil {
			continue
		}
		ref, isString := val.(string)
		ref = strings.TrimSpace(ref)
		switch {
		case !isString:
			problems = append(problems, field+" must be an s3:// URI")
			continue
		case ref == "":
			continue
		case !strings.HasPrefix(ref, "s3://"):
			problems = append(problems, field+" must be an s3:// URI")
			continue
		}
		data, err := loader.readFromS3(ctx, ref)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", field, err))
			continue
		}
		entry["content"] = string(data)
		delete(entry, "content_file")
		list[j] = entry
	}
	return problems
}

// SanitizeUntrusted applies policy to an already-parsed configuration, such
// as one decoded directly from an event payload. Loader options in opt supply
// the S3 client used to resolve s3:// content_file references.
func SanitizeUntrusted(ctx context.Context, cfg *PolicyConfig, policy *UntrustedPolicy, opt LoadOptions) error {
	if cfg == nil || policy == nil {
		return nil
	}
	return policy.apply(ctx, NewLoader(opt.LoaderOptions...), cfg)
}

func normalizeBaseURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}
