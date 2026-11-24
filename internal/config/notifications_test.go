package config_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

func TestNotifications_Validation(t *testing.T) {
	valid := `
version: v1
settings:
  notifications:
    slack: { webhook_url: "https://hooks.slack.com/services/T/B/X", channel: "#sec", min_severity: warning }
    teams: { webhook_url: "https://example.webhook.office.com/workflows/abc", min_severity: critical }
    sns: { topic_arn: "arn:aws:sns:us-east-1:123456789012:governance-alerts" }
    webhook: { url: "https://alerts.example.com/glfg", min_severity: info }
`
	cfg, err := config.Unmarshal([]byte(valid))
	require.NoError(t, err)
	cfg.SetDefaults()
	require.NoError(t, cfg.Validate())

	for name, tc := range map[string]struct{ yaml, field string }{
		"http slack":   {"slack: { webhook_url: \"http://hooks.example/x\" }", "settings.notifications.slack.webhook_url"},
		"empty teams":  {"teams: { webhook_url: \"\" }", "settings.notifications.teams.webhook_url"},
		"bad arn":      {"sns: { topic_arn: \"arn:aws:sqs:us-east-1:123456789012:q\" }", "settings.notifications.sns.topic_arn"},
		"bad severity": {"webhook: { url: \"https://a.example\", min_severity: loud }", "settings.notifications.webhook.min_severity"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Unmarshal([]byte("version: v1\nsettings:\n  notifications:\n    " + tc.yaml + "\n"))
			require.NoError(t, err)
			cfg.SetDefaults()
			err = cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.field)
			assert.NotContains(t, err.Error(), "hooks.example/x", "webhook URLs are secrets and are not echoed")
		})
	}
}

func TestUntrusted_RejectsNotifications(t *testing.T) {
	doc := []byte("version: v1\nsettings:\n  notifications:\n    webhook: { url: \"https://attacker.example/collect\" }\n")
	_, _, err := config.LoadInline(context.Background(), doc, "inline:test", untrustedOpts(&MockS3Client{}))
	require.ErrorContains(t, err, "settings.notifications is not allowed")
}
