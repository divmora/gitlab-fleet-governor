package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadOptions configures the config loading and parsing behavior.
type LoadOptions struct {
	LoaderOptions []LoaderOption
	SkipValidate  bool
}

// Load executes the full 4-stage pipeline: Source Ingestion -> Envsubst -> Strict Unmarshal -> Semantic Validation.
func Load(ctx context.Context, configPath string, opts ...LoadOptions) (*PolicyConfig, string, error) {
	var opt LoadOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	loader := NewLoader(opt.LoaderOptions...)

	// Stage 1: Load raw configuration bytes
	rawBytes, sourceDesc, err := loader.LoadRaw(ctx, configPath)
	if err != nil {
		return nil, sourceDesc, fmt.Errorf("config load error: %w", err)
	}

	return parseLoaded(loader, rawBytes, sourceDesc, opt)
}

// LoadFromS3 loads, expands, parses, and validates configuration from an
// s3://bucket/key URI only. Unlike Load it never reads local files, stdin or
// CONFIG_* sources, so it is safe for URIs taken from untrusted events.
func LoadFromS3(ctx context.Context, s3URI string, opts ...LoadOptions) (*PolicyConfig, string, error) {
	var opt LoadOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	loader := NewLoader(opt.LoaderOptions...)
	sourceDesc := strings.TrimSpace(s3URI)

	rawBytes, err := loader.readFromS3(ctx, sourceDesc)
	if err != nil {
		return nil, sourceDesc, fmt.Errorf("config load error: %w", err)
	}
	if len(bytes.TrimSpace(rawBytes)) == 0 {
		return nil, sourceDesc, fmt.Errorf("config load error: %w", errors.New("empty configuration content"))
	}

	return parseLoaded(loader, rawBytes, sourceDesc, opt)
}

// parseLoaded runs env substitution, strict unmarshaling, defaults and
// validation on raw configuration bytes.
func parseLoaded(loader *Loader, rawBytes []byte, sourceDesc string, opt LoadOptions) (*PolicyConfig, string, error) {
	// Stage 2: Environment variable substitution
	expandedStr, err := ExpandEnvWithLookup(string(rawBytes), loader.lookupEnv)
	if err != nil {
		return nil, sourceDesc, fmt.Errorf("config env substitution error in source '%s': %w", sourceDesc, err)
	}

	// Stage 3: Strict Schema Unmarshaling (YAML/JSON)
	cfg := &PolicyConfig{}
	if err := UnmarshalStrict([]byte(expandedStr), cfg); err != nil {
		return nil, sourceDesc, fmt.Errorf("config unmarshal error in source '%s': %w", sourceDesc, err)
	}

	// Apply default values
	cfg.SetDefaults()

	// Stage 4: Semantic Validation
	if !opt.SkipValidate {
		if err := cfg.Validate(); err != nil {
			return nil, sourceDesc, fmt.Errorf("config validation error in source '%s': %w", sourceDesc, err)
		}
	}

	return cfg, sourceDesc, nil
}

// LoadFromFile loads, expands, parses, and validates configuration from a file path.
func LoadFromFile(ctx context.Context, path string) (*PolicyConfig, error) {
	cfg, _, err := Load(ctx, path)
	return cfg, err
}

// LoadFromBytes loads, expands, parses, and validates configuration directly from byte slice.
func LoadFromBytes(ctx context.Context, data []byte, lookup ...EnvLookupFunc) (*PolicyConfig, error) {
	envLookup := os.LookupEnv
	if len(lookup) > 0 && lookup[0] != nil {
		envLookup = lookup[0]
	}

	expandedStr, err := ExpandEnvWithLookup(string(data), envLookup)
	if err != nil {
		return nil, fmt.Errorf("config env substitution error: %w", err)
	}

	cfg := &PolicyConfig{}
	if err := UnmarshalStrict([]byte(expandedStr), cfg); err != nil {
		return nil, fmt.Errorf("config unmarshal error: %w", err)
	}

	cfg.SetDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation error: %w", err)
	}

	return cfg, nil
}
