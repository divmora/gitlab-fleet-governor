package lambda

import (
	"context"
	"fmt"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
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

	if payload.Config != nil {
		cfg = payload.Config
		sourceDesc = "inline:payload"
	} else if strings.TrimSpace(inlineContent) != "" {
		cfg, err = config.LoadFromBytes(ctx, []byte(inlineContent), r.lookupEnv)
		if err != nil {
			return nil, "inline:payload", fmt.Errorf("failed to load inline config payload: %w", err)
		}
		sourceDesc = "inline:payload"
	} else if strings.TrimSpace(payload.ConfigS3URI) != "" {
		// 2. S3 URI in payload
		loaderOpts := []config.LoaderOption{}
		if r.lookupEnv != nil {
			loaderOpts = append(loaderOpts, config.WithEnvLookup(r.lookupEnv))
		}
		if r.s3Client != nil {
			loaderOpts = append(loaderOpts, config.WithS3Client(r.s3Client))
		}
		cfg, sourceDesc, err = config.Load(ctx, strings.TrimSpace(payload.ConfigS3URI), config.LoadOptions{
			LoaderOptions: loaderOpts,
		})
		if err != nil {
			return nil, sourceDesc, fmt.Errorf("failed to load config from S3 URI %q: %w", payload.ConfigS3URI, err)
		}
	} else {
		// 3. Fallback to environment variables / default candidate files
		loaderOpts := []config.LoaderOption{}
		if r.lookupEnv != nil {
			loaderOpts = append(loaderOpts, config.WithEnvLookup(r.lookupEnv))
		}
		if r.s3Client != nil {
			loaderOpts = append(loaderOpts, config.WithS3Client(r.s3Client))
		}
		cfg, sourceDesc, err = config.Load(ctx, "", config.LoadOptions{
			LoaderOptions: loaderOpts,
		})
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
	loaderOpts := []config.LoaderOption{}
	if r.lookupEnv != nil {
		loaderOpts = append(loaderOpts, config.WithEnvLookup(r.lookupEnv))
	}
	if r.s3Client != nil {
		loaderOpts = append(loaderOpts, config.WithS3Client(r.s3Client))
	}
	return config.Load(ctx, s3URI, config.LoadOptions{
		LoaderOptions: loaderOpts,
	})
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
