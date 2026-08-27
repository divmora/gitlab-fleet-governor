package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/cli"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// MockS3Client implements config.S3ClientAPI for opaque-box S3 URI testing.
type MockS3Client struct {
	mu      sync.RWMutex
	Objects map[string][]byte
}

// NewMockS3Client initializes a thread-safe MockS3Client.
func NewMockS3Client() *MockS3Client {
	return &MockS3Client{
		Objects: make(map[string][]byte),
	}
}

// Put adds an object to the in-memory S3 store.
func (m *MockS3Client) Put(bucket, key string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Objects[bucket+"/"+key] = data
}

// GetObject retrieves an object from the in-memory S3 store.
func (m *MockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if params.Bucket == nil || params.Key == nil {
		return nil, errors.New("bucket or key cannot be nil")
	}
	key := *params.Bucket + "/" + *params.Key
	data, ok := m.Objects[key]
	if !ok {
		return nil, fmt.Errorf("S3 object not found: %s", key)
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

// E2EHarness bundles the mock server, temporary directory, mock S3, and CLI execution wrappers.
type E2EHarness struct {
	Server   *mockserver.MockGitLabServer
	TempDir  string
	S3Client *MockS3Client
	t        *testing.T
}

// NewE2EHarness creates an isolated test environment with a seeded mock GitLab server.
func NewE2EHarness(t *testing.T) *E2EHarness {
	t.Helper()

	server := mockserver.NewMockGitLabServer()
	server.Seed()

	tempDir := t.TempDir()
	s3Client := NewMockS3Client()

	h := &E2EHarness{
		Server:   server,
		TempDir:  tempDir,
		S3Client: s3Client,
		t:        t,
	}

	t.Cleanup(func() {
		server.Close()
	})

	return h
}

// WriteConfigFile writes content to a file inside the harness temp directory and returns its absolute path.
func (h *E2EHarness) WriteConfigFile(filename, content string) string {
	h.t.Helper()
	filePath := filepath.Join(h.TempDir, filename)
	err := os.WriteFile(filePath, []byte(content), 0600)
	require.NoError(h.t, err, "failed to write config file %s", filename)
	return filePath
}

// WriteConfigFileJSON writes an object marshaled as JSON to the harness temp directory.
func (h *E2EHarness) WriteConfigFileJSON(filename string, obj any) string {
	h.t.Helper()
	data, err := json.MarshalIndent(obj, "", "  ")
	require.NoError(h.t, err, "failed to marshal JSON object for %s", filename)
	return h.WriteConfigFile(filename, string(data))
}

// ExecuteCLI executes a Cobra command with captured stdout and stderr.
func (h *E2EHarness) ExecuteCLI(ctx context.Context, args ...string) (string, string, error) {
	return h.ExecuteCLIWithStdin(ctx, nil, args...)
}

// ExecuteCLIWithStdin executes a Cobra command with an explicit stdin reader.
func (h *E2EHarness) ExecuteCLIWithStdin(ctx context.Context, stdin io.Reader, args ...string) (string, string, error) {
	h.t.Helper()

	cmd := cli.NewRootCmd()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if stdin != nil {
		cmd.SetIn(stdin)
	}
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

// BasePolicyYAML generates a valid baseline YAML configuration pointing to the mock server.
func (h *E2EHarness) BasePolicyYAML(customPolicy string) string {
	return fmt.Sprintf(`
version: "v1"
settings:
  dry_run: true
  concurrency: 4
  log_level: "info"
  log_format: "text"
  report_format: "table"
  gitlab:
    base_url: "%s"
    token: "mock-token"
    rate_limit_rps: 100.0
    rate_limit_burst: 100
    max_retries: 3
    retry_base_delay_ms: 50
targets:
  group_selector:
    group_paths_include:
      - "platform"
    recursive: true
  project_selector:
    archived: false
policies:
%s
`, h.Server.BaseURL(), customPolicy)
}

// GovernorClient creates an initialized client pointing to the mock server.
func (h *E2EHarness) GovernorClient(opts ...gl.ClientOption) (*gl.Client, error) {
	return h.Server.GovernorClient(opts...)
}

// NewEngine creates an initialized GovernanceEngine pointing to the mock server.
func (h *E2EHarness) NewEngine(cfg *config.PolicyConfig, opts ...engine.EngineOption) (*engine.GovernanceEngine, error) {
	client, err := h.GovernorClient()
	if err != nil {
		return nil, err
	}
	return engine.NewGovernanceEngine(client, cfg, opts...)
}

// NewLambdaHandler instantiates a Lambda handler wired with mock S3 and client factories.
func (h *E2EHarness) NewLambdaHandler(opts ...lambda.HandlerOption) *lambda.Handler {
	allOpts := []lambda.HandlerOption{
		lambda.WithS3Client(h.S3Client),
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gl.GitLabClient, error) {
			if cfg.BaseURL == "" {
				cfg.BaseURL = h.Server.BaseURL()
			}
			if cfg.Token == "" {
				cfg.Token = "mock-token"
			}
			lookupFn := config.EnvLookupFunc(nil)
			if len(lookup) > 0 {
				lookupFn = lookup[0]
			}
			return gl.NewClientFromConfig(cfg, lookupFn)
		}),
	}
	allOpts = append(allOpts, opts...)
	return lambda.NewHandler(allOpts...)
}

// Helper: Find Project by ID in Mock State
func (h *E2EHarness) GetProject(id int) (*gogitlab.Project, bool) {
	return h.Server.State().GetProject(fmt.Sprintf("%d", id))
}

// Helper: Find Group by ID in Mock State
func (h *E2EHarness) GetGroup(id int) (*gogitlab.Group, bool) {
	return h.Server.State().GetGroup(fmt.Sprintf("%d", id))
}

// Helper: Get Pipeline Retention in seconds for a project
func (h *E2EHarness) GetPipelineRetention(id int) (int, bool) {
	return h.Server.State().GetProjectPipelineRetention(id)
}

func TestE2EHarness_Bootstrap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewE2EHarness(t)
	require.NotEmpty(t, h.Server.BaseURL())

	// Verify CLI Help executes cleanly
	stdout, stderr, err := h.ExecuteCLI(ctx, "--help")
	require.NoError(t, err, "stderr: %s", stderr)
	require.Contains(t, stdout, "gitlab-fleet-governor")
	require.Contains(t, stdout, "run")
	require.Contains(t, stdout, "validate")
	require.Contains(t, stdout, "version")
	require.Contains(t, stdout, "lambda")
}
