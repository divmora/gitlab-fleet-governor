package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/notify"
)

func notifyEndpoint(t *testing.T) (*httptest.Server, func() []notify.Summary) {
	t.Helper()
	var mu sync.Mutex
	var got []notify.Summary
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var s notify.Summary
		_ = json.Unmarshal(b, &s)
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	orig := newNotifyDispatcher
	newNotifyDispatcher = func() *notify.Dispatcher {
		d := orig()
		c := srv.Client()
		c.CheckRedirect = d.HTTP.CheckRedirect
		d.HTTP = c
		return d
	}
	t.Cleanup(func() { newNotifyDispatcher = orig })
	return srv, func() []notify.Summary { mu.Lock(); defer mu.Unlock(); return append([]notify.Summary(nil), got...) }
}

func TestRunAndAudit_SendNotifications(t *testing.T) {
	srv, received := notifyEndpoint(t)
	gl := compliantAuditServer(t)
	policy := writePolicy(t, fmt.Sprintf(`
settings:
  dry_run: true
  gitlab: { base_url: "%s", token: mock-token }
targets:
  project_selector: { namespaces_include: ["compliance"] }
policies:
  variables:
    - { key: NEW_VAR, value: "1" }
`, gl.BaseURL()))

	ctx := context.Background()
	_, _, err := executeCommand(ctx, "run", "-c", policy, "--notify-webhook-url", srv.URL+"/hook", "--notify-min-severity", "info")
	require.NoError(t, err)
	got := received()
	require.Len(t, got, 1)
	assert.Equal(t, "run", got[0].Command)
	assert.Equal(t, "plan", got[0].Mode)
	assert.Equal(t, "warning", got[0].Severity, "the new variable is drift")

	t.Setenv(notify.EnvWebhookURL, srv.URL+"/from-env")
	_, _, err = executeCommand(ctx, "audit", "-c", policy, "--format=json", "-o", t.TempDir()+"/a.json", "--notify-min-severity", "info")
	require.NoError(t, err)
	got = received()
	require.Len(t, got, 2)
	assert.Equal(t, "audit", got[1].Command)

	// A critical threshold suppresses a run that only has drift.
	_, _, err = executeCommand(ctx, "run", "-c", policy, "--notify-min-severity", "critical")
	require.NoError(t, err)
	assert.Len(t, received(), 2)
}
