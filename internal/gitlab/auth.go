package gitlab

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// TokenType represents supported GitLab authentication token types.
type TokenType string

const (
	TokenTypePrivate TokenType = "private_token"
	TokenTypeOAuth   TokenType = "oauth"
	TokenTypeJob     TokenType = "job_token"

	DefaultGitLabBaseURL = "https://gitlab.com/api/v4"
)

// EnvLookupFunc abstracts environment variable lookup for isolated testing.
type EnvLookupFunc = config.EnvLookupFunc

// ResolvedAuth contains resolved credentials and connection endpoint.
type ResolvedAuth struct {
	BaseURL       string
	Token         string
	TokenType     TokenType
	SourceToken   string
	SourceBaseURL string
}

// ResolveAuth resolves the GitLab token, token type, and base URL based on
// deterministic precedence rules:
//
// Token precedence:
//  1. Config file (settings.gitlab.token)
//  2. PRIVATE_TOKEN env var
//  3. GITLAB_TOKEN env var
//  4. OAUTH_TOKEN / GITLAB_OAUTH_TOKEN env var
//  5. CI_JOB_TOKEN env var
//
// Base URL precedence:
//  1. Config file (settings.gitlab.base_url, if non-empty and non-default)
//  2. GITLAB_BASE_URL env var
//  3. CI_API_V4_URL env var
//  4. Config file default / DefaultGitLabBaseURL ("https://gitlab.com/api/v4")
func ResolveAuth(cfg *config.GitLabSettingsConfig, lookup ...EnvLookupFunc) (*ResolvedAuth, error) {
	lookupFn := os.LookupEnv
	if len(lookup) > 0 && lookup[0] != nil {
		lookupFn = lookup[0]
	}

	res := &ResolvedAuth{
		BaseURL:   DefaultGitLabBaseURL,
		TokenType: TokenTypePrivate,
	}

	// 1. Resolve Base URL
	if cfg != nil && cfg.BaseURL != "" && cfg.BaseURL != DefaultGitLabBaseURL {
		res.BaseURL = cfg.BaseURL
		res.SourceBaseURL = "config"
	} else if val, ok := lookupFn("GITLAB_BASE_URL"); ok && strings.TrimSpace(val) != "" {
		res.BaseURL = strings.TrimSpace(val)
		res.SourceBaseURL = "GITLAB_BASE_URL"
	} else if val, ok := lookupFn("CI_API_V4_URL"); ok && strings.TrimSpace(val) != "" {
		res.BaseURL = strings.TrimSpace(val)
		res.SourceBaseURL = "CI_API_V4_URL"
	} else if cfg != nil && cfg.BaseURL != "" {
		res.BaseURL = cfg.BaseURL
		res.SourceBaseURL = "config_default"
	} else {
		res.BaseURL = DefaultGitLabBaseURL
		res.SourceBaseURL = "default"
	}

	// Normalize Base URL (strip trailing slashes)
	res.BaseURL = strings.TrimRight(res.BaseURL, "/")

	// Validate Base URL
	parsedURL, err := url.ParseRequestURI(res.BaseURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("invalid GitLab base URL '%s': must be a valid http or https URL", res.BaseURL)
	}

	// 2. Resolve Token and Token Type
	if cfg != nil && strings.TrimSpace(cfg.Token) != "" {
		res.Token = strings.TrimSpace(cfg.Token)
		res.SourceToken = "config"
		if cfg.TokenType != "" {
			res.TokenType = TokenType(strings.ToLower(cfg.TokenType))
		}
	} else if val, ok := lookupFn("PRIVATE_TOKEN"); ok && strings.TrimSpace(val) != "" {
		res.Token = strings.TrimSpace(val)
		res.TokenType = TokenTypePrivate
		res.SourceToken = "PRIVATE_TOKEN"
	} else if val, ok := lookupFn("GITLAB_TOKEN"); ok && strings.TrimSpace(val) != "" {
		res.Token = strings.TrimSpace(val)
		res.TokenType = TokenTypePrivate
		res.SourceToken = "GITLAB_TOKEN"
	} else if val, ok := lookupFn("OAUTH_TOKEN"); ok && strings.TrimSpace(val) != "" {
		res.Token = strings.TrimSpace(val)
		res.TokenType = TokenTypeOAuth
		res.SourceToken = "OAUTH_TOKEN"
	} else if val, ok := lookupFn("GITLAB_OAUTH_TOKEN"); ok && strings.TrimSpace(val) != "" {
		res.Token = strings.TrimSpace(val)
		res.TokenType = TokenTypeOAuth
		res.SourceToken = "GITLAB_OAUTH_TOKEN"
	} else if val, ok := lookupFn("CI_JOB_TOKEN"); ok && strings.TrimSpace(val) != "" {
		res.Token = strings.TrimSpace(val)
		res.TokenType = TokenTypeJob
		res.SourceToken = "CI_JOB_TOKEN"
	}

	// Validate token presence
	if res.Token == "" {
		return nil, errors.New("no GitLab authentication token found: please set settings.gitlab.token in config, or export PRIVATE_TOKEN, GITLAB_TOKEN, OAUTH_TOKEN, or CI_JOB_TOKEN")
	}

	// Override token type from config if explicitly specified
	if cfg != nil && cfg.TokenType != "" {
		res.TokenType = TokenType(strings.ToLower(cfg.TokenType))
	}

	// Validate token type value
	switch res.TokenType {
	case TokenTypePrivate, TokenTypeOAuth, TokenTypeJob:
		// Valid
	default:
		return nil, fmt.Errorf("unsupported token type '%s': must be 'private_token', 'oauth', or 'job_token'", res.TokenType)
	}

	return res, nil
}
