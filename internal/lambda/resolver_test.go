package lambda_test

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

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
)

type mockS3Client struct {
	objects map[string][]byte
	err     error
}

func (m *mockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	key := ""
	if params.Key != nil {
		key = *params.Key
	}
	content, ok := m.objects[key]
	if !ok {
		return nil, errors.New("NoSuchKey: the specified key does not exist")
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(content)),
	}, nil
}

func TestConfigResolver_ResolveInlineYAML(t *testing.T) {
	resolver := lambda.NewConfigResolver(nil, nil)
	payload := &lambda.DirectInvocationPayload{
		ConfigYAML: `
version: "v1"
settings:
  concurrency: 5
  dry_run: true
targets:
  group_selector:
    group_ids_include: [10]
`,
	}

	cfg, source, err := resolver.ResolveFromDirectPayload(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if source != "inline:payload" {
		t.Errorf("expected source 'inline:payload', got %q", source)
	}
	if cfg.Settings.Concurrency != 5 {
		t.Errorf("expected concurrency 5, got %d", cfg.Settings.Concurrency)
	}
}

func TestConfigResolver_ResolveFromS3URI(t *testing.T) {
	s3Content := []byte(`
version: "v1"
settings:
  concurrency: 8
`)
	mockS3 := &mockS3Client{
		objects: map[string][]byte{
			"policies/config.yaml": s3Content,
		},
	}

	resolver := lambda.NewConfigResolver(mockS3, nil)
	payload := &lambda.DirectInvocationPayload{
		ConfigS3URI: "s3://governance-bucket/policies/config.yaml",
	}

	cfg, source, err := resolver.ResolveFromDirectPayload(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Settings.Concurrency != 8 {
		t.Errorf("expected concurrency 8, got %d", cfg.Settings.Concurrency)
	}
	if source != "s3://governance-bucket/policies/config.yaml" {
		t.Errorf("expected S3 source path, got %q", source)
	}
}

// A Lambda event must not be able to point the loader at local files or stdin,
// even when the path names a readable, valid policy file.
func TestConfigResolver_RejectsNonS3ConfigURI(t *testing.T) {
	localPolicy := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(localPolicy, []byte("version: \"v1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolver := lambda.NewConfigResolver(&mockS3Client{objects: map[string][]byte{}}, nil)
	for _, uri := range []string{
		localPolicy,
		"/etc/passwd",
		"../policy.yaml",
		"-",
		"file://" + localPolicy,
		"S3://bucket/key.yaml",
		"s3://",
		"s3://bucket-only",
		"s3://bucket/",
		"s3:///key.yaml",
	} {
		t.Run(uri, func(t *testing.T) {
			_, _, err := resolver.ResolveFromDirectPayload(context.Background(), &lambda.DirectInvocationPayload{ConfigS3URI: uri})
			if err == nil || !strings.Contains(err.Error(), "must be an s3://bucket/key URI") {
				t.Errorf("direct payload: expected s3 URI rejection, got %v", err)
			}
			_, _, err = resolver.ResolveFromS3URI(context.Background(), uri)
			if err == nil || !strings.Contains(err.Error(), "must be an s3://bucket/key URI") {
				t.Errorf("ResolveFromS3URI: expected s3 URI rejection, got %v", err)
			}
		})
	}
}

func TestConfigResolver_ParameterOverrides(t *testing.T) {
	resolver := lambda.NewConfigResolver(nil, nil)
	dryRunOverride := false
	payload := &lambda.DirectInvocationPayload{
		ConfigYAML: `
version: "v1"
settings:
  concurrency: 5
  dry_run: true
`,
		DryRun:              &dryRunOverride,
		Concurrency:         20,
		GroupIDsInclude:     []int64{101, 102},
		GroupPathsInclude:   []string{"enterprise/platform"},
		NamespacesInclude:   []string{"enterprise"},
		ProjectRegexInclude: "^prod-.*$",
	}

	cfg, _, err := resolver.ResolveFromDirectPayload(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Settings.DryRun == nil || *cfg.Settings.DryRun != false {
		t.Errorf("expected DryRun override to false")
	}
	if cfg.Settings.Concurrency != 20 {
		t.Errorf("expected Concurrency override to 20, got %d", cfg.Settings.Concurrency)
	}
	if cfg.Targets.GroupSelector == nil || len(cfg.Targets.GroupSelector.GroupIDsInclude) != 2 {
		t.Errorf("expected GroupIDsInclude override to [101, 102]")
	}
	if cfg.Targets.ProjectSelector == nil || cfg.Targets.ProjectSelector.ProjectNameRegexInclude != "^prod-.*$" {
		t.Errorf("expected ProjectNameRegexInclude override")
	}
}

// Configs supplied by an event are untrusted: they cannot redirect the GitLab
// client, read local files or expand secrets. The operator's CONFIG_* fallback
// keeps full expansion.
func TestConfigResolver_SanitizesEventConfigs(t *testing.T) {
	env := map[string]string{
		"GITLAB_TOKEN":    "glpat-operator-secret",
		"GITLAB_BASE_URL": "https://gitlab.example.com/api/v4",
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	evil := []byte("version: \"v1\"\nsettings:\n  gitlab:\n    base_url: \"https://attacker.example/api/v4\"\n    token: \"${GITLAB_TOKEN}\"\n")
	leak := []byte("version: \"v1\"\npolicies:\n  repository_files:\n    - path: LEAK.txt\n      content_file: \"/proc/self/environ\"\n")
	mockS3 := &mockS3Client{objects: map[string][]byte{"evil.yaml": evil, "leak.yaml": leak}}
	resolver := lambda.NewConfigResolver(mockS3, lookup)
	ctx := context.Background()

	cases := map[string]func() error{
		"inline content redirects GitLab": func() error {
			_, _, err := resolver.ResolveFromDirectPayload(ctx, &lambda.DirectInvocationPayload{ConfigContent: string(evil)})
			return err
		},
		"inline object reads a local file": func() error {
			cfg := &config.PolicyConfig{}
			cfg.Policies.RepositoryFiles = []config.RepositoryFileConfig{{Path: "LEAK.txt", ContentFile: "/proc/self/environ"}}
			_, _, err := resolver.ResolveFromDirectPayload(ctx, &lambda.DirectInvocationPayload{Config: cfg})
			return err
		},
		"payload config_s3_uri object": func() error {
			_, _, err := resolver.ResolveFromDirectPayload(ctx, &lambda.DirectInvocationPayload{ConfigS3URI: "s3://bucket/evil.yaml"})
			return err
		},
		"S3 event object": func() error {
			_, _, err := resolver.ResolveFromS3URI(ctx, "s3://bucket/leak.yaml")
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "untrusted configuration rejected") {
				t.Fatalf("expected untrusted config rejection, got %v", err)
			}
		})
	}

	t.Run("event config cannot expand secrets", func(t *testing.T) {
		doc := "version: \"v1\"\nsettings:\n  gitlab:\n    token: \"${GITLAB_TOKEN}\"\n"
		cfg, _, err := resolver.ResolveFromDirectPayload(ctx, &lambda.DirectInvocationPayload{ConfigContent: doc})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Settings.GitLab.Token != "" {
			t.Fatalf("secret expanded into event config: %q", cfg.Settings.GitLab.Token)
		}
	})

	t.Run("operator fallback stays trusted", func(t *testing.T) {
		env["CONFIG_CONTENT"] = "version: \"v1\"\nsettings:\n  gitlab:\n    token: \"${GITLAB_TOKEN}\"\n"
		defer delete(env, "CONFIG_CONTENT")
		cfg, _, err := resolver.ResolveFromDirectPayload(ctx, &lambda.DirectInvocationPayload{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Settings.GitLab.Token != "glpat-operator-secret" {
			t.Fatalf("operator config should expand GITLAB_TOKEN, got %q", cfg.Settings.GitLab.Token)
		}
	})
}
