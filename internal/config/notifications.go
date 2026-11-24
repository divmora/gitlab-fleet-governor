package config

import (
	"net/url"
	"regexp"
	"strings"
)

// Notification severities, lowest first.
const (
	NotifyInfo     = "info"
	NotifyWarning  = "warning"
	NotifyCritical = "critical"
)

// NotificationsConfig lists the channels that receive run and audit
// summaries. Webhook URLs are secrets: set them with ${VAR}.
type NotificationsConfig struct {
	Slack   *SlackNotifyConfig   `yaml:"slack,omitempty" json:"slack,omitempty"`
	Teams   *TeamsNotifyConfig   `yaml:"teams,omitempty" json:"teams,omitempty"`
	SNS     *SNSNotifyConfig     `yaml:"sns,omitempty" json:"sns,omitempty"`
	Webhook *WebhookNotifyConfig `yaml:"webhook,omitempty" json:"webhook,omitempty"`
}

// SlackNotifyConfig posts to a Slack incoming webhook.
type SlackNotifyConfig struct {
	WebhookURL  string `yaml:"webhook_url" json:"webhook_url"`
	Channel     string `yaml:"channel,omitempty" json:"channel,omitempty"`
	MinSeverity string `yaml:"min_severity,omitempty" json:"min_severity,omitempty"`
}

// TeamsNotifyConfig posts an Adaptive Card to a Microsoft Teams Workflows
// webhook.
type TeamsNotifyConfig struct {
	WebhookURL  string `yaml:"webhook_url" json:"webhook_url"`
	MinSeverity string `yaml:"min_severity,omitempty" json:"min_severity,omitempty"`
}

// SNSNotifyConfig publishes the JSON summary to an AWS SNS topic.
type SNSNotifyConfig struct {
	TopicARN    string `yaml:"topic_arn" json:"topic_arn"`
	MinSeverity string `yaml:"min_severity,omitempty" json:"min_severity,omitempty"`
}

// WebhookNotifyConfig POSTs the JSON summary to any HTTPS endpoint.
type WebhookNotifyConfig struct {
	URL         string `yaml:"url" json:"url"`
	MinSeverity string `yaml:"min_severity,omitempty" json:"min_severity,omitempty"`
}

// IsEmpty reports whether no channel is configured.
func (n *NotificationsConfig) IsEmpty() bool {
	return n == nil || (n.Slack == nil && n.Teams == nil && n.SNS == nil && n.Webhook == nil)
}

// NotifySeverityRank orders severities; unknown values rank as warning,
// the default minimum.
func NotifySeverityRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case NotifyInfo:
		return 0
	case NotifyCritical:
		return 2
	default:
		return 1
	}
}

var snsTopicARNRegex = regexp.MustCompile(`^arn:aws[a-z-]*:sns:[a-z0-9-]+:\d{12}:[A-Za-z0-9_.-]{1,256}$`)

func validateNotifications(n *NotificationsConfig, prefix string, errs *ValidationErrors) {
	if n == nil {
		return
	}
	add := func(field, msg string, value any) {
		*errs = append(*errs, ValidationError{Field: prefix + "." + field, Message: msg, Value: value})
	}
	severity := func(field, v string) {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", NotifyInfo, NotifyWarning, NotifyCritical:
		default:
			add(field, "must be info, warning or critical", v)
		}
	}
	httpsURL := func(field, v string) {
		u, err := url.Parse(strings.TrimSpace(v))
		switch {
		case strings.TrimSpace(v) == "":
			add(field, "is required (use ${VAR} to keep it out of the policy file)", nil)
		case err != nil || u.Scheme != "https" || u.Host == "":
			// The URL is a secret: don't echo it back.
			add(field, "must be an https:// URL", nil)
		}
	}
	if n.Slack != nil {
		httpsURL("slack.webhook_url", n.Slack.WebhookURL)
		severity("slack.min_severity", n.Slack.MinSeverity)
	}
	if n.Teams != nil {
		httpsURL("teams.webhook_url", n.Teams.WebhookURL)
		severity("teams.min_severity", n.Teams.MinSeverity)
	}
	if n.Webhook != nil {
		httpsURL("webhook.url", n.Webhook.URL)
		severity("webhook.min_severity", n.Webhook.MinSeverity)
	}
	if n.SNS != nil {
		if !snsTopicARNRegex.MatchString(strings.TrimSpace(n.SNS.TopicARN)) {
			add("sns.topic_arn", "must be an SNS topic ARN such as arn:aws:sns:us-east-1:123456789012:alerts", n.SNS.TopicARN)
		}
		severity("sns.min_severity", n.SNS.MinSeverity)
	}
}

// ValidateNotificationsConfig checks a notifications block built outside
// the policy file (for example from CLI flags and environment variables).
func ValidateNotificationsConfig(n *NotificationsConfig) error {
	var errs ValidationErrors
	validateNotifications(n, "notifications", &errs)
	if len(errs) > 0 {
		return errs
	}
	return nil
}
