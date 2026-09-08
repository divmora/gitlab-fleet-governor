package audit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// ProjectModuleAuditor is the interface implemented by audit modules.
type ProjectModuleAuditor interface {
	Name() string
}

// UserAccessModuleAuditor defines project audit for user access.
type UserAccessModuleAuditor interface {
	AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]UserAccessFinding, error)
}

// ProtectedBranchesModuleAuditor defines project audit for protected branches.
type ProtectedBranchesModuleAuditor interface {
	AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]ProtectedBranchFinding, error)
}

// ProtectedEnvironmentsModuleAuditor defines project audit for protected environments.
type ProtectedEnvironmentsModuleAuditor interface {
	AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]ProtectedEnvironmentFinding, error)
}

// Auditor coordinates fleet discovery and parallel module audits.
type Auditor struct {
	client        gl.GitLabClient
	targets       config.TargetSelectors
	concurrency   int
	activeModules map[string]bool
	classifier    *BotClassifier

	userAccessAuditor  UserAccessModuleAuditor
	protectedBranchAud ProtectedBranchesModuleAuditor
	protectedEnvAud    ProtectedEnvironmentsModuleAuditor
}

// AuditorOption provides functional configuration for Auditor.
type AuditorOption func(*Auditor)

// WithAuditorConcurrency sets the worker concurrency.
func WithAuditorConcurrency(c int) AuditorOption {
	return func(a *Auditor) {
		if c > 0 {
			a.concurrency = c
		}
	}
}

// WithAuditorTargets sets target discovery selectors.
func WithAuditorTargets(targets config.TargetSelectors) AuditorOption {
	return func(a *Auditor) {
		a.targets = targets
	}
}

// WithAuditorModules filters the active audit modules by name.
func WithAuditorModules(modules []string) AuditorOption {
	return func(a *Auditor) {
		if len(modules) > 0 {
			a.activeModules = make(map[string]bool)
			for _, m := range modules {
				a.activeModules[strings.ToLower(strings.TrimSpace(m))] = true
			}
		}
	}
}

// WithAuditorClassifier sets a custom BotClassifier for account classification.
func WithAuditorClassifier(c *BotClassifier) AuditorOption {
	return func(a *Auditor) {
		a.classifier = c
	}
}

// WithAuditorServiceAccounts configures explicit service accounts and bot matching patterns.
func WithAuditorServiceAccounts(serviceAccounts []string, botPatterns []string) AuditorOption {
	return func(a *Auditor) {
		a.classifier = NewBotClassifier(serviceAccounts, botPatterns)
	}
}

// WithAuditorSubModules allows injecting custom module implementations (for testing or extensibility).
func WithAuditorSubModules(
	userAccess UserAccessModuleAuditor,
	protectedBranches ProtectedBranchesModuleAuditor,
	protectedEnvironments ProtectedEnvironmentsModuleAuditor,
) AuditorOption {
	return func(a *Auditor) {
		if userAccess != nil {
			a.userAccessAuditor = userAccess
		}
		if protectedBranches != nil {
			a.protectedBranchAud = protectedBranches
		}
		if protectedEnvironments != nil {
			a.protectedEnvAud = protectedEnvironments
		}
	}
}

// NewAuditor constructs an initialized Auditor instance.
func NewAuditor(client gl.GitLabClient, opts ...AuditorOption) (*Auditor, error) {
	if client == nil {
		return nil, fmt.Errorf("gitlab client cannot be nil")
	}

	a := &Auditor{
		client:        client,
		concurrency:   10,
		activeModules: make(map[string]bool),
	}

	for _, m := range AllModuleNames() {
		a.activeModules[m] = true
	}

	for _, opt := range opts {
		opt(a)
	}

	reg := NewUserRegistry(client, a.classifier)

	if a.userAccessAuditor == nil {
		ua := NewUserAccessAuditor(reg)
		ua.SetClassifier(a.classifier)
		a.userAccessAuditor = ua
	} else {
		if setter, ok := a.userAccessAuditor.(interface{ SetUserRegistry(*UserRegistry) }); ok {
			setter.SetUserRegistry(reg)
		}
		if setter, ok := a.userAccessAuditor.(interface{ SetClassifier(*BotClassifier) }); ok {
			setter.SetClassifier(a.classifier)
		}
	}

	if a.protectedBranchAud == nil {
		a.protectedBranchAud = NewProtectedBranchesAuditor(reg)
	} else if setter, ok := a.protectedBranchAud.(interface{ SetUserRegistry(*UserRegistry) }); ok {
		setter.SetUserRegistry(reg)
	}

	if a.protectedEnvAud == nil {
		a.protectedEnvAud = NewProtectedEnvironmentsAuditor(reg)
	} else if setter, ok := a.protectedEnvAud.(interface{ SetUserRegistry(*UserRegistry) }); ok {
		setter.SetUserRegistry(reg)
	}

	return a, nil
}

// Execute performs fleet discovery and runs all active audit modules across the target projects.
func (a *Auditor) Execute(ctx context.Context) (*AuditReport, error) {
	startTime := time.Now()

	slog.Info("Starting fleet-wide compliance & security audit",
		"concurrency", a.concurrency,
		"modules", a.activeModuleList(),
	)

	// 1. Discover Target Fleet Projects
	fleet, err := discovery.DiscoverFleet(ctx, a.client, a.targets, discovery.WithConcurrency(a.concurrency))
	if err != nil {
		return nil, fmt.Errorf("fleet discovery for audit failed: %w", err)
	}

	projects := fleet.ProjectList()
	slog.Info("Fleet discovery completed for audit",
		"scanned_projects", fleet.ScannedProjectsCount,
		"matched_projects", len(projects),
	)

	activeCount := 0
	archivedCount := 0
	for _, p := range projects {
		if p.Archived {
			archivedCount++
		} else {
			activeCount++
		}
	}

	report := &AuditReport{
		Title:         "GitLab Fleet Compliance & Security Audit Report",
		GeneratedAt:   startTime,
		ActiveModules: a.activeModuleList(),
		Summary: SummaryMetrics{
			TotalProjectsScanned:  len(projects),
			ActiveProjectsCount:   activeCount,
			ArchivedProjectsCount: archivedCount,
		},
		UserAccessFindings:      make([]UserAccessFinding, 0),
		BotAccessFindings:       make([]UserAccessFinding, 0),
		ProtectedBranchFindings: make([]ProtectedBranchFinding, 0),
		ProtectedEnvFindings:    make([]ProtectedEnvironmentFinding, 0),
	}

	// Register user registry on modules
	var reg *UserRegistry
	if uAud, ok := a.userAccessAuditor.(*UserAccessAuditor); ok {
		reg = uAud.registry
	}

	if len(projects) == 0 {
		report.Duration = time.Since(startTime)
		report.DurationString = report.Duration.Round(time.Millisecond).String()
		report.ComputeSummary()
		return report, nil
	}

	// 2. Concurrently audit each matched project
	var mu sync.Mutex
	sem := make(chan struct{}, a.concurrency)
	var wg sync.WaitGroup
	var auditErrors []error

	for _, proj := range projects {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(p *discovery.TargetProject) {
			defer wg.Done()
			defer func() { <-sem }()

			uFindings, bFindings, eFindings, err := a.auditSingleProject(ctx, p)
			if err != nil {
				slog.Warn("Failed to audit project", "project", p.PathWithNamespace, "error", err)
				mu.Lock()
				auditErrors = append(auditErrors, fmt.Errorf("project %s: %w", p.PathWithNamespace, err))
				mu.Unlock()
				return
			}

			mu.Lock()
			for _, uf := range uFindings {
				if uf.IsBot {
					report.BotAccessFindings = append(report.BotAccessFindings, uf)
				} else {
					report.UserAccessFindings = append(report.UserAccessFindings, uf)
				}
			}
			if len(bFindings) > 0 {
				report.ProtectedBranchFindings = append(report.ProtectedBranchFindings, bFindings...)
			}
			if len(eFindings) > 0 {
				report.ProtectedEnvFindings = append(report.ProtectedEnvFindings, eFindings...)
			}
			mu.Unlock()
		}(proj)
	}

	wg.Wait()

	// 3. Populate fleet-wide user directory
	if reg != nil {
		report.UserDirectory = reg.AllUsers()
	}

	// 4. Finalize report metadata and metrics
	report.SortFindings()
	report.ComputeSummary()
	report.Duration = time.Since(startTime)
	report.DurationString = report.Duration.Round(time.Millisecond).String()

	slog.Info("Fleet audit finished",
		"duration", report.DurationString,
		"projects_scanned", report.Summary.TotalProjectsScanned,
		"violations", report.Summary.TotalViolations,
		"critical", report.Summary.CriticalSeverityCount,
		"high", report.Summary.HighSeverityCount,
	)

	return report, nil
}

func (a *Auditor) auditSingleProject(ctx context.Context, p *discovery.TargetProject) (
	[]UserAccessFinding,
	[]ProtectedBranchFinding,
	[]ProtectedEnvironmentFinding,
	error,
) {
	var uFindings []UserAccessFinding
	var bFindings []ProtectedBranchFinding
	var eFindings []ProtectedEnvironmentFinding

	// User Access Module
	if a.isModuleActive(string(ModuleUserAccess)) && a.userAccessAuditor != nil {
		findings, err := a.userAccessAuditor.AuditProject(ctx, a.client, p)
		if err != nil {
			return nil, nil, nil, err
		}
		uFindings = findings
	}

	// Protected Branches Module
	if a.isModuleActive(string(ModuleProtectedBranches)) && a.protectedBranchAud != nil {
		findings, err := a.protectedBranchAud.AuditProject(ctx, a.client, p)
		if err != nil {
			return nil, nil, nil, err
		}
		bFindings = findings
	}

	// Protected Environments Module
	if a.isModuleActive(string(ModuleProtectedEnvironments)) && a.protectedEnvAud != nil {
		findings, err := a.protectedEnvAud.AuditProject(ctx, a.client, p)
		if err != nil {
			return nil, nil, nil, err
		}
		eFindings = findings
	}

	return uFindings, bFindings, eFindings, nil
}

func (a *Auditor) isModuleActive(mod string) bool {
	return a.activeModules[strings.ToLower(mod)]
}

func (a *Auditor) activeModuleList() []string {
	var list []string
	for _, m := range AllModuleNames() {
		if a.activeModules[m] {
			list = append(list, m)
		}
	}
	return list
}
