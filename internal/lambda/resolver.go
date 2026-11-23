package lambda

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// ConfigResolver loads and resolves PolicyConfig from multiple event sources.
type ConfigResolver struct {
	s3Client  config.S3ClientAPI
	lookupEnv config.EnvLookupFunc
}

// NewConfigResolver creates a new ConfigResolver.
func NewConfigResolver(s3Client config.S3ClientAPI, lookupEnv config.EnvLookupFunc) *ConfigResolver {
	return &ConfigResolver{
		s3Client:  s3Client,
		lookupEnv: lookupEnv,
	}
}

// ResolveFromDirectPayload resolves PolicyConfig from a DirectInvocationPayload with overrides.
func (r *ConfigResolver) ResolveFromDirectPayload(ctx context.Context, payload *DirectInvocationPayload) (*config.PolicyConfig, string, error) {
	var cfg *config.PolicyConfig
	var sourceDesc string
	var err error

	if payload == nil {
		payload = &DirectInvocationPayload{}
	}

	if payload.Action != "" {
		if strings.EqualFold(payload.Action, "dry_run") || strings.EqualFold(payload.Action, "plan") {
			b := true
			payload.DryRun = &b
		} else if strings.EqualFold(payload.Action, "apply") {
			b := false
			payload.DryRun = &b
		}
	}

	// 1. Direct inline configuration content
	inlineContent := payload.ConfigContent
	if inlineContent == "" {
		inlineContent = payload.ConfigYAML
	}
	if inlineContent == "" {
		inlineContent = payload.ConfigJSON
	}

	// Configs from the event (inline, or an S3 object the event names) are
	// untrusted: they may not read local files, expand secrets from the
	// environment, or redirect the GitLab client. The operator's own
	// CONFIG_* fallback below stays fully trusted.
	eventOpts := config.LoadOptions{LoaderOptions: r.loaderOptions(), Untrusted: r.untrustedPolicy()}

	if payload.Config != nil {
		cfg = payload.Config
		sourceDesc = "inline:payload"
		cfg.SetDefaults()
		if err := config.SanitizeUntrusted(ctx, cfg, eventOpts.Untrusted, eventOpts); err != nil {
			return nil, sourceDesc, err
		}
	} else if strings.TrimSpace(inlineContent) != "" {
		cfg, sourceDesc, err = config.LoadInline(ctx, []byte(inlineContent), "inline:payload", eventOpts)
		if err != nil {
			return nil, sourceDesc, fmt.Errorf("failed to load inline config payload: %w", err)
		}
	} else if strings.TrimSpace(payload.ConfigS3URI) != "" {
		// 2. S3 URI in payload. Only s3:// is accepted: config.Load also reads
		// local files and stdin, which an invocation payload must not reach.
		if err := requireS3URI(payload.ConfigS3URI); err != nil {
			return nil, "", err
		}
		cfg, sourceDesc, err = config.LoadFromS3(ctx, payload.ConfigS3URI, eventOpts)
		if err != nil {
			return nil, sourceDesc, fmt.Errorf("failed to load config from S3 URI %q: %w", strings.TrimSpace(payload.ConfigS3URI), err)
		}
	} else {
		// 3. Fallback to the operator's environment variables / default candidate files
		cfg, sourceDesc, err = config.Load(ctx, "", config.LoadOptions{LoaderOptions: r.loaderOptions()})
		if err != nil {
			return nil, sourceDesc, fmt.Errorf("failed to load default configuration: %w", err)
		}
	}

	// Apply runtime parameter overrides from payload
	r.applyPayloadOverrides(cfg, payload)

	return cfg, sourceDesc, nil
}

// ResolveFromS3URI resolves PolicyConfig directly from an S3 URI.
func (r *ConfigResolver) ResolveFromS3URI(ctx context.Context, s3URI string) (*config.PolicyConfig, string, error) {
	if err := requireS3URI(s3URI); err != nil {
		return nil, "", err
	}
	// The bucket and key come from the event, so the object is untrusted.
	return config.LoadFromS3(ctx, s3URI, config.LoadOptions{
		LoaderOptions: r.loaderOptions(),
		Untrusted:     r.untrustedPolicy(),
	})
}

// EventConfigAllowedEnvVar names the environment variable listing extra
// variables (comma-separated) that event-supplied configs may expand.
const EventConfigAllowedEnvVar = "GLFG_EVENT_CONFIG_ALLOWED_ENV"

func (r *ConfigResolver) env(name string) (string, bool) {
	if r.lookupEnv != nil {
		return r.lookupEnv(name)
	}
	return os.LookupEnv(name)
}

func (r *ConfigResolver) loaderOptions() []config.LoaderOption {
	opts := []config.LoaderOption{}
	if r.lookupEnv != nil {
		opts = append(opts, config.WithEnvLookup(r.lookupEnv))
	}
	if r.s3Client != nil {
		opts = append(opts, config.WithS3Client(r.s3Client))
	}
	return opts
}

// untrustedPolicy builds the restrictions for event-supplied configs from the
// operator's environment.
func (r *ConfigResolver) untrustedPolicy() *config.UntrustedPolicy {
	allowed := []string{"GITLAB_BASE_URL", "CI_API_V4_URL"}
	if extra, ok := r.env(EventConfigAllowedEnvVar); ok {
		for _, name := range strings.Split(extra, ",") {
			if name = strings.TrimSpace(name); name != "" {
				allowed = append(allowed, name)
			}
		}
	}

	trustedURL := gitlab.DefaultGitLabBaseURL
	for _, name := range []string{"GITLAB_BASE_URL", "CI_API_V4_URL"} {
		if v, ok := r.env(name); ok && strings.TrimSpace(v) != "" {
			trustedURL = strings.TrimSpace(v)
			break
		}
	}

	return &config.UntrustedPolicy{AllowedEnv: allowed, TrustedGitLabBaseURL: trustedURL}
}

// requireS3URI rejects any configuration source that is not an s3:// URI with
// a bucket and key, so Lambda events cannot make the loader open local paths.
func requireS3URI(uri string) error {
	uri = strings.TrimSpace(uri)
	rest, ok := strings.CutPrefix(uri, "s3://")
	bucket, key, _ := strings.Cut(rest, "/")
	if !ok || bucket == "" || strings.TrimSpace(key) == "" {
		return fmt.Errorf("invalid config_s3_uri: must be an s3://bucket/key URI")
	}
	return nil
}

func (r *ConfigResolver) applyPayloadOverrides(cfg *config.PolicyConfig, p *DirectInvocationPayload) {
	if cfg == nil || p == nil {
		return
	}

	if p.DryRun != nil {
		cfg.Settings.DryRun = p.DryRun
	}
	if p.Concurrency > 0 {
		cfg.Settings.Concurrency = p.Concurrency
	}
	if p.LogLevel != "" {
		cfg.Settings.LogLevel = p.LogLevel
	}
	if p.LogFormat != "" {
		cfg.Settings.LogFormat = p.LogFormat
	}

	// Overlay target selector overrides if provided
	if len(p.GroupIDsInclude) > 0 || len(p.GroupIDsExclude) > 0 || len(p.GroupPathsInclude) > 0 || len(p.GroupPathsExclude) > 0 {
		if cfg.Targets.GroupSelector == nil {
			cfg.Targets.GroupSelector = &config.GroupSelector{}
		}
		if len(p.GroupIDsInclude) > 0 {
			cfg.Targets.GroupSelector.GroupIDsInclude = p.GroupIDsInclude
		}
		if len(p.GroupIDsExclude) > 0 {
			cfg.Targets.GroupSelector.GroupIDsExclude = p.GroupIDsExclude
		}
		if len(p.GroupPathsInclude) > 0 {
			cfg.Targets.GroupSelector.GroupPathsInclude = p.GroupPathsInclude
		}
		if len(p.GroupPathsExclude) > 0 {
			cfg.Targets.GroupSelector.GroupPathsExclude = p.GroupPathsExclude
		}
	}

	if len(p.NamespacesInclude) > 0 || len(p.NamespacesExclude) > 0 || p.ProjectRegexInclude != "" || p.ProjectRegexExclude != "" {
		if cfg.Targets.ProjectSelector == nil {
			cfg.Targets.ProjectSelector = &config.ProjectSelector{}
		}
		if len(p.NamespacesInclude) > 0 {
			cfg.Targets.ProjectSelector.NamespacesInclude = p.NamespacesInclude
		}
		if len(p.NamespacesExclude) > 0 {
			cfg.Targets.ProjectSelector.NamespacesExclude = p.NamespacesExclude
		}
		if p.ProjectRegexInclude != "" {
			cfg.Targets.ProjectSelector.ProjectNameRegexInclude = p.ProjectRegexInclude
		}
		if p.ProjectRegexExclude != "" {
			cfg.Targets.ProjectSelector.ProjectNameRegexExclude = p.ProjectRegexExclude
		}
	}
}
