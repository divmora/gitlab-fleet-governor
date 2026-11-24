package notify

import (
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// Environment variables that configure channels when neither a flag nor
// the policy does. Prefer these (or ${VAR} in the policy) to flags: flag
// values end up in shell history and process listings.
const (
	EnvSlackWebhook = "GLFG_SLACK_WEBHOOK_URL"
	EnvSlackChannel = "GLFG_SLACK_CHANNEL"
	EnvTeamsWebhook = "GLFG_TEAMS_WEBHOOK_URL"
	EnvSNSTopicARN  = "GLFG_SNS_TOPIC_ARN"
	EnvWebhookURL   = "GLFG_NOTIFY_WEBHOOK_URL"
	EnvMinSeverity  = "GLFG_NOTIFY_MIN_SEVERITY"
)

// Overrides are channel settings from CLI flags.
type Overrides struct {
	SlackWebhook string
	SlackChannel string
	TeamsWebhook string
	SNSTopicARN  string
	WebhookURL   string
	MinSeverity  string
}

// Resolve combines flags, the policy's settings.notifications and the
// environment. Flags win over the policy, and the environment fills in
// channels that neither sets. A flag or environment min_severity applies
// to the channels it configures and to policy channels without their own.
func Resolve(policy *config.NotificationsConfig, o Overrides, lookup func(string) (string, bool)) *config.NotificationsConfig {
	env := func(k string) string {
		if lookup == nil {
			return ""
		}
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	pick := func(flag, envKey string) string {
		if strings.TrimSpace(flag) != "" {
			return strings.TrimSpace(flag)
		}
		return env(envKey)
	}

	out := &config.NotificationsConfig{}
	if policy != nil {
		cp := *policy
		if cp.Slack != nil {
			s := *cp.Slack
			cp.Slack = &s
		}
		if cp.Teams != nil {
			t := *cp.Teams
			cp.Teams = &t
		}
		if cp.SNS != nil {
			s := *cp.SNS
			cp.SNS = &s
		}
		if cp.Webhook != nil {
			w := *cp.Webhook
			cp.Webhook = &w
		}
		out = &cp
	}
	min := pick(o.MinSeverity, EnvMinSeverity)

	if v := strings.TrimSpace(o.SlackWebhook); v != "" || (out.Slack == nil && env(EnvSlackWebhook) != "") {
		if out.Slack == nil {
			out.Slack = &config.SlackNotifyConfig{}
		}
		out.Slack.WebhookURL = pick(v, EnvSlackWebhook)
	}
	if out.Slack != nil {
		if ch := pick(o.SlackChannel, EnvSlackChannel); ch != "" {
			out.Slack.Channel = ch
		}
	}
	if v := strings.TrimSpace(o.TeamsWebhook); v != "" || (out.Teams == nil && env(EnvTeamsWebhook) != "") {
		if out.Teams == nil {
			out.Teams = &config.TeamsNotifyConfig{}
		}
		out.Teams.WebhookURL = pick(v, EnvTeamsWebhook)
	}
	if v := strings.TrimSpace(o.SNSTopicARN); v != "" || (out.SNS == nil && env(EnvSNSTopicARN) != "") {
		if out.SNS == nil {
			out.SNS = &config.SNSNotifyConfig{}
		}
		out.SNS.TopicARN = pick(v, EnvSNSTopicARN)
	}
	if v := strings.TrimSpace(o.WebhookURL); v != "" || (out.Webhook == nil && env(EnvWebhookURL) != "") {
		if out.Webhook == nil {
			out.Webhook = &config.WebhookNotifyConfig{}
		}
		out.Webhook.URL = pick(v, EnvWebhookURL)
	}

	if min != "" {
		if out.Slack != nil && out.Slack.MinSeverity == "" {
			out.Slack.MinSeverity = min
		}
		if out.Teams != nil && out.Teams.MinSeverity == "" {
			out.Teams.MinSeverity = min
		}
		if out.SNS != nil && out.SNS.MinSeverity == "" {
			out.SNS.MinSeverity = min
		}
		if out.Webhook != nil && out.Webhook.MinSeverity == "" {
			out.Webhook.MinSeverity = min
		}
		if strings.TrimSpace(o.MinSeverity) != "" {
			// An explicit flag overrides every channel.
			for _, p := range []*string{minPtr(out.Slack), minPtrT(out.Teams), minPtrS(out.SNS), minPtrW(out.Webhook)} {
				if p != nil {
					*p = strings.TrimSpace(o.MinSeverity)
				}
			}
		}
	}
	if out.IsEmpty() {
		return nil
	}
	return out
}

// Features returns the licence features needed to use n.
func Features(n *config.NotificationsConfig) []string {
	if n.IsEmpty() {
		return nil
	}
	var f []string
	if n.Slack != nil {
		f = append(f, "notify.slack")
	}
	if n.Teams != nil {
		f = append(f, "notify.teams")
	}
	if n.SNS != nil {
		f = append(f, "notify.sns")
	}
	if n.Webhook != nil {
		f = append(f, "notify.webhook")
	}
	return f
}

func minPtr(c *config.SlackNotifyConfig) *string {
	if c == nil {
		return nil
	}
	return &c.MinSeverity
}

func minPtrT(c *config.TeamsNotifyConfig) *string {
	if c == nil {
		return nil
	}
	return &c.MinSeverity
}

func minPtrS(c *config.SNSNotifyConfig) *string {
	if c == nil {
		return nil
	}
	return &c.MinSeverity
}

func minPtrW(c *config.WebhookNotifyConfig) *string {
	if c == nil {
		return nil
	}
	return &c.MinSeverity
}
