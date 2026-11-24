package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/notify"
)

// newNotifyDispatcher is replaced in tests.
var newNotifyDispatcher = notify.NewDispatcher

func addNotifyFlags(cmd *cobra.Command, o *notify.Overrides) {
	f := cmd.Flags()
	f.StringVar(&o.SlackWebhook, "notify-slack-webhook", "", "Slack incoming webhook URL (prefer "+notify.EnvSlackWebhook+": flag values show up in shell history)")
	f.StringVar(&o.SlackChannel, "notify-slack-channel", "", "Slack channel override, e.g. #security-alerts")
	f.StringVar(&o.TeamsWebhook, "notify-teams-webhook", "", "Microsoft Teams Workflows webhook URL (prefer "+notify.EnvTeamsWebhook+")")
	f.StringVar(&o.SNSTopicARN, "notify-sns-topic-arn", "", "AWS SNS topic ARN to publish the JSON summary to")
	f.StringVar(&o.WebhookURL, "notify-webhook-url", "", "HTTPS endpoint to POST the JSON summary to (prefer "+notify.EnvWebhookURL+")")
	f.StringVar(&o.MinSeverity, "notify-min-severity", "", "Minimum severity that triggers a notification: info, warning (default) or critical")
}

// sendNotifications resolves the channels for cfg and sends s. It never
// fails the command.
func sendNotifications(ctx context.Context, cfg *config.PolicyConfig, o notify.Overrides, s *notify.Summary, gate notify.Gate) {
	var policy *config.NotificationsConfig
	if cfg != nil {
		policy = cfg.Settings.Notifications
	}
	n := notify.Resolve(policy, o, os.LookupEnv)
	if n.IsEmpty() {
		return
	}
	if gate.GitLabBaseURL == "" && cfg != nil {
		gate.GitLabBaseURL = cfg.Settings.GitLab.BaseURL
		if cfg.IsMultiInstance() {
			gate.GitLabBaseURL = cfg.Instances[0].BaseURL
		}
	}
	notify.Send(ctx, newNotifyDispatcher(), n, s, gate)
}

// notifyScope describes what was governed, for the headline.
func notifyScope(cfg *config.PolicyConfig) string {
	if cfg == nil {
		return ""
	}
	if gs := cfg.Targets.GroupSelector; gs != nil && len(gs.GroupPathsInclude) == 1 {
		return gs.GroupPathsInclude[0]
	}
	return ""
}
