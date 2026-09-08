package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
)

// Config encapsulates SMTP server connection and authentication parameters.
type Config struct {
	Host          string   `yaml:"host" json:"host"`
	Port          int      `yaml:"port" json:"port"`
	Username      string   `yaml:"username" json:"username"`
	Password      string   `yaml:"password" json:"password"`
	From          string   `yaml:"from" json:"from"`
	To            []string `yaml:"to" json:"to"`
	Subject       string   `yaml:"subject" json:"subject"`
	StartTLS      bool     `yaml:"starttls" json:"starttls"`
	DirectTLS     bool     `yaml:"direct_tls" json:"direct_tls"`
	SkipTLSVerify bool     `yaml:"skip_tls_verify" json:"skip_tls_verify"`
	SendAttach    bool     `yaml:"send_attachment" json:"send_attachment"`
}

// SetDefaults assigns sensible default parameters.
func (c *Config) SetDefaults() {
	if c.Port <= 0 {
		c.Port = 587
	}
	if c.Subject == "" {
		c.Subject = "GitLab Fleet Compliance & Security Audit Report"
	}
	if c.Port == 465 {
		c.DirectTLS = true
	} else {
		c.StartTLS = true
	}
	c.SendAttach = true
}

// Attachment encapsulates a binary or text file to attach to an email.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message represents an email message to be transmitted.
type Message struct {
	From        string
	To          []string
	Subject     string
	TextBody    string
	HTMLBody    string
	Attachments []Attachment
}

// Dispatcher manages SMTP delivery.
type Dispatcher struct {
	config Config
}

// NewDispatcher creates an initialized Dispatcher instance.
func NewDispatcher(cfg Config) *Dispatcher {
	cfg.SetDefaults()
	return &Dispatcher{config: cfg}
}

// Send dispatches an email message using the configured SMTP server.
func (d *Dispatcher) Send(ctx context.Context, msg *Message) error {
	if msg == nil {
		return errors.New("cannot dispatch nil email message")
	}

	from := msg.From
	if from == "" {
		from = d.config.From
	}
	if from == "" {
		return errors.New("smtp sender 'From' address is required")
	}

	to := msg.To
	if len(to) == 0 {
		to = d.config.To
	}
	if len(to) == 0 {
		return errors.New("smtp recipient 'To' address is required")
	}

	subject := msg.Subject
	if subject == "" {
		subject = d.config.Subject
	}

	// 1. Build MIME multipart payload
	payload, err := d.buildMIMEMessage(from, to, subject, msg)
	if err != nil {
		return fmt.Errorf("failed to build MIME email payload: %w", err)
	}

	addr := net.JoinHostPort(d.config.Host, strconv.Itoa(d.config.Port))

	// Check context cancellation before connecting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 2. Connect and authenticate
	tlsConfig := &tls.Config{
		ServerName:         d.config.Host,
		InsecureSkipVerify: d.config.SkipTLSVerify, //nolint:gosec // user configurable
	}

	var client *smtp.Client

	if d.config.DirectTLS || d.config.Port == 465 {
		// Implicit TLS (Port 465)
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to connect via direct TLS to %s: %w", addr, err)
		}
		c, err := smtp.NewClient(conn, d.config.Host)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("failed to create SMTP client over TLS: %w", err)
		}
		client = c
	} else {
		// Plain or STARTTLS (Port 587 or 25)
		conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
		if err != nil {
			return fmt.Errorf("failed to dial SMTP server %s: %w", addr, err)
		}
		c, err := smtp.NewClient(conn, d.config.Host)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("failed to create SMTP client: %w", err)
		}
		client = c

		// Negotiate STARTTLS if supported/requested
		if d.config.StartTLS {
			if ok, _ := client.Extension("STARTTLS"); ok {
				if err := client.StartTLS(tlsConfig); err != nil {
					_ = client.Close()
					return fmt.Errorf("STARTTLS handshake failed with %s: %w", addr, err)
				}
			}
		}
	}

	defer func() {
		_ = client.Quit()
		_ = client.Close()
	}()

	// 3. Authenticate if credentials provided
	if d.config.Username != "" && d.config.Password != "" {
		auth := d.chooseAuth(d.config.Host)
		if err := client.Auth(auth); err != nil {
			// Fallback to LOGIN auth for Microsoft 365 / older relays if PLAIN fails
			loginAuth := newLoginAuth(d.config.Username, d.config.Password)
			if loginErr := client.Auth(loginAuth); loginErr != nil {
				return fmt.Errorf("SMTP authentication failed (auth: %w, login fallback: %v)", err, loginErr)
			}
		}
	}

	// 4. Send MAIL FROM
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM failed for %s: %w", from, err)
	}

	// 5. Send RCPT TO for each recipient
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("SMTP RCPT TO failed for %s: %w", recipient, err)
		}
	}

	// 6. Write Data Payload
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA command failed: %w", err)
	}

	if _, err := w.Write(payload); err != nil {
		_ = w.Close()
		return fmt.Errorf("failed writing SMTP body payload: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed closing SMTP DATA stream: %w", err)
	}

	return nil
}

func (d *Dispatcher) chooseAuth(host string) smtp.Auth {
	return smtp.PlainAuth("", d.config.Username, d.config.Password, host)
}

func (d *Dispatcher) buildMIMEMessage(from string, to []string, subject string, msg *Message) ([]byte, error) {
	var buf bytes.Buffer

	boundaryMixed := fmt.Sprintf("==_FleetMixed_%d_==", time.Now().UnixNano())
	boundaryAlt := fmt.Sprintf("==_FleetAlt_%d_==", time.Now().UnixNano())

	// Top-level MIME Headers
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ", ")))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	buf.WriteString("MIME-Version: 1.0\r\n")

	if len(msg.Attachments) > 0 {
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundaryMixed))

		// Multipart Alternative part for text + HTML
		buf.WriteString(fmt.Sprintf("--%s\r\n", boundaryMixed))
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundaryAlt))

		// Plain text body
		if msg.TextBody != "" {
			buf.WriteString(fmt.Sprintf("--%s\r\n", boundaryAlt))
			buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
			buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
			buf.WriteString(msg.TextBody)
			buf.WriteString("\r\n\r\n")
		}

		// HTML body
		if msg.HTMLBody != "" {
			buf.WriteString(fmt.Sprintf("--%s\r\n", boundaryAlt))
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
			buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
			buf.WriteString(msg.HTMLBody)
			buf.WriteString("\r\n\r\n")
		}

		buf.WriteString(fmt.Sprintf("--%s--\r\n", boundaryAlt))

		// Attachments
		for _, att := range msg.Attachments {
			buf.WriteString(fmt.Sprintf("--%s\r\n", boundaryMixed))
			cType := att.ContentType
			if cType == "" {
				cType = "application/octet-stream"
			}
			buf.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", cType, att.Filename))
			buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename))
			buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

			encoded := base64.StdEncoding.EncodeToString(att.Data)
			// Wrap at 76 characters per RFC 2045
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				buf.WriteString(encoded[i:end] + "\r\n")
			}
			buf.WriteString("\r\n")
		}

		buf.WriteString(fmt.Sprintf("--%s--\r\n", boundaryMixed))
	} else {
		// Single or Alternative without attachments
		if msg.HTMLBody != "" && msg.TextBody != "" {
			buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundaryAlt))

			buf.WriteString(fmt.Sprintf("--%s\r\n", boundaryAlt))
			buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
			buf.WriteString(msg.TextBody)
			buf.WriteString("\r\n\r\n")

			buf.WriteString(fmt.Sprintf("--%s\r\n", boundaryAlt))
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
			buf.WriteString(msg.HTMLBody)
			buf.WriteString("\r\n\r\n")

			buf.WriteString(fmt.Sprintf("--%s--\r\n", boundaryAlt))
		} else if msg.HTMLBody != "" {
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
			buf.WriteString(msg.HTMLBody)
		} else {
			buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
			buf.WriteString(msg.TextBody)
		}
	}

	return buf.Bytes(), nil
}

// BuildAuditEmail creates a formatted Message with summary bodies and optional XLSX attachment.
func BuildAuditEmail(report *audit.AuditReport, xlsxData []byte, filename string) *Message {
	textBody := fmt.Sprintf(`GitLab Fleet Compliance & Security Audit Report
================================================================================
Generated At       : %s
Duration           : %s
Audited By         : %s
Repositories       : Scanned: %d | Compliant: %d | Non-Compliant: %d
Total Violations   : %d (Critical: %d, High: %d, Medium: %d, Low: %d)

Audit Breakdown:
- User Access & Expiration Violations         : %d
- Protected Branches Compliance Violations    : %d
- Protected Environments Deployment Violations: %d
- Pipeline Retention & Cleanup Violations     : %d
================================================================================
Please inspect the attached Excel workbook (%s) for detailed repository findings.
`,
		report.GeneratedAt.UTC().Format(time.RFC1123),
		report.DurationString,
		report.InitiatorDescription(),
		report.Summary.TotalProjectsScanned,
		report.Summary.CompliantProjectsCount,
		report.Summary.NonCompliantProjectsCount,
		report.Summary.TotalViolations,
		report.Summary.CriticalSeverityCount,
		report.Summary.HighSeverityCount,
		report.Summary.MediumSeverityCount,
		report.Summary.LowSeverityCount,
		report.Summary.UserAccessViolations,
		report.Summary.ProtectedBranchViolations,
		report.Summary.ProtectedEnvViolations,
		report.Summary.PipelineRetentionViolations,
		filename,
	)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family: Arial, sans-serif; color: #333333; line-height: 1.6; margin: 20px;">
  <div style="max-width: 650px; margin: auto; border: 1px solid #e1e4e8; border-radius: 6px; padding: 24px; background: #ffffff;">
    <h2 style="color: #1f497d; margin-top: 0; border-bottom: 2px solid #eaecef; padding-bottom: 8px;">GitLab Fleet Compliance & Security Audit</h2>
    <p style="color: #666666; font-size: 13px;"><b>Generated:</b> %s | <b>Scan Duration:</b> %s | <b>Audited By:</b> %s</p>
    
    <table style="width: 100%%; border-collapse: collapse; margin: 16px 0;">
      <tr style="background: #f6f8fa;"><th style="padding: 8px; border: 1px solid #d1d5da; text-align: left;">Metric</th><th style="padding: 8px; border: 1px solid #d1d5da; text-align: right;">Count</th></tr>
      <tr><td style="padding: 8px; border: 1px solid #e1e4e8;">Repositories Scanned</td><td style="padding: 8px; border: 1px solid #e1e4e8; text-align: right; font-weight: bold;">%d</td></tr>
      <tr><td style="padding: 8px; border: 1px solid #e1e4e8;">Fully Compliant</td><td style="padding: 8px; border: 1px solid #e1e4e8; text-align: right; font-weight: bold; color: #28a745;">%d</td></tr>
      <tr><td style="padding: 8px; border: 1px solid #e1e4e8;">Non-Compliant (Drift Detected)</td><td style="padding: 8px; border: 1px solid #e1e4e8; text-align: right; font-weight: bold; color: #cb2431;">%d</td></tr>
      <tr><td style="padding: 8px; border: 1px solid #e1e4e8;">Critical Severity Violations</td><td style="padding: 8px; border: 1px solid #e1e4e8; text-align: right; font-weight: bold; color: #cb2431;">%d</td></tr>
      <tr><td style="padding: 8px; border: 1px solid #e1e4e8;">High Severity Violations</td><td style="padding: 8px; border: 1px solid #e1e4e8; text-align: right; font-weight: bold; color: #cb2431;">%d</td></tr>
    </table>

    <p style="margin-top: 20px;">The detailed multi-sheet audit workbook <b>%s</b> is attached with complete field-level findings.</p>
  </div>
</body>
</html>`,
		report.GeneratedAt.UTC().Format(time.RFC1123),
		report.DurationString,
		report.InitiatorDescription(),
		report.Summary.TotalProjectsScanned,
		report.Summary.CompliantProjectsCount,
		report.Summary.NonCompliantProjectsCount,
		report.Summary.CriticalSeverityCount,
		report.Summary.HighSeverityCount,
		filename,
	)

	msg := &Message{
		TextBody: textBody,
		HTMLBody: htmlBody,
	}

	if len(xlsxData) > 0 {
		msg.Attachments = append(msg.Attachments, Attachment{
			Filename:    filename,
			ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			Data:        xlsxData,
		})
	}

	return msg
}

// GroupRoutingRule maps group hierarchy paths or numeric IDs to recipient email lists.
type GroupRoutingRule struct {
	GroupID    int      `yaml:"group_id" json:"group_id"`
	GroupPath  string   `yaml:"group_path" json:"group_path"`
	Recipients []string `yaml:"recipients" json:"recipients"`
}

// ResolveGroupRecipients finds matching recipients for a given project path or group ID.
func ResolveGroupRecipients(rules []GroupRoutingRule, groupID int, projectPath string) []string {
	var recipients []string
	seen := make(map[string]struct{})

	add := func(email string) {
		email = strings.TrimSpace(email)
		if email != "" {
			if _, ok := seen[email]; !ok {
				seen[email] = struct{}{}
				recipients = append(recipients, email)
			}
		}
	}

	pLower := strings.ToLower(projectPath)
	for _, rule := range rules {
		if rule.GroupID > 0 && rule.GroupID == groupID {
			for _, r := range rule.Recipients {
				add(r)
			}
		}
		if rule.GroupPath != "" {
			gpLower := strings.ToLower(strings.Trim(rule.GroupPath, "/"))
			if strings.HasPrefix(pLower, gpLower+"/") || pLower == gpLower {
				for _, r := range rule.Recipients {
					add(r)
				}
			}
		}
	}

	return recipients
}

// ----------------------------------------------------------------------------
// Custom LOGIN Authentication for Microsoft 365 and legacy SMTP servers
// ----------------------------------------------------------------------------

type loginAuth struct {
	username, password string
}

func newLoginAuth(username, password string) smtp.Auth {
	return &loginAuth{username, password}
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:", "username:":
			return []byte(a.username), nil
		case "Password:", "password:":
			return []byte(a.password), nil
		default:
			return nil, fmt.Errorf("unexpected server challenge in LOGIN auth: %s", string(fromServer))
		}
	}
	return nil, nil
}
