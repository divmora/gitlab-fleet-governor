package config_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// MockS3Client implements config.S3ClientAPI.
type MockS3Client struct {
	Objects map[string][]byte
}

func (m *MockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if params.Bucket == nil || params.Key == nil {
		return nil, errors.New("bucket or key is nil")
	}
	key := *params.Bucket + "/" + *params.Key
	data, ok := m.Objects[key]
	if !ok {
		return nil, errors.New("S3 object not found (NoSuchKey)")
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

func TestParseS3URI(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		wantBucket string
		wantKey    string
		wantErr    bool
	}{
		{
			name:       "Valid S3 URI",
			uri:        "s3://my-bucket/policies/config.yaml",
			wantBucket: "my-bucket",
			wantKey:    "policies/config.yaml",
			wantErr:    false,
		},
		{
			name:       "Valid S3 URI with single key",
			uri:        "s3://bucket-123/config.json",
			wantBucket: "bucket-123",
			wantKey:    "config.json",
			wantErr:    false,
		},
		{
			name:    "Invalid scheme",
			uri:     "https://my-bucket/config.yaml",
			wantErr: true,
		},
		{
			name:    "Missing key",
			uri:     "s3://my-bucket/",
			wantErr: true,
		},
		{
			name:    "Missing bucket",
			uri:     "s3:///config.yaml",
			wantErr: true,
		},
		{
			name:    "Bare prefix",
			uri:     "s3://",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := config.ParseS3URI(tt.uri)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBucket, res.Bucket)
			assert.Equal(t, tt.wantKey, res.Key)
		})
	}
}

func TestLoader_LoadRaw_Sources(t *testing.T) {
	ctx := context.Background()

	validYAML := "version: v1\nsettings:\n  concurrency: 5\n"
	validJSON := `{"version": "v1", "settings": {"concurrency": 8}}`

	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "policy.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(validYAML), 0644))

	jsonFile := filepath.Join(tmpDir, "policy.json")
	require.NoError(t, os.WriteFile(jsonFile, []byte(validJSON), 0644))

	emptyFile := filepath.Join(tmpDir, "empty.yaml")
	require.NoError(t, os.WriteFile(emptyFile, []byte("   \n\t  "), 0644))

	mockS3 := &MockS3Client{
		Objects: map[string][]byte{
			"gov-bucket/corp.yaml": []byte(validYAML),
		},
	}

	t.Run("Load from local YAML file", func(t *testing.T) {
		loader := config.NewLoader()
		data, desc, err := loader.LoadRaw(ctx, yamlFile)
		require.NoError(t, err)
		assert.Equal(t, yamlFile, desc)
		assert.Equal(t, validYAML, string(data))
	})

	t.Run("Load from local JSON file", func(t *testing.T) {
		loader := config.NewLoader()
		data, desc, err := loader.LoadRaw(ctx, jsonFile)
		require.NoError(t, err)
		assert.Equal(t, jsonFile, desc)
		assert.Equal(t, validJSON, string(data))
	})

	t.Run("Load from stdin (-)", func(t *testing.T) {
		loader := config.NewLoader(
			config.WithStdin(strings.NewReader(validYAML)),
		)
		data, desc, err := loader.LoadRaw(ctx, "-")
		require.NoError(t, err)
		assert.Equal(t, "-", desc)
		assert.Equal(t, validYAML, string(data))
	})

	t.Run("Load from CONFIG_CONTENT env var", func(t *testing.T) {
		env := map[string]string{"CONFIG_CONTENT": validYAML}
		loader := config.NewLoader(
			config.WithEnvLookup(func(k string) (string, bool) { val, ok := env[k]; return val, ok }),
		)
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_CONTENT", desc)
		assert.Equal(t, validYAML, string(data))
	})

	t.Run("Load from CONFIG_YAML env var", func(t *testing.T) {
		env := map[string]string{"CONFIG_YAML": validYAML}
		loader := config.NewLoader(
			config.WithEnvLookup(func(k string) (string, bool) { val, ok := env[k]; return val, ok }),
		)
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_YAML", desc)
		assert.Equal(t, validYAML, string(data))
	})

	t.Run("Load from CONFIG_JSON env var", func(t *testing.T) {
		env := map[string]string{"CONFIG_JSON": validJSON}
		loader := config.NewLoader(
			config.WithEnvLookup(func(k string) (string, bool) { val, ok := env[k]; return val, ok }),
		)
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, "env:CONFIG_JSON", desc)
		assert.Equal(t, validJSON, string(data))
	})

	t.Run("Load from CONFIG_SOURCE referencing file", func(t *testing.T) {
		env := map[string]string{"CONFIG_SOURCE": yamlFile}
		loader := config.NewLoader(
			config.WithEnvLookup(func(k string) (string, bool) { val, ok := env[k]; return val, ok }),
		)
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Contains(t, desc, "env:CONFIG_SOURCE")
		assert.Equal(t, validYAML, string(data))
	})

	t.Run("Load from CONFIG_FILE referencing file", func(t *testing.T) {
		env := map[string]string{"CONFIG_FILE": jsonFile}
		loader := config.NewLoader(
			config.WithEnvLookup(func(k string) (string, bool) { val, ok := env[k]; return val, ok }),
		)
		data, desc, err := loader.LoadRaw(ctx, "")
		require.NoError(t, err)
		assert.Contains(t, desc, "env:CONFIG_FILE")
		assert.Equal(t, validJSON, string(data))
	})

	t.Run("Load from S3 URI with mock S3 client", func(t *testing.T) {
		loader := config.NewLoader(
			config.WithS3Client(mockS3),
		)
		data, desc, err := loader.LoadRaw(ctx, "s3://gov-bucket/corp.yaml")
		require.NoError(t, err)
		assert.Equal(t, "s3://gov-bucket/corp.yaml", desc)
		assert.Equal(t, validYAML, string(data))
	})

	t.Run("Error: File not found", func(t *testing.T) {
		loader := config.NewLoader()
		_, _, err := loader.LoadRaw(ctx, filepath.Join(tmpDir, "missing.yaml"))
		require.Error(t, err)
	})

	t.Run("Error: Empty file content", func(t *testing.T) {
		loader := config.NewLoader()
		_, _, err := loader.LoadRaw(ctx, emptyFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty configuration content")
	})

	t.Run("Error: S3 missing object", func(t *testing.T) {
		loader := config.NewLoader(
			config.WithS3Client(mockS3),
		)
		_, _, err := loader.LoadRaw(ctx, "s3://gov-bucket/missing.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "S3 object not found")
	})

	t.Run("Error: No config source available", func(t *testing.T) {
		loader := config.NewLoader(
			config.WithEnvLookup(func(k string) (string, bool) { return "", false }),
		)
		_, _, err := loader.LoadRaw(ctx, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no configuration source provided")
	})
}
