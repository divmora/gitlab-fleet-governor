package lambda_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/internal/notify"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil"
)

func TestHandler_NotificationsComeFromEnvironmentOnly(t *testing.T) {
	license.SetVerificationPublicKey(testutil.GetTestPublicKey())
	defer license.ResetVerificationPublicKey()
	t.Setenv("DIVMORA_LICENSE_KEY", testutil.CLITestToken)
	var mu sync.Mutex
	paths := []string{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
	}))
	defer srv.Close()
	d := notify.NewDispatcher()
	c := srv.Client()
	c.CheckRedirect = d.HTTP.CheckRedirect
	d.HTTP = c

	env := map[string]string{
		notify.EnvWebhookURL:  srv.URL + "/operator",
		notify.EnvMinSeverity: "info",
	}
	handler := lambda.NewHandler(
		lambda.WithEnvLookup(func(k string) (string, bool) { v, ok := env[k]; return v, ok }),
		lambda.WithNotifyDispatcher(d),
		lambda.WithClientFactory(func(*config.GitLabSettingsConfig, ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(gitlab.GitLabClient, *config.PolicyConfig) lambda.EngineExecutor {
			return &mockEngineExecutor{result: &engine.ExecutionResult{Success: true, DryRun: true, Metrics: &engine.SummaryMetricsSnapshot{}}}
		}),
	)

	// The operator's environment target receives the summary.
	resp, err := handler.HandleRequest(context.Background(), []byte(`{"config_yaml": "version: v1\n", "dry_run": true}`))
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", resp.(*lambda.LambdaResponse).Status)
	mu.Lock()
	assert.Equal(t, []string{"/operator"}, paths)
	mu.Unlock()

	// An event that tries to set a target is rejected and nothing is sent to it.
	resp, err = handler.HandleRequest(context.Background(), []byte(`{"config": {"version": "v1", "settings": {"notifications": {"webhook": {"url": "`+srv.URL+`/attacker"}}}}}`))
	require.NoError(t, err)
	r := resp.(*lambda.LambdaResponse)
	assert.Equal(t, "FAILED", r.Status)
	assert.True(t, strings.Contains(strings.Join(r.Errors, " "), "settings.notifications is not allowed"))
	mu.Lock()
	assert.NotContains(t, paths, "/attacker")
	mu.Unlock()
}

func TestHandler_NotificationLicenceFromEnvironmentFile(t *testing.T) {
	license.SetVerificationPublicKey(testutil.GetTestPublicKey())
	defer license.ResetVerificationPublicKey()
	licFile := filepath.Join(t.TempDir(), "license.key")
	require.NoError(t, os.WriteFile(licFile, []byte(testutil.CLITestToken), 0o600))
	t.Setenv("DIVMORA_LICENSE_KEY", "")
	t.Setenv("DIVMORA_LICENSE_FILE", licFile)

	var mu sync.Mutex
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer srv.Close()
	d := notify.NewDispatcher()
	c := srv.Client()
	c.CheckRedirect = d.HTTP.CheckRedirect
	d.HTTP = c

	env := map[string]string{notify.EnvWebhookURL: srv.URL + "/operator", notify.EnvMinSeverity: "info"}
	handler := lambda.NewHandler(
		lambda.WithEnvLookup(func(k string) (string, bool) { v, ok := env[k]; return v, ok }),
		lambda.WithNotifyDispatcher(d),
		lambda.WithClientFactory(func(*config.GitLabSettingsConfig, ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			return &mockGitLabClientStub{}, nil
		}),
		lambda.WithEngineFactory(func(gitlab.GitLabClient, *config.PolicyConfig) lambda.EngineExecutor {
			return &mockEngineExecutor{result: &engine.ExecutionResult{Success: true, Metrics: &engine.SummaryMetricsSnapshot{}}}
		}),
	)

	// Apply mode: notifications need the Pro licence, found via DIVMORA_LICENSE_FILE.
	_, err := handler.HandleRequest(context.Background(), []byte(`{"config_yaml": "version: v1\n", "dry_run": false}`))
	require.NoError(t, err)
	mu.Lock()
	assert.Equal(t, 1, hits)
	mu.Unlock()
}
