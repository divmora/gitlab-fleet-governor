// Package notify sends run and audit summaries to Slack, Microsoft Teams,
// AWS SNS and generic webhooks.
package notify

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
)

// MaxItems is the number of findings included in a notification.
const MaxItems = 5

// Item is one finding or drift entry in a notification.
type Item struct {
	Severity string `json:"severity"`
	Target   string `json:"target"`
	Source   string `json:"source"` // reconciler or audit module
	Message  string `json:"message"`
}

// Summary is the channel-neutral content of a notification. It is also
// the JSON body sent to SNS and generic webhooks.
type Summary struct {
	Tool        string         `json:"tool"`
	Version     string         `json:"version,omitempty"`
	Command     string         `json:"command"` // "run" or "audit"
	Mode        string         `json:"mode"`    // "plan", "apply" or "audit"
	Scope       string         `json:"scope,omitempty"`
	Severity    string         `json:"severity"`
	Counts      map[string]int `json:"counts"`
	Scanned     int            `json:"scanned"`
	Changed     int            `json:"changed,omitempty"`
	Failed      int            `json:"failed,omitempty"`
	Violations  int            `json:"violations,omitempty"`
	Instances   []string       `json:"instances,omitempty"`
	ReportPath  string         `json:"report_path,omitempty"`
	GeneratedAt time.Time      `json:"generated_at"`
	Items       []Item         `json:"items"`
	TotalItems  int            `json:"total_items"`
}

// Headline is the one-line summary used as the message title.
func (s *Summary) Headline() string {
	what := "governance run"
	if s.Command == "audit" {
		what = "compliance audit"
	} else if s.Mode == "plan" {
		what = "governance plan"
	}
	scope := ""
	if s.Scope != "" {
		scope = " in " + s.Scope
	}
	c := s.Counts
	if c[config.NotifyCritical]+c[config.NotifyWarning] == 0 {
		return fmt.Sprintf("GitLab Fleet Governor: %s%s found no issues", what, scope)
	}
	return fmt.Sprintf("GitLab Fleet Governor: %s%s found %d issue(s) (%d critical, %d warning)",
		what, scope, c[config.NotifyCritical]+c[config.NotifyWarning], c[config.NotifyCritical], c[config.NotifyWarning])
}

// ForChannel returns a copy whose items are limited to those at or above
// min and capped at MaxItems.
func (s *Summary) ForChannel(min string) *Summary {
	cp := *s
	cp.Items = nil
	for _, it := range s.Items {
		if config.NotifySeverityRank(it.Severity) >= config.NotifySeverityRank(min) {
			cp.Items = append(cp.Items, it)
		}
	}
	cp.TotalItems = len(cp.Items)
	if len(cp.Items) > MaxItems {
		cp.Items = cp.Items[:MaxItems]
	}
	return &cp
}

func newSummary(command, mode string) *Summary {
	return &Summary{
		Tool:        "gitlab-fleet-governor",
		Command:     command,
		Mode:        mode,
		Severity:    config.NotifyInfo,
		Counts:      map[string]int{config.NotifyCritical: 0, config.NotifyWarning: 0, config.NotifyInfo: 0},
		GeneratedAt: time.Now().UTC(),
	}
}

func (s *Summary) add(it Item) {
	s.Items = append(s.Items, it)
	s.Counts[it.Severity]++
	if config.NotifySeverityRank(it.Severity) > config.NotifySeverityRank(s.Severity) {
		s.Severity = it.Severity
	}
}

func (s *Summary) finish() {
	sort.SliceStable(s.Items, func(i, j int) bool {
		return config.NotifySeverityRank(s.Items[i].Severity) > config.NotifySeverityRank(s.Items[j].Severity)
	})
	s.TotalItems = len(s.Items)
}

func label(instance, path string) string {
	return audit.Label(instance, path)
}

// FromExecution summarises a run: failed operations and errors are
// critical, drift and applied changes are warnings.
func FromExecution(res *engine.ExecutionResult, execErr error) *Summary {
	mode := "apply"
	if res != nil && res.DryRun {
		mode = "plan"
	}
	s := newSummary("run", mode)
	if res == nil {
		if execErr != nil {
			s.add(Item{Severity: config.NotifyCritical, Source: "run", Message: execErr.Error()})
		}
		s.finish()
		return s
	}
	if res.Metrics != nil {
		s.Scanned, s.Changed, s.Failed = res.Metrics.TotalScanned, res.Metrics.TotalChanged, res.Metrics.TotalFailed
	}
	for _, in := range res.Instances {
		s.Instances = append(s.Instances, in.Name)
	}

	for _, tr := range res.TargetResults {
		if tr == nil {
			continue
		}
		target := label(tr.Instance, tr.TargetPath)
		for _, op := range tr.Operations {
			if op == nil {
				continue
			}
			switch {
			case !op.Success || op.Error != nil:
				msg := "operation failed"
				if op.Error != nil {
					msg = op.Error.Error()
				}
				s.add(Item{Severity: config.NotifyCritical, Target: target, Source: op.OperationName, Message: msg})
			case op.HasChanges:
				verb := "drift detected"
				if !res.DryRun {
					verb = "changes applied"
				}
				fields := 0
				for _, d := range op.Diffs {
					fields += len(d.Fields)
				}
				s.add(Item{Severity: config.NotifyWarning, Target: target, Source: op.OperationName, Message: fmt.Sprintf("%s (%d field(s))", verb, fields)})
			}
		}
	}
	// Errors not tied to a target (discovery, licence, instance failures).
	if len(res.TargetResults) == 0 {
		for _, err := range res.Errors {
			s.add(Item{Severity: config.NotifyCritical, Source: "run", Message: err.Error()})
		}
		if len(res.Errors) == 0 && execErr != nil {
			s.add(Item{Severity: config.NotifyCritical, Source: "run", Message: execErr.Error()})
		}
	}
	s.finish()
	return s
}

func auditSeverity(sev audit.Severity) (string, bool) {
	switch sev {
	case audit.SeverityCritical, audit.SeverityHigh:
		return config.NotifyCritical, true
	case audit.SeverityMedium, audit.SeverityLow:
		return config.NotifyWarning, true
	default:
		return "", false
	}
}

// FromAudit summarises an audit: CRITICAL and HIGH findings are critical,
// MEDIUM and LOW are warnings, and passing checks are left out.
func FromAudit(rep *audit.AuditReport, reportPath string) *Summary {
	s := newSummary("audit", "audit")
	s.ReportPath = reportPath
	if rep == nil {
		s.finish()
		return s
	}
	s.Version = rep.GovernorVersion
	s.Scanned = rep.Summary.TotalProjectsScanned
	s.Violations = rep.Summary.TotalViolations
	for _, in := range rep.Instances {
		s.Instances = append(s.Instances, in.Name)
	}

	add := func(sev audit.Severity, instance, target, module, msg string) {
		if level, ok := auditSeverity(sev); ok {
			s.add(Item{Severity: level, Target: label(instance, target), Source: module, Message: msg})
		}
	}
	for _, f := range rep.UserAccessFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "user_access", "@"+f.Username+": "+f.Details)
	}
	for _, f := range rep.BotAccessFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "user_access", "@"+f.Username+": "+f.Details)
	}
	for _, f := range rep.ProtectedBranchFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "protected_branches", f.BranchName+": "+f.Details)
	}
	for _, f := range rep.ProtectedEnvFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "protected_environments", f.EnvironmentName+": "+f.Details)
	}
	for _, f := range rep.PipelineRetentionFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "pipeline_retention", f.Details)
	}
	for _, f := range rep.RepositoryFileFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "repository_files", f.FilePath+": "+f.Details)
	}
	for _, f := range rep.ProtectedTagFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "protected_tags", f.TagNamePattern+": "+f.Details)
	}
	for _, f := range rep.DeployKeyFindings {
		add(f.Severity, f.Instance, f.ProjectPath, "deploy_keys", f.KeyTitle+": "+f.Details)
	}
	for _, f := range rep.DeployTokenFindings {
		add(f.Severity, f.Instance, f.OwnerPath, "deploy_tokens", f.TokenName+": "+f.Details)
	}
	for _, f := range rep.GroupSettingFindings {
		add(f.Severity, f.Instance, f.GroupPath, "group_settings", f.Details)
	}
	s.finish()
	return s
}

// truncateText shortens s to n runes for channels with length limits.
func truncateText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
