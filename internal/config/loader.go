package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// DefaultCandidateFiles contains default configuration filenames scanned if none specified.
var DefaultCandidateFiles = []string{
	"gitlab-fleet-governor.yaml",
	"gitlab-fleet-governor.yml",
	"gitlab-fleet-governor.json",
	".gitlab-fleet-governor.yaml",
	".gitlab-fleet-governor.yml",
	".gitlab-fleet-governor.json",
	"config.yaml",
	"config.yml",
	"config.json",
}

// Loader loads raw configuration from multiple sources.
type Loader struct {
	stdin      io.Reader
	lookupEnv  EnvLookupFunc
	s3Client   S3ClientAPI
	s3InitOnce sync.Once
	s3InitErr  error
}

// LoaderOption configures a Loader.
type LoaderOption func(*Loader)

// WithStdin sets the standard input reader.
func WithStdin(r io.Reader) LoaderOption {
	return func(l *Loader) {
		l.stdin = r
	}
}

// WithEnvLookup sets a custom environment lookup function.
func WithEnvLookup(lookup EnvLookupFunc) LoaderOption {
	return func(l *Loader) {
		l.lookupEnv = lookup
	}
}

// WithS3Client injects a mock or custom S3 client.
func WithS3Client(client S3ClientAPI) LoaderOption {
	return func(l *Loader) {
		l.s3Client = client
	}
}

// NewLoader creates a new Loader instance with given options.
func NewLoader(opts ...LoaderOption) *Loader {
	l := &Loader{
		stdin:     os.Stdin,
		lookupEnv: os.LookupEnv,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// LoadRaw loads the raw unexpanded configuration bytes from the resolved source.
func (l *Loader) LoadRaw(ctx context.Context, configPath string) ([]byte, string, error) {
	// 1. Explicit CLI Flag / parameter
	if strings.TrimSpace(configPath) != "" {
		data, err := l.readFromSource(ctx, strings.TrimSpace(configPath))
		if err != nil {
			return nil, configPath, err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, configPath, errors.New("empty configuration content")
		}
		return data, configPath, nil
	}

	// 2. Direct inline content environment variables
	for _, envKey := range []string{"CONFIG_CONTENT", "CONFIG_YAML", "CONFIG_JSON"} {
		if val, ok := l.lookupEnv(envKey); ok && strings.TrimSpace(val) != "" {
			data := []byte(val)
			if len(bytes.TrimSpace(data)) == 0 {
				return nil, fmt.Sprintf("env:%s", envKey), errors.New("empty configuration content")
			}
			return data, fmt.Sprintf("env:%s", envKey), nil
		}
	}

	// 3. Source reference environment variables
	for _, envKey := range []string{"CONFIG_SOURCE", "CONFIG_FILE"} {
		if val, ok := l.lookupEnv(envKey); ok && strings.TrimSpace(val) != "" {
			source := strings.TrimSpace(val)
			data, err := l.readFromSource(ctx, source)
			desc := fmt.Sprintf("env:%s(%s)", envKey, source)
			if err != nil {
				return nil, desc, err
			}
			if len(bytes.TrimSpace(data)) == 0 {
				return nil, desc, errors.New("empty configuration content")
			}
			return data, desc, nil
		}
	}

	// 4. Default candidate filenames in current working directory
	for _, candidate := range DefaultCandidateFiles {
		if _, err := os.Stat(candidate); err == nil {
			data, err := os.ReadFile(candidate)
			if err != nil {
				return nil, "", fmt.Errorf("failed to read default config file '%s': %w", candidate, err)
			}
			if len(bytes.TrimSpace(data)) == 0 {
				return nil, candidate, errors.New("empty configuration content")
			}
			return data, candidate, nil
		}
	}

	return nil, "", errors.New("no configuration source provided: specify via --config, CONFIG_* environment variables, or create a config.yaml in the working directory")
}

func (l *Loader) readFromSource(ctx context.Context, source string) ([]byte, error) {
	if source == "-" {
		if l.stdin == nil {
			return nil, errors.New("standard input reader is nil")
		}
		data, err := io.ReadAll(l.stdin)
		if err != nil {
			return nil, fmt.Errorf("failed to read configuration from standard input: %w", err)
		}
		return data, nil
	}

	if strings.HasPrefix(source, "s3://") {
		return l.readFromS3(ctx, source)
	}

	// Local filesystem path
	cleanPath := filepath.Clean(source)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration file '%s': %w", cleanPath, err)
	}
	return data, nil
}

func (l *Loader) readFromS3(ctx context.Context, rawURI string) ([]byte, error) {
	s3URI, err := ParseS3URI(rawURI)
	if err != nil {
		return nil, err
	}

	client, err := l.getOrInitS3Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize AWS S3 client: %w", err)
	}

	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s3URI.Bucket,
		Key:    &s3URI.Key,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config from S3 URI '%s': %w", rawURI, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read S3 object payload from '%s': %w", rawURI, err)
	}
	return data, nil
}

func (l *Loader) getOrInitS3Client(ctx context.Context) (S3ClientAPI, error) {
	if l.s3Client != nil {
		return l.s3Client, nil
	}

	l.s3InitOnce.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			l.s3InitErr = fmt.Errorf("failed to load AWS configuration for S3: %w", err)
			return
		}
		l.s3Client = s3.NewFromConfig(cfg)
	})

	if l.s3InitErr != nil {
		return nil, l.s3InitErr
	}
	return l.s3Client, nil
}
