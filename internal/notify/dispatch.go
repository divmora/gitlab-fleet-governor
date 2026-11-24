package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// Timeout bounds each notification request.
const Timeout = 5 * time.Second

// SNSPublisher is the part of the SNS client used here.
type SNSPublisher interface {
	Publish(ctx context.Context, in *sns.PublishInput, opts ...func(*sns.Options)) (*sns.PublishOutput, error)
}

// Dispatcher sends summaries to the configured channels.
type Dispatcher struct {
	HTTP *http.Client
	// NewSNS builds the SNS client for a topic; it defaults to the AWS SDK
	// with the standard credential chain and the topic's region.
	NewSNS func(ctx context.Context, region string) (SNSPublisher, error)
}

// NewDispatcher returns a Dispatcher with a bounded HTTP client that does
// not follow redirects, so a webhook can't bounce the payload elsewhere.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		HTTP: &http.Client{
			Timeout:       Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		NewSNS: func(ctx context.Context, region string) (SNSPublisher, error) {
			cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
			if err != nil {
				return nil, err
			}
			return sns.NewFromConfig(cfg), nil
		},
	}
}

// Result records the outcome for one channel.
type Result struct {
	Channel string
	Sent    bool
	Skipped string // why nothing was sent, e.g. below min_severity
	Err     error
}

// Dispatch sends s to every configured channel whose min_severity it
// meets. Failures are returned per channel; they never panic or block for
// longer than Timeout each.
func (d *Dispatcher) Dispatch(ctx context.Context, n *config.NotificationsConfig, s *Summary) []Result {
	if n.IsEmpty() || s == nil {
		return nil
	}
	var out []Result
	try := func(channel, min string, send func(*Summary) error) {
		if min == "" {
			min = config.NotifyWarning
		}
		if config.NotifySeverityRank(s.Severity) < config.NotifySeverityRank(min) {
			out = append(out, Result{Channel: channel, Skipped: fmt.Sprintf("%s is below min_severity %s", s.Severity, min)})
			return
		}
		err := send(s.ForChannel(min))
		out = append(out, Result{Channel: channel, Sent: err == nil, Err: err})
	}
	if c := n.Slack; c != nil {
		try("slack", c.MinSeverity, func(cs *Summary) error { return d.postJSON(ctx, c.WebhookURL, slackPayload(cs, c.Channel)) })
	}
	if c := n.Teams; c != nil {
		try("teams", c.MinSeverity, func(cs *Summary) error { return d.postJSON(ctx, c.WebhookURL, teamsPayload(cs)) })
	}
	if c := n.Webhook; c != nil {
		try("webhook", c.MinSeverity, func(cs *Summary) error { return d.postJSON(ctx, c.URL, cs) })
	}
	if c := n.SNS; c != nil {
		try("sns", c.MinSeverity, func(cs *Summary) error { return d.publishSNS(ctx, c.TopicARN, cs) })
	}
	return out
}

// Log writes one log line per channel result. Webhook URLs are never
// logged.
func Log(results []Result) {
	for _, r := range results {
		switch {
		case r.Err != nil:
			slog.Warn("Notification failed", "channel", r.Channel, "error", r.Err)
		case r.Skipped != "":
			slog.Info("Notification skipped", "channel", r.Channel, "reason", r.Skipped)
		case r.Sent:
			slog.Info("Notification sent", "channel", r.Channel)
		}
	}
}

func (d *Dispatcher) postJSON(ctx context.Context, url string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(url), bytes.NewReader(raw))
	if err != nil {
		return errors.New("invalid webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gitlab-fleet-governor")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		// The URL holds the secret; report the host only.
		return fmt.Errorf("POST to %s failed: %w", req.URL.Host, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("POST to %s returned HTTP %d", req.URL.Host, resp.StatusCode)
	}
	return nil
}

// unwrapURLError drops *url.Error's message, which contains the full URL.
func unwrapURLError(err error) error {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) && strings.Contains(err.Error(), "://") {
		if inner := ue.Unwrap(); inner != nil {
			return inner
		}
	}
	return err
}

func (d *Dispatcher) publishSNS(ctx context.Context, topicARN string, s *Summary) error {
	parts := strings.Split(strings.TrimSpace(topicARN), ":")
	if len(parts) < 6 {
		return errors.New("invalid SNS topic ARN")
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	client, err := d.NewSNS(ctx, parts[3])
	if err != nil {
		return fmt.Errorf("SNS client: %w", err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = client.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(strings.TrimSpace(topicARN)),
		Subject:  aws.String(snsSubject(s.Headline())),
		Message:  aws.String(string(body)),
	})
	if err != nil {
		return fmt.Errorf("SNS publish: %w", err)
	}
	return nil
}

// snsSubject fits SNS's subject rules: at most 100 printable ASCII
// characters, no line breaks.
func snsSubject(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
	}
	return truncateText(b.String(), 100)
}

// ----------------------------------------------------------------------------
// Slack (incoming webhooks, Block Kit)
// ----------------------------------------------------------------------------

var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// slackText escapes text from GitLab so it can't form links or mentions
// such as <!channel>.
func slackText(s string) string { return slackEscaper.Replace(s) }

func colour(severity string) string {
	switch severity {
	case config.NotifyCritical:
		return "#D93025"
	case config.NotifyWarning:
		return "#F2A600"
	default:
		return "#2E7D32"
	}
}

func slackPayload(s *Summary, channel string) map[string]any {
	fields := []map[string]any{
		{"type": "mrkdwn", "text": fmt.Sprintf("*Critical*\n%d", s.Counts[config.NotifyCritical])},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Warning*\n%d", s.Counts[config.NotifyWarning])},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Scanned*\n%d", s.Scanned)},
		{"type": "mrkdwn", "text": fmt.Sprintf("*Mode*\n%s", s.Mode)},
	}
	blocks := []map[string]any{
		{"type": "header", "text": map[string]any{"type": "plain_text", "text": truncateText(s.Headline(), 150)}},
		{"type": "section", "fields": fields},
	}
	if len(s.Items) > 0 {
		var lines []string
		for _, it := range s.Items {
			lines = append(lines, fmt.Sprintf("• *%s* `%s` %s: %s", strings.ToUpper(it.Severity), slackText(it.Source), slackText(it.Target), slackText(truncateText(it.Message, 200))))
		}
		if more := s.TotalItems - len(s.Items); more > 0 {
			lines = append(lines, fmt.Sprintf("…and %d more", more))
		}
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": truncateText(strings.Join(lines, "\n"), 2900)}})
	}
	if ctx := contextLine(s); ctx != "" {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{{"type": "mrkdwn", "text": slackText(ctx)}}})
	}
	payload := map[string]any{
		"text":        s.Headline(),
		"attachments": []map[string]any{{"color": colour(s.Severity), "blocks": blocks}},
	}
	if channel != "" {
		payload["channel"] = channel
	}
	return payload
}

func contextLine(s *Summary) string {
	var parts []string
	if len(s.Instances) > 0 {
		parts = append(parts, "Instances: "+strings.Join(s.Instances, ", "))
	}
	if s.ReportPath != "" {
		parts = append(parts, "Report: "+s.ReportPath)
	}
	if s.Version != "" {
		parts = append(parts, "Version "+s.Version)
	}
	return strings.Join(parts, " | ")
}

// ----------------------------------------------------------------------------
// Microsoft Teams (Workflows webhook, Adaptive Card)
// ----------------------------------------------------------------------------

var teamsEscaper = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "*", `\*`, "_", `\_`, "`", "\\`", "<", "&lt;", ">", "&gt;")

// teamsText escapes Adaptive Card markdown so GitLab text can't add links
// or formatting.
func teamsText(s string) string { return teamsEscaper.Replace(s) }

func teamsPayload(s *Summary) map[string]any {
	style := map[string]string{config.NotifyCritical: "attention", config.NotifyWarning: "warning", config.NotifyInfo: "good"}[s.Severity]
	body := []map[string]any{
		{"type": "TextBlock", "text": teamsText(s.Headline()), "weight": "Bolder", "size": "Medium", "wrap": true, "color": style},
		{"type": "FactSet", "facts": []map[string]string{
			{"title": "Critical", "value": fmt.Sprint(s.Counts[config.NotifyCritical])},
			{"title": "Warning", "value": fmt.Sprint(s.Counts[config.NotifyWarning])},
			{"title": "Scanned", "value": fmt.Sprint(s.Scanned)},
			{"title": "Mode", "value": s.Mode},
		}},
	}
	for _, it := range s.Items {
		body = append(body, map[string]any{
			"type": "TextBlock", "wrap": true, "spacing": "Small",
			"text": fmt.Sprintf("**%s** %s · %s: %s", strings.ToUpper(it.Severity), teamsText(it.Source), teamsText(it.Target), teamsText(truncateText(it.Message, 200))),
		})
	}
	if more := s.TotalItems - len(s.Items); more > 0 {
		body = append(body, map[string]any{"type": "TextBlock", "text": fmt.Sprintf("…and %d more", more), "isSubtle": true})
	}
	if ctx := contextLine(s); ctx != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": teamsText(ctx), "isSubtle": true, "wrap": true, "size": "Small"})
	}
	return map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body":    body,
			},
		}},
	}
}
