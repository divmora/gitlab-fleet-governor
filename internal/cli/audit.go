package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	auditreport "github.com/divmora/gitlab-fleet-governor/internal/audit/report"
	auditsmtp "github.com/divmora/gitlab-fleet-governor/internal/audit/smtp"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/spf13/cobra"
)

type auditFlags struct {
	Modules     string
	Format      string
	OutputFile  string
	Timeout     time.Duration
	Concurrency int

	// Service Account & Bot Classification flags
	ServiceAccounts string
	BotPatterns     string

	// SMTP flags
	SMTPHost       string
	SMTPPort       int
	SMTPUsername   string
	SMTPPassword   string
	SMTPFrom       string
	SMTPTo         string
	SMTPCc         string
	SMTPBcc        string
	SMTPSubject    string
	SMTPGreeting   string
	SMTPStartTLS   bool
	SMTPDirectTLS  bool
	SMTPSkipVerify bool
	SMTPSendAttach bool
}

func newAuditCmd() *cobra.Command {
	var flags auditFlags

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Execute fleet-wide compliance & security audits with multi-sheet Excel and email distribution",
		Long: `Audit runs read-only, non-mutating compliance inspections across target groups
and projects. It evaluates user access expiration hygiene, protected branch
configurations, protected environment deployment constraints, and CI/CD pipeline
retention policies & unpruned stale pipeline accumulation, rendering
executive multi-sheet Excel (.xlsx) workbooks, JSON, CSV, Markdown, HTML, or
terminal tables, with automated SMTP email distribution.`,
		Example: `  # Audit fleet and display terminal report
  gitlab-fleet-governor audit -c config.yaml

  # Generate formatted Excel (.xlsx) report with 20 parallel workers
  gitlab-fleet-governor audit -c config.yaml -o audit-report.xlsx --concurrency=20

  # Audit specific modules with JSON export
  gitlab-fleet-governor audit -c config.yaml --modules=user_access,protected_branches,pipeline_retention --format=json -o audit.json

  # Audit fleet and automatically email multi-sheet Excel report via SMTP
  gitlab-fleet-governor audit -c config.yaml -o audit-report.xlsx \
    --smtp-host=smtp.mailgun.org --smtp-port=587 \
    --smtp-username=postmaster@example.com --smtp-password=secret \
    --smtp-from=security@example.com --smtp-to=compliance-team@example.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if flags.Timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, flags.Timeout)
				defer cancel()
			}

			return executeAudit(ctx, cmd, flags)
		},
	}

	cmd.Flags().StringVar(&flags.Modules, "modules", strings.Join(audit.AllModuleNames(), ","), "Comma-separated audit modules to execute (user_access, protected_branches, protected_environments, pipeline_retention)")
	cmd.Flags().StringVar(&flags.Format, "format", "", "Audit report presentation format (xlsx, json, csv, markdown, html, table)")
	cmd.Flags().StringVarP(&flags.OutputFile, "output-file", "o", "", "Destination file path for the audit report (e.g. audit.xlsx, report.json)")
	cmd.Flags().IntVar(&flags.Concurrency, "concurrency", 10, "Number of concurrent worker goroutines for fleet audits")
	cmd.Flags().DurationVar(&flags.Timeout, "timeout", 0, "Global execution timeout duration (e.g. 15m, 1h; default: no timeout)")
	cmd.Flags().StringVar(&flags.ServiceAccounts, "service-accounts", os.Getenv("FLEET_SERVICE_ACCOUNTS"), "Comma-separated service account usernames, emails, or user IDs (env: FLEET_SERVICE_ACCOUNTS)")
	cmd.Flags().StringVar(&flags.BotPatterns, "bot-patterns", os.Getenv("FLEET_BOT_PATTERNS"), "Comma-separated wildcard or substring patterns for bot/service accounts (env: FLEET_BOT_PATTERNS)")

	// SMTP Distribution Flags
	cmd.Flags().StringVar(&flags.SMTPHost, "smtp-host", os.Getenv("SMTP_HOST"), "SMTP relay host address (env: SMTP_HOST)")
	cmd.Flags().IntVar(&flags.SMTPPort, "smtp-port", 587, "SMTP relay port (default: 587, or 465 for direct TLS)")
	cmd.Flags().StringVar(&flags.SMTPUsername, "smtp-username", os.Getenv("SMTP_USERNAME"), "SMTP username (env: SMTP_USERNAME)")
	cmd.Flags().StringVar(&flags.SMTPPassword, "smtp-password", os.Getenv("SMTP_PASSWORD"), "SMTP password (env: SMTP_PASSWORD)")
	cmd.Flags().StringVar(&flags.SMTPFrom, "smtp-from", os.Getenv("SMTP_FROM"), "Email sender 'From' address (env: SMTP_FROM)")
	cmd.Flags().StringVar(&flags.SMTPTo, "smtp-to", os.Getenv("SMTP_TO"), "Comma-separated email recipients (env: SMTP_TO)")
	cmd.Flags().StringVar(&flags.SMTPCc, "smtp-cc", os.Getenv("SMTP_CC"), "Comma-separated CC email recipients (env: SMTP_CC)")
	cmd.Flags().StringVar(&flags.SMTPBcc, "smtp-bcc", os.Getenv("SMTP_BCC"), "Comma-separated BCC email recipients (env: SMTP_BCC)")
	cmd.Flags().StringVar(&flags.SMTPSubject, "smtp-subject", "GitLab Fleet Compliance & Security Audit Report", "Subject line for email dispatch")
	cmd.Flags().BoolVar(&flags.SMTPStartTLS, "smtp-starttls", true, "Enable STARTTLS on port 587 or 25")
	cmd.Flags().BoolVar(&flags.SMTPDirectTLS, "smtp-direct-tls", false, "Use direct SSL/TLS (implicit TLS on port 465)")
	cmd.Flags().BoolVar(&flags.SMTPSkipVerify, "smtp-skip-tls-verify", false, "Skip TLS certificate verification (insecure/dev only)")
	cmd.Flags().BoolVar(&flags.SMTPSendAttach, "smtp-send-attachment", true, "Attach Excel (.xlsx) report to the dispatched email")
	cmd.Flags().StringVar(&flags.SMTPGreeting, "smtp-greeting", os.Getenv("SMTP_GREETING"), "Greeting line prepended to the email body (env: SMTP_GREETING); defaults to 'Hello Team,' when empty")

	return cmd
}

func executeAudit(ctx context.Context, cmd *cobra.Command, flags auditFlags) error {
	// 1. Load configuration
	cfg, sourceDesc, err := config.Load(ctx, globalFlags.ConfigPath, config.LoadOptions{
		LoaderOptions: []config.LoaderOption{config.WithStdin(cmd.InOrStdin())},
	})
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	concurrency := flags.Concurrency
	if cmd.Flags().Changed("concurrency") {
		concurrency = flags.Concurrency
	} else if globalFlags.Concurrency > 0 {
		concurrency = globalFlags.Concurrency
	} else if cfg.Settings.Concurrency > 0 {
		concurrency = cfg.Settings.Concurrency
	}
	if concurrency <= 0 {
		concurrency = 10
	}

	slog.Info("Configuration loaded for audit",
		"source", sourceDesc,
		"concurrency", concurrency,
	)

	// 2. Initialize resilient GitLab API client
	client, err := gitlab.NewClientFromConfig(&cfg.Settings.GitLab)
	if err != nil {
		return fmt.Errorf("failed to initialize GitLab client: %w", err)
	}

	// 3. Parse active audit modules
	var modules []string
	if flags.Modules != "" {
		for _, m := range strings.Split(flags.Modules, ",") {
			trimmed := strings.TrimSpace(m)
			if trimmed != "" {
				modules = append(modules, trimmed)
			}
		}
	}
	if len(modules) == 0 {
		modules = audit.AllModuleNames()
	}

	// 4. Resolve service accounts and bot patterns (from config, CLI flags, or env)
	var serviceAccounts []string
	serviceAccounts = append(serviceAccounts, cfg.Settings.ServiceAccounts...)
	if flags.ServiceAccounts != "" {
		for _, sa := range strings.Split(flags.ServiceAccounts, ",") {
			trimmed := strings.TrimSpace(sa)
			if trimmed != "" {
				serviceAccounts = append(serviceAccounts, trimmed)
			}
		}
	}

	var botPatterns []string
	botPatterns = append(botPatterns, cfg.Settings.BotPatterns...)
	if flags.BotPatterns != "" {
		for _, bp := range strings.Split(flags.BotPatterns, ",") {
			trimmed := strings.TrimSpace(bp)
			if trimmed != "" {
				botPatterns = append(botPatterns, trimmed)
			}
		}
	}

	// 5. Construct Auditor coordinator
	auditor, err := audit.NewAuditor(client,
		audit.WithAuditorConcurrency(concurrency),
		audit.WithAuditorTargets(cfg.Targets),
		audit.WithAuditorModules(modules),
		audit.WithAuditorServiceAccounts(serviceAccounts, botPatterns),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize auditor: %w", err)
	}

	// 6. Run fleet discovery and audits
	reportData, err := auditor.Execute(ctx)
	if err != nil {
		return fmt.Errorf("audit execution failed: %w", err)
	}

	// 6. Determine Export Format and Destination
	outputFile := flags.OutputFile
	if outputFile == "" {
		outputFile = globalFlags.OutputFile
	}

	exportFormat := auditreport.FormatTable
	if flags.Format != "" {
		parsed, err := auditreport.ParseExportFormat(flags.Format)
		if err != nil {
			return err
		}
		exportFormat = parsed
	} else if outputFile != "" {
		ext := strings.ToLower(filepath.Ext(outputFile))
		switch ext {
		case ".xlsx":
			exportFormat = auditreport.FormatXLSX
		case ".json":
			exportFormat = auditreport.FormatJSON
		case ".csv":
			exportFormat = auditreport.FormatCSV
		case ".md", ".markdown":
			exportFormat = auditreport.FormatMarkdown
		case ".html", ".htm":
			exportFormat = auditreport.FormatHTML
		default:
			exportFormat = auditreport.FormatTable
		}
	}

	// If format is XLSX and no output file was specified, set a default filename
	if exportFormat == auditreport.FormatXLSX && outputFile == "" {
		outputFile = "gitlab-fleet-audit.xlsx"
	}

	// 7. Write Report Output
	var xlsxBytes []byte
	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("failed to create output report file '%s': %w", outputFile, err)
		}
		defer func() { _ = f.Close() }()

		if exportFormat == auditreport.FormatXLSX {
			var buf bytes.Buffer
			mw := io.MultiWriter(f, &buf)
			if err := auditreport.ExportAuditReport(reportData, exportFormat, mw); err != nil {
				return fmt.Errorf("failed to generate xlsx report: %w", err)
			}
			xlsxBytes = buf.Bytes()
		} else {
			if err := auditreport.ExportAuditReport(reportData, exportFormat, f); err != nil {
				return fmt.Errorf("failed to export report: %w", err)
			}
		}

		slog.Info("Audit report successfully written", "format", exportFormat, "path", outputFile)
		fmt.Fprintf(cmd.ErrOrStderr(), "Audit report successfully written to %s (format: %s)\n", outputFile, exportFormat)
	} else {
		// Output to stdout
		if err := auditreport.ExportAuditReport(reportData, exportFormat, cmd.OutOrStdout()); err != nil {
			return fmt.Errorf("failed to render report: %w", err)
		}
	}

	// Also print concise table summary to stderr if written to a non-table file
	if outputFile != "" && exportFormat != auditreport.FormatTable {
		_ = auditreport.ExportAuditReport(reportData, auditreport.FormatTable, cmd.ErrOrStderr())
	}

	// 8. SMTP Distribution (if configured)
	if flags.SMTPHost != "" || flags.SMTPTo != "" || flags.SMTPCc != "" || flags.SMTPBcc != "" {
		if err := dispatchAuditEmail(ctx, flags, reportData, xlsxBytes, outputFile); err != nil {
			slog.Error("Failed to dispatch audit report email", "error", err)
			return fmt.Errorf("smtp dispatch failed: %w", err)
		}
		slog.Info("Audit report email successfully dispatched", "to", flags.SMTPTo, "cc", flags.SMTPCc, "bcc", flags.SMTPBcc, "host", flags.SMTPHost)
		msgRecipients := flags.SMTPTo
		if flags.SMTPCc != "" {
			if msgRecipients != "" {
				msgRecipients += fmt.Sprintf(" (cc: %s)", flags.SMTPCc)
			} else {
				msgRecipients = fmt.Sprintf("(cc: %s)", flags.SMTPCc)
			}
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Audit report successfully emailed to %s via %s\n", msgRecipients, flags.SMTPHost)
	}

	if reportData.Summary.CriticalSeverityCount > 0 || reportData.Summary.HighSeverityCount > 0 {
		return fmt.Errorf("audit completed with %d critical/high security violations (see report)",
			reportData.Summary.CriticalSeverityCount+reportData.Summary.HighSeverityCount)
	}

	return nil
}

func dispatchAuditEmail(
	ctx context.Context,
	flags auditFlags,
	reportData *audit.AuditReport,
	xlsxBytes []byte,
	outputFile string,
) error {
	var toRecipients []string
	for _, r := range strings.Split(flags.SMTPTo, ",") {
		trimmed := strings.TrimSpace(r)
		if trimmed != "" {
			toRecipients = append(toRecipients, trimmed)
		}
	}

	var ccRecipients []string
	for _, r := range strings.Split(flags.SMTPCc, ",") {
		trimmed := strings.TrimSpace(r)
		if trimmed != "" {
			ccRecipients = append(ccRecipients, trimmed)
		}
	}

	var bccRecipients []string
	for _, r := range strings.Split(flags.SMTPBcc, ",") {
		trimmed := strings.TrimSpace(r)
		if trimmed != "" {
			bccRecipients = append(bccRecipients, trimmed)
		}
	}

	if len(toRecipients) == 0 && len(ccRecipients) == 0 && len(bccRecipients) == 0 {
		return fmt.Errorf("no email recipients configured (--smtp-to, --smtp-cc, or --smtp-bcc)")
	}

	// If xlsxBytes not generated yet (e.g. format was json), generate in-memory for attachment
	if flags.SMTPSendAttach && len(xlsxBytes) == 0 {
		var buf bytes.Buffer
		if err := auditreport.GenerateXLSX(reportData, &buf); err == nil {
			xlsxBytes = buf.Bytes()
		}
	}

	attachFilename := "gitlab-fleet-audit.xlsx"
	if outputFile != "" && strings.HasSuffix(strings.ToLower(outputFile), ".xlsx") {
		attachFilename = filepath.Base(outputFile)
	}

	emailMsg := auditsmtp.BuildAuditEmail(reportData, xlsxBytes, attachFilename, flags.SMTPGreeting)
	emailMsg.From = flags.SMTPFrom
	emailMsg.To = toRecipients
	emailMsg.Cc = ccRecipients
	emailMsg.Bcc = bccRecipients
	emailMsg.Subject = flags.SMTPSubject

	smtpCfg := auditsmtp.Config{
		Host:          flags.SMTPHost,
		Port:          flags.SMTPPort,
		Username:      flags.SMTPUsername,
		Password:      flags.SMTPPassword,
		From:          flags.SMTPFrom,
		To:            toRecipients,
		Cc:            ccRecipients,
		Bcc:           bccRecipients,
		Subject:       flags.SMTPSubject,
		StartTLS:      flags.SMTPStartTLS,
		DirectTLS:     flags.SMTPDirectTLS,
		SkipTLSVerify: flags.SMTPSkipVerify,
		SendAttach:    flags.SMTPSendAttach,
	}

	dispatcher := auditsmtp.NewDispatcher(smtpCfg)
	return dispatcher.Send(ctx, emailMsg)
}
