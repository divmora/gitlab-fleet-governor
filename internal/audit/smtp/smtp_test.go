package smtp_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/audit/smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAuditEmail(t *testing.T) {
	rep := &audit.AuditReport{
		Title:          "Audit Report",
		GeneratedAt:    time.Now(),
		Duration:       time.Second,
		DurationString: "1s",
		Summary: audit.SummaryMetrics{
			TotalProjectsScanned:      10,
			CompliantProjectsCount:    8,
			NonCompliantProjectsCount: 2,
			TotalViolations:           3,
			CriticalSeverityCount:     1,
			HighSeverityCount:         2,
			UserAccessViolations:      1,
			ProtectedBranchViolations: 2,
		},
	}

	attachmentData := []byte("PK\x03\x04mock_excel_data")
	msg := smtp.BuildAuditEmail(rep, attachmentData, "fleet-audit.xlsx")

	assert.NotEmpty(t, msg.TextBody)
	assert.Contains(t, msg.TextBody, "Total Violations   : 3")
	assert.Contains(t, msg.TextBody, "fleet-audit.xlsx")

	assert.NotEmpty(t, msg.HTMLBody)
	assert.Contains(t, msg.HTMLBody, "GitLab Fleet Compliance & Security Audit")
	assert.Contains(t, msg.HTMLBody, "fleet-audit.xlsx")

	require.Len(t, msg.Attachments, 1)
	assert.Equal(t, "fleet-audit.xlsx", msg.Attachments[0].Filename)
	assert.Equal(t, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", msg.Attachments[0].ContentType)
	assert.Equal(t, attachmentData, msg.Attachments[0].Data)
}

func TestResolveGroupRecipients(t *testing.T) {
	rules := []smtp.GroupRoutingRule{
		{
			GroupID:    10,
			GroupPath:  "fintech/core",
			Recipients: []string{"core-leads@example.com", "security@example.com"},
		},
		{
			GroupID:    20,
			GroupPath:  "infrastructure",
			Recipients: []string{"devops@example.com"},
		},
	}

	// Match by group path prefix
	r1 := smtp.ResolveGroupRecipients(rules, 0, "fintech/core/payments-api")
	assert.Contains(t, r1, "core-leads@example.com")
	assert.Contains(t, r1, "security@example.com")
	assert.NotContains(t, r1, "devops@example.com")

	// Match by numeric group ID
	r2 := smtp.ResolveGroupRecipients(rules, 20, "other/project")
	assert.Equal(t, []string{"devops@example.com"}, r2)

	// No match
	r3 := smtp.ResolveGroupRecipients(rules, 999, "unrelated/namespace")
	assert.Empty(t, r3)
}

func TestSMTPSendWithMockServer(t *testing.T) {
	// Start an in-process mock SMTP server on a dynamic port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	serverErrChan := make(chan error, 1)
	receivedCommands := make([]string, 0)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErrChan <- err
			return
		}
		defer func() { _ = conn.Close() }()

		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)

		// 220 Greeting
		_, _ = writer.WriteString("220 mock-smtp-server Service ready\r\n")
		_ = writer.Flush()

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimRight(line, "\r\n")
			receivedCommands = append(receivedCommands, line)

			cmd := strings.ToUpper(strings.Fields(line)[0])
			switch cmd {
			case "EHLO", "HELO":
				// Respond with capabilities (no TLS/Auth for this plain test)
				_, _ = writer.WriteString("250-mock-smtp-server Hello\r\n250 HELP\r\n")
				_ = writer.Flush()
			case "MAIL":
				_, _ = writer.WriteString("250 2.1.0 Sender OK\r\n")
				_ = writer.Flush()
			case "RCPT":
				_, _ = writer.WriteString("250 2.1.5 Recipient OK\r\n")
				_ = writer.Flush()
			case "DATA":
				_, _ = writer.WriteString("354 Start mail input; end with <CRLF>.<CRLF>\r\n")
				_ = writer.Flush()
				// Read data until "."
				for {
					dataLine, err := reader.ReadString('\n')
					if err != nil || dataLine == ".\r\n" || dataLine == ".\n" {
						break
					}
				}
				_, _ = writer.WriteString("250 2.0.0 OK: message queued\r\n")
				_ = writer.Flush()
			case "QUIT":
				_, _ = writer.WriteString("221 2.0.0 Bye\r\n")
				_ = writer.Flush()
				serverErrChan <- nil
				return
			default:
				_, _ = writer.WriteString("500 Command not recognized\r\n")
				_ = writer.Flush()
			}
		}
		serverErrChan <- nil
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	cfg := smtp.Config{
		Host:       tcpAddr.IP.String(),
		Port:       tcpAddr.Port,
		From:       "audit-bot@example.com",
		To:         []string{"compliance@example.com"},
		Subject:    "Automated Audit Report",
		StartTLS:   false,
		DirectTLS:  false,
		SendAttach: true,
	}

	dispatcher := smtp.NewDispatcher(cfg)
	msg := &smtp.Message{
		From:     "audit-bot@example.com",
		To:       []string{"compliance@example.com"},
		Subject:  "Automated Audit Report",
		TextBody: "This is a test audit email.",
		HTMLBody: "<p>This is a test audit email.</p>",
		Attachments: []smtp.Attachment{
			{
				Filename:    "report.xlsx",
				ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
				Data:        []byte("mock excel content"),
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = dispatcher.Send(ctx, msg)
	require.NoError(t, err)

	serverErr := <-serverErrChan
	require.NoError(t, serverErr)

	// Verify protocol commands were exchanged
	assert.True(t, len(receivedCommands) >= 4)
	assert.True(t, strings.HasPrefix(receivedCommands[0], "EHLO") || strings.HasPrefix(receivedCommands[0], "HELO"))
	assert.Contains(t, strings.Join(receivedCommands, " "), "MAIL FROM:<audit-bot@example.com>")
	assert.Contains(t, strings.Join(receivedCommands, " "), "RCPT TO:<compliance@example.com>")
	assert.Contains(t, strings.Join(receivedCommands, " "), "DATA")
}
