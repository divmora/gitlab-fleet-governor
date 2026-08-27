package lambda_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock S3 Client for testing S3 Put and S3 URI loading
type mockS3ClientChallenge struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMockS3ClientChallenge() *mockS3ClientChallenge {
	return &mockS3ClientChallenge{
		objects: make(map[string][]byte),
	}
}

func (m *mockS3ClientChallenge) putObject(bucket, key string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[bucket+"/"+key] = data
}

func (m *mockS3ClientChallenge) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := ""
	if params.Key != nil {
		key = *params.Key
	}
	bucket := ""
	if params.Bucket != nil {
		bucket = *params.Bucket
	}
	data, ok := m.objects[bucket+"/"+key]
	if !ok {
		data, ok = m.objects[key]
	}
	if !ok {
		return nil, errors.New("NoSuchKey: The specified key does not exist")
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

// TestChallenge_Lambda_Detector_AllTriggerVariants tests edge-case event payload detection.
func TestChallenge_Lambda_Detector_AllTriggerVariants(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		expected lambda.EventType
	}{
		{
			name:     "Empty payload",
			payload:  "",
			expected: lambda.EventTypeDirectInvocation,
		},
		{
			name:     "Empty JSON object",
			payload:  "{}",
			expected: lambda.EventTypeDirectInvocation,
		},
		{
			name:     "Null JSON",
			payload:  "null",
			expected: lambda.EventTypeDirectInvocation,
		},
		{
			name: "Standard S3 Put Event",
			payload: `{
				"Records": [{
					"eventSource": "aws:s3",
					"eventName": "ObjectCreated:Put",
					"s3": {
						"bucket": {"name": "governor-policies"},
						"object": {"key": "policies/my-policy.yaml"}
					}
				}]
			}`,
			expected: lambda.EventTypeS3Put,
		},
		{
			name: "S3 Event with URL escaped key and plus sign",
			payload: `{
				"Records": [{
					"eventSource": "aws:s3",
					"eventName": "ObjectCreated:Put",
					"s3": {
						"bucket": {"name": "governor-policies"},
						"object": {"key": "policies/my+policy%20v2.yaml"}
					}
				}]
			}`,
			expected: lambda.EventTypeS3Put,
		},
		{
			name: "EventBridge Scheduled Event",
			payload: `{
				"version": "0",
				"id": "fe10b144-ec85-4299-bb6a-1f81d11b333a",
				"detail-type": "Scheduled Event",
				"source": "aws.events",
				"account": "123456789012",
				"time": "2026-08-26T00:00:00Z",
				"region": "us-east-1",
				"resources": ["arn:aws:events:us-east-1:123456789012:rule/hourly-governance-audit"],
				"detail": {}
			}`,
			expected: lambda.EventTypeEventBridgeSchedule,
		},
		{
			name: "API Gateway Proxy Event",
			payload: `{
				"httpMethod": "POST",
				"path": "/governor/run",
				"headers": {"Content-Type": "application/json"},
				"body": "{\"dry_run\": true}"
			}`,
			expected: lambda.EventTypeAPIGateway,
		},
		{
			name: "Direct JSON with config S3 URI",
			payload: `{
				"config_s3_uri": "s3://governor-configs/prod-policy.yaml",
				"dry_run": false,
				"concurrency": 20
			}`,
			expected: lambda.EventTypeDirectInvocation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detected, err := lambda.DetectEventType([]byte(tc.payload))
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, detected)
		})
	}
}

// TestChallenge_Lambda_S3KeyUrlUnescaping tests S3 key decoding including '+', '%20', and '%2B'.
func TestChallenge_Lambda_S3KeyUrlUnescaping(t *testing.T) {
	mockS3 := newMockS3ClientChallenge()
	// Store object with space in name
	validPolicy := `
version: "v1"
settings:
  dry_run: true
`
	mockS3.putObject("my-bucket", "folder name/my policy.yaml", []byte(validPolicy))

	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "plan",
			DryRun:  true,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:  10,
				TotalTargeted: 2,
				TotalChanged:  0,
			},
		},
	}

	handler := lambda.NewHandler(
		lambda.WithS3Client(mockS3),
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	// S3 event where key is URL encoded with '+' and '%20'
	rawKey := url.QueryEscape("folder name/my policy.yaml") // "folder+name%2Fmy+policy.yaml"
	s3Payload := `{
		"Records": [{
			"eventSource": "aws:s3",
			"eventName": "ObjectCreated:Put",
			"s3": {
				"bucket": {"name": "my-bucket"},
				"object": {"key": "` + rawKey + `"}
			}
		}]
	}`

	respAny, err := handler.HandleRequest(context.Background(), []byte(s3Payload))
	require.NoError(t, err)

	resp, ok := respAny.(*lambda.LambdaResponse)
	if !ok {
		t.Fatalf("expected *LambdaResponse, got %T", respAny)
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "SUCCESS", resp.Status)
	assert.Equal(t, lambda.EventTypeS3Put, resp.EventType)
	assert.Equal(t, 0, len(resp.Errors))
}

// TestChallenge_Lambda_APIGatewayBase64Payload tests base64 payload decoding in API Gateway events.
func TestChallenge_Lambda_APIGatewayBase64Payload(t *testing.T) {
	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "plan",
			DryRun:  true,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:  5,
				TotalTargeted: 1,
			},
		},
	}

	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	bodyPayload := `{"config_yaml": "version: 'v1'\nsettings:\n  dry_run: true", "concurrency": 5}`
	encodedBody := base64.StdEncoding.EncodeToString([]byte(bodyPayload))

	apigwReq := `{
		"httpMethod": "POST",
		"path": "/governance/execute",
		"isBase64Encoded": true,
		"headers": {"Content-Type": "application/json"},
		"body": "` + encodedBody + `"
	}`

	respAny, err := handler.HandleRequest(context.Background(), []byte(apigwReq))
	require.NoError(t, err)

	apigwResp, ok := respAny.(*lambda.APIGatewayProxyResponse)
	require.True(t, ok, "API Gateway trigger must return *APIGatewayProxyResponse, got %T", respAny)

	assert.Equal(t, http.StatusOK, apigwResp.StatusCode)
	assert.Equal(t, "application/json", apigwResp.Headers["Content-Type"])

	var parsedBody lambda.LambdaResponse
	err = json.Unmarshal([]byte(apigwResp.Body), &parsedBody)
	require.NoError(t, err, "Response body must be valid JSON")
	assert.Equal(t, "SUCCESS", parsedBody.Status)
	assert.Equal(t, lambda.EventTypeAPIGateway, parsedBody.EventType)
}

// TestChallenge_Lambda_DirectPayloadOverrides verifies all runtime parameter overrides.
func TestChallenge_Lambda_DirectPayloadOverrides(t *testing.T) {
	var capturedCfg *config.PolicyConfig
	var mu sync.Mutex

	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "apply",
			DryRun:  false,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:  1,
				TotalTargeted: 1,
			},
		},
	}

	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			mu.Lock()
			capturedCfg = cfg
			mu.Unlock()
			return mockExec
		}),
	)

	payload := `{
		"config_yaml": "version: 'v1'\nsettings:\n  dry_run: true\n  concurrency: 2",
		"dry_run": false,
		"concurrency": 25,
		"log_level": "debug",
		"log_format": "json",
		"group_ids_include": [10, 20],
		"group_ids_exclude": [30],
		"group_paths_include": ["sec/*"],
		"group_paths_exclude": ["legacy/*"],
		"namespaces_include": ["gitlab-org/*"],
		"namespaces_exclude": ["test/*"],
		"project_regex_include": "^app-.*",
		"project_regex_exclude": ".*-deprecated$"
	}`

	respAny, err := handler.HandleRequest(context.Background(), []byte(payload))
	require.NoError(t, err)

	resp, ok := respAny.(*lambda.LambdaResponse)
	require.True(t, ok)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, capturedCfg)

	// Verify all overrides were applied
	assert.False(t, *capturedCfg.Settings.DryRun, "dry_run should be overridden to false")
	assert.Equal(t, 25, capturedCfg.Settings.Concurrency, "concurrency should be overridden to 25")
	assert.Equal(t, "debug", capturedCfg.Settings.LogLevel)
	assert.Equal(t, "json", capturedCfg.Settings.LogFormat)

	require.NotNil(t, capturedCfg.Targets.GroupSelector)
	assert.Equal(t, []int{10, 20}, capturedCfg.Targets.GroupSelector.GroupIDsInclude)
	assert.Equal(t, []int{30}, capturedCfg.Targets.GroupSelector.GroupIDsExclude)
	assert.Equal(t, []string{"sec/*"}, capturedCfg.Targets.GroupSelector.GroupPathsInclude)
	assert.Equal(t, []string{"legacy/*"}, capturedCfg.Targets.GroupSelector.GroupPathsExclude)

	require.NotNil(t, capturedCfg.Targets.ProjectSelector)
	assert.Equal(t, []string{"gitlab-org/*"}, capturedCfg.Targets.ProjectSelector.NamespacesInclude)
	assert.Equal(t, []string{"test/*"}, capturedCfg.Targets.ProjectSelector.NamespacesExclude)
	assert.Equal(t, "^app-.*", capturedCfg.Targets.ProjectSelector.ProjectNameRegexInclude)
	assert.Equal(t, ".*-deprecated$", capturedCfg.Targets.ProjectSelector.ProjectNameRegexExclude)
}

// TestChallenge_Lambda_ConcurrentInvocations tests thread-safety of Handler under parallel execution.
func TestChallenge_Lambda_ConcurrentInvocations(t *testing.T) {
	mockExec := &mockEngineExecutor{
		result: &engine.ExecutionResult{
			Mode:    "plan",
			DryRun:  true,
			Success: true,
			Metrics: &engine.SummaryMetricsSnapshot{
				TotalScanned:  10,
				TotalTargeted: 5,
			},
		},
	}

	handler := lambda.NewHandler(
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(client gitlab.GitLabClient, cfg *config.PolicyConfig) lambda.EngineExecutor {
			return mockExec
		}),
	)

	const numParallel = 30
	var wg sync.WaitGroup
	wg.Add(numParallel)

	for i := 0; i < numParallel; i++ {
		go func(id int) {
			defer wg.Done()
			payload := `{
				"config_yaml": "version: 'v1'\nsettings:\n  dry_run: true",
				"concurrency": 5
			}`
			respAny, err := handler.HandleRequest(context.Background(), []byte(payload))
			assert.NoError(t, err)
			assert.NotNil(t, respAny)
		}(i)
	}

	wg.Wait()
}

// TestChallenge_Lambda_PanicRecoveryAcrossTriggers tests runtime panic recovery across all event trigger pathways.
func TestChallenge_Lambda_PanicRecoveryAcrossTriggers(t *testing.T) {
	mockS3 := newMockS3ClientChallenge()
	mockS3.putObject("test-bucket", "policy.yaml", []byte("version: 'v1'"))

	panicHandler := lambda.NewHandler(
		lambda.WithS3Client(mockS3),
		lambda.WithClientFactory(func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			panic("critical unhandled panic during client initialization")
		}),
	)

	triggers := []struct {
		name        string
		payload     string
		isAPIGateway bool
	}{
		{
			name:        "Direct Invocation Panic",
			payload:     `{"config_yaml": "version: 'v1'"}`,
			isAPIGateway: false,
		},
		{
			name: "EventBridge Scheduled Event Panic",
			payload: `{
				"version": "0",
				"id": "test-123",
				"detail-type": "Scheduled Event",
				"source": "aws.events",
				"time": "2026-08-26T00:00:00Z",
				"resources": ["arn:aws:events:us-east-1:123456789012:rule/hourly"],
				"detail": {"config_yaml": "version: 'v1'"}
			}`,
			isAPIGateway: false,
		},
		{
			name: "S3 Put Event Panic",
			payload: `{
				"Records": [{
					"eventSource": "aws:s3",
					"eventName": "ObjectCreated:Put",
					"s3": {
						"bucket": {"name": "test-bucket"},
						"object": {"key": "policy.yaml"}
					}
				}]
			}`,
			isAPIGateway: false,
		},
		{
			name: "API Gateway Proxy Event Panic",
			payload: `{
				"httpMethod": "POST",
				"path": "/run",
				"body": "{\"config_yaml\": \"version: 'v1'\"}"
			}`,
			isAPIGateway: true,
		},
	}

	for _, tt := range triggers {
		t.Run(tt.name, func(t *testing.T) {
			respAny, err := panicHandler.HandleRequest(context.Background(), []byte(tt.payload))
			require.NoError(t, err, "HandleRequest must NOT return Go error on recovered panic")

			if tt.isAPIGateway {
				// API Gateway returns an APIGatewayProxyResponse with 500
				apigwResp, ok := respAny.(*lambda.APIGatewayProxyResponse)
				if !ok {
					// Or a LambdaResponse if panic unwound before APIGateway response wrapper
					lResp, okLambda := respAny.(*lambda.LambdaResponse)
					require.True(t, okLambda, "Must return either APIGatewayProxyResponse or LambdaResponse on panic")
					assert.Equal(t, http.StatusInternalServerError, lResp.StatusCode)
					assert.Equal(t, "FAILED", lResp.Status)
					require.NotEmpty(t, lResp.Errors)
					assert.Contains(t, lResp.Errors[0], "panic in lambda handler")
					assert.Contains(t, lResp.Errors[0], "stack:")
					return
				}
				assert.Equal(t, http.StatusInternalServerError, apigwResp.StatusCode)
			} else {
				resp, ok := respAny.(*lambda.LambdaResponse)
				require.True(t, ok, "Expected *LambdaResponse on panic recovery")
				assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
				assert.Equal(t, "FAILED", resp.Status)
				require.NotEmpty(t, resp.Errors)
				assert.Contains(t, resp.Errors[0], "panic in lambda handler")
				assert.Contains(t, resp.Errors[0], "stack:")
			}
		})
	}
}

