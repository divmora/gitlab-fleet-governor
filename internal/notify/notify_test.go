package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/internal/notify"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil"
)

func runResult() *engine.ExecutionResult {
	return &engine.ExecutionResult{
		DryRun:  true,
		Metrics: &engine.SummaryMetricsSnapshot{TotalScanned: 3, TotalChanged: 1, TotalFailed: 1},
		TargetResults: []*engine.TargetResult{
			{TargetPath: "team/clean", Operations: []*engine.OperationResult{{OperationName: "push_rules", Success: true}}},
			{TargetPath: "team/drift", Operations: []*engine.OperationResult{{OperationName: "protected_branches", Success: true, HasChanges: true}}},
			{Instance: "onprem", TargetPath: "team/broken", Operations: []*engine.OperationResult{{OperationName: "variables", Error: errors.New("403 Forbidden")}}},
		},
		Instances: []*engine.InstanceSummary{{Name: "saas"}, {Name: "onprem"}},
	}
}

func TestFromExecution(t *testing.T) {
	s := notify.FromExecution(runResult(), nil)
	assert.Equal(t, "run", s.Command)
	assert.Equal(t, "plan", s.Mode)
	assert.Equal(t, config.NotifyCritical, s.Severity)
	assert.Equal(t, map[string]int{"critical": 1, "warning": 1, "info": 0}, s.Counts)
	require.Len(t, s.Items, 2)
	assert.Equal(t, notify.Item{Severity: "critical", Target: "onprem:team/broken", Source: "variables", Message: "403 Forbidden"}, s.Items[0], "most severe first")
	assert.Equal(t, "drift detected (0 field(s))", s.Items[1].Message)
	assert.Equal(t, []string{"saas", "onprem"}, s.Instances)
	assert.Contains(t, s.Headline(), "governance plan found 2 issue(s) (1 critical, 1 warning)")

	clean := notify.FromExecution(&engine.ExecutionResult{Metrics: &engine.SummaryMetricsSnapshot{}}, nil)
	assert.Equal(t, config.NotifyInfo, clean.Severity)
	assert.Contains(t, clean.Headline(), "governance run found no issues")

	failed := notify.FromExecution(nil, errors.New("fleet discovery failed"))
	assert.Equal(t, config.NotifyCritical, failed.Severity)
	assert.Equal(t, "fleet discovery failed", failed.Items[0].Message)
}

func TestFromAudit(t *testing.T) {
	rep := &audit.AuditReport{
		GovernorVersion:         "0.21.0",
		Summary:                 audit.SummaryMetrics{TotalProjectsScanned: 4, TotalViolations: 3},
		GroupSettingFindings:    []audit.GroupSettingFinding{{GroupPath: "fleet", Severity: audit.SeverityHigh, Details: "Two-factor authentication is not required"}},
		DeployKeyFindings:       []audit.DeployKeyFinding{{ProjectPath: "fleet/app", KeyTitle: "ci", Severity: audit.SeverityMedium, Details: "write access"}},
		ProtectedBranchFindings: []audit.ProtectedBranchFinding{{ProjectPath: "fleet/app", BranchName: "main", Severity: audit.SeverityPass}, {ProjectPath: "fleet/lib", BranchName: "main", Severity: audit.SeverityLow, Details: "x"}},
	}
	s := notify.FromAudit(rep, "fleet-audit.xlsx")
	assert.Equal(t, config.NotifyCritical, s.Severity)
	assert.Equal(t, map[string]int{"critical": 1, "warning": 2, "info": 0}, s.Counts, "PASS findings are left out")
	assert.Equal(t, "group_settings", s.Items[0].Source)
	assert.Equal(t, "fleet-audit.xlsx", s.ReportPath)
	assert.Equal(t, "0.21.0", s.Version)
}

func TestForChannel(t *testing.T) {
	s := notify.FromExecution(runResult(), nil)
	crit := s.ForChannel("critical")
	assert.Len(t, crit.Items, 1)
	assert.Equal(t, 1, crit.TotalItems)
	assert.Len(t, s.Items, 2, "the original is not modified")
}

type capture struct {
	mu     sync.Mutex
	bodies map[string][]byte
}

func (c *capture) handler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies[r.URL.Path] = b
		c.mu.Unlock()
		w.WriteHeader(status)
	}
}

func newServer(t *testing.T, status int) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{bodies: map[string][]byte{}}
	srv := httptest.NewTLSServer(c.handler(status))
	t.Cleanup(srv.Close)
	return srv, c
}

func dispatcherFor(srv *httptest.Server, pub notify.SNSPublisher) *notify.Dispatcher {
	d := notify.NewDispatcher()
	client := srv.Client()
	client.CheckRedirect = d.HTTP.CheckRedirect
	client.Timeout = notify.Timeout
	d.HTTP = client
	d.NewSNS = func(context.Context, string) (notify.SNSPublisher, error) { return pub, nil }
	return d
}

type fakeSNS struct {
	in *sns.PublishInput
}

func (f *fakeSNS) Publish(_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	f.in = in
	return &sns.PublishOutput{}, nil
}

func TestDispatch_AllChannels(t *testing.T) {
	srv, got := newServer(t, http.StatusOK)
	pub := &fakeSNS{}
	d := dispatcherFor(srv, pub)
	var region string
	d.NewSNS = func(_ context.Context, r string) (notify.SNSPublisher, error) { region = r; return pub, nil }

	res := runResult()
	res.TargetResults[1].TargetPath = "team/<!channel>"
	s := notify.FromExecution(res, nil)

	n := &config.NotificationsConfig{
		Slack:   &config.SlackNotifyConfig{WebhookURL: srv.URL + "/slack", Channel: "#sec"},
		Teams:   &config.TeamsNotifyConfig{WebhookURL: srv.URL + "/teams"},
		Webhook: &config.WebhookNotifyConfig{URL: srv.URL + "/hook"},
		SNS:     &config.SNSNotifyConfig{TopicARN: "arn:aws:sns:eu-west-1:123456789012:alerts"},
	}
	results := d.Dispatch(context.Background(), n, s)
	require.Len(t, results, 4)
	for _, r := range results {
		assert.True(t, r.Sent, "%s: %v %s", r.Channel, r.Err, r.Skipped)
	}

	var slack map[string]any
	require.NoError(t, json.Unmarshal(got.bodies["/slack"], &slack))
	assert.Equal(t, "#sec", slack["channel"])
	assert.Contains(t, slack["text"], "1 critical, 1 warning")
	raw := string(got.bodies["/slack"])
	assert.NotContains(t, raw, "<!channel>", "GitLab text can't trigger mentions")
	assert.Contains(t, raw, "\\u0026lt;!channel\\u0026gt;")
	assert.Contains(t, raw, "#D93025")

	var teams map[string]any
	require.NoError(t, json.Unmarshal(got.bodies["/teams"], &teams))
	att := teams["attachments"].([]any)[0].(map[string]any)
	assert.Equal(t, "application/vnd.microsoft.card.adaptive", att["contentType"])
	assert.Equal(t, "AdaptiveCard", att["content"].(map[string]any)["type"])

	var hook notify.Summary
	require.NoError(t, json.Unmarshal(got.bodies["/hook"], &hook))
	assert.Equal(t, "run", hook.Command)
	assert.Len(t, hook.Items, 2)

	assert.Equal(t, "eu-west-1", region)
	require.NotNil(t, pub.in)
	assert.Equal(t, "arn:aws:sns:eu-west-1:123456789012:alerts", *pub.in.TopicArn)
	assert.LessOrEqual(t, len(*pub.in.Subject), 100)
	var msg notify.Summary
	require.NoError(t, json.Unmarshal([]byte(*pub.in.Message), &msg))
	assert.Equal(t, config.NotifyCritical, msg.Severity)
}

func TestDispatch_MinSeverityAndFailures(t *testing.T) {
	srv, got := newServer(t, http.StatusInternalServerError)
	d := dispatcherFor(srv, &fakeSNS{})
	warn := notify.FromExecution(&engine.ExecutionResult{TargetResults: []*engine.TargetResult{{TargetPath: "a", Operations: []*engine.OperationResult{{Success: true, HasChanges: true}}}}}, nil)

	results := d.Dispatch(context.Background(), &config.NotificationsConfig{
		Slack:   &config.SlackNotifyConfig{WebhookURL: srv.URL + "/secret-token-path", MinSeverity: "critical"},
		Webhook: &config.WebhookNotifyConfig{URL: srv.URL + "/secret-token-path"},
	}, warn)
	require.Len(t, results, 2)
	assert.Contains(t, results[0].Skipped, "below min_severity critical")
	assert.Error(t, results[1].Err)
	assert.Contains(t, results[1].Err.Error(), "HTTP 500")
	assert.NotContains(t, results[1].Err.Error(), "secret-token-path", "errors never contain the webhook path")
	_, slackCalled := got.bodies["/slack"]
	assert.False(t, slackCalled)
}

func TestDispatch_DoesNotFollowRedirects(t *testing.T) {
	var hits int
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	t.Cleanup(target.Close)
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)

	d := dispatcherFor(redirect, nil)
	s := notify.FromExecution(nil, errors.New("boom"))
	results := d.Dispatch(context.Background(), &config.NotificationsConfig{Webhook: &config.WebhookNotifyConfig{URL: redirect.URL}}, s)
	require.Len(t, results, 1)
	assert.ErrorContains(t, results[0].Err, "HTTP 307")
	assert.Zero(t, hits, "the payload must not follow the redirect")
}

func TestResolve(t *testing.T) {
	env := map[string]string{
		notify.EnvSlackWebhook: "https://env.example/slack",
		notify.EnvTeamsWebhook: "https://env.example/teams",
		notify.EnvMinSeverity:  "critical",
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	policy := &config.NotificationsConfig{Slack: &config.SlackNotifyConfig{WebhookURL: "https://policy.example/slack", MinSeverity: "info"}}

	n := notify.Resolve(policy, notify.Overrides{}, lookup)
	assert.Equal(t, "https://policy.example/slack", n.Slack.WebhookURL, "policy wins over env")
	assert.Equal(t, "info", n.Slack.MinSeverity, "a channel's own min_severity wins over env")
	assert.Equal(t, "https://env.example/teams", n.Teams.WebhookURL, "env fills a channel the policy doesn't set")
	assert.Equal(t, "critical", n.Teams.MinSeverity)
	assert.Equal(t, "https://policy.example/slack", policy.Slack.WebhookURL, "the policy is not modified")

	n = notify.Resolve(policy, notify.Overrides{SlackWebhook: "https://flag.example/slack", MinSeverity: "warning"}, lookup)
	assert.Equal(t, "https://flag.example/slack", n.Slack.WebhookURL, "flags win")
	assert.Equal(t, "warning", n.Slack.MinSeverity, "the flag overrides every channel")
	assert.Equal(t, "warning", n.Teams.MinSeverity)

	assert.Nil(t, notify.Resolve(nil, notify.Overrides{}, func(string) (string, bool) { return "", false }))
	assert.Equal(t, []string{"notify.slack", "notify.teams"}, notify.Features(n))
}

func TestSend_ValidationAndLicence(t *testing.T) {
	license.SetVerificationPublicKey(testutil.GetTestPublicKey())
	defer license.ResetVerificationPublicKey()
	t.Setenv("DIVMORA_LICENSE_KEY", "")

	srv, got := newServer(t, http.StatusOK)
	d := dispatcherFor(srv, nil)
	s := notify.FromExecution(nil, errors.New("boom"))
	n := &config.NotificationsConfig{Webhook: &config.WebhookNotifyConfig{URL: srv.URL + "/hook"}}

	assert.Nil(t, notify.Send(context.Background(), d, &config.NotificationsConfig{Webhook: &config.WebhookNotifyConfig{URL: "http://insecure.example"}}, s, notify.Gate{LicenseKey: testutil.CLITestToken}), "http:// is rejected")

	assert.Nil(t, notify.Send(context.Background(), d, n, s, notify.Gate{}), "without a Pro licence nothing is sent, whatever the run mode")
	assert.Empty(t, got.bodies)

	results := notify.Send(context.Background(), d, n, s, notify.Gate{LicenseKey: testutil.CLITestToken})
	require.Len(t, results, 1)
	assert.True(t, results[0].Sent)
	assert.NotEmpty(t, got.bodies["/hook"])
}
