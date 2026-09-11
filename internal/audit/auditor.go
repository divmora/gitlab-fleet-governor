package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	gitlab "gitlab.com/gitlab-org/api/client-go"
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

// PipelineRetentionModuleAuditor defines project audit for pipeline retention policies.
type PipelineRetentionModuleAuditor interface {
	AuditProject(ctx context.Context, client gl.GitLabClient, project *discovery.TargetProject) ([]PipelineRetentionFinding, error)
}

// Auditor coordinates fleet discovery and parallel module audits.
type Auditor struct {
	client        gl.GitLabClient
	targets       config.TargetSelectors
	concurrency   int
	activeModules map[string]bool
	classifier    *BotClassifier

	userAccessAuditor    UserAccessModuleAuditor
	protectedBranchAud   ProtectedBranchesModuleAuditor
	protectedEnvAud      ProtectedEnvironmentsModuleAuditor
	pipelineRetentionAud PipelineRetentionModuleAuditor

	licenseKey  string
	licenseFile string
	dryRun      bool
}

// AuditorOption provides functional configuration for Auditor.
type AuditorOption func(*Auditor)

// WithAuditorLicense configures commercial license enforcement settings for the audit run.
func WithAuditorLicense(key, file string, dryRun bool) AuditorOption {
	return func(a *Auditor) {
		a.licenseKey = key
		a.licenseFile = file
		a.dryRun = dryRun
	}
}

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
	pipelineRetention ...PipelineRetentionModuleAuditor,
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
		if len(pipelineRetention) > 0 && pipelineRetention[0] != nil {
			a.pipelineRetentionAud = pipelineRetention[0]
		}
	}
}

// WithPipelineRetentionAuditor allows injecting a custom pipeline retention auditor.
func WithPipelineRetentionAuditor(aud PipelineRetentionModuleAuditor) AuditorOption {
	return func(a *Auditor) {
		if aud != nil {
			a.pipelineRetentionAud = aud
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

	if a.pipelineRetentionAud == nil {
		a.pipelineRetentionAud = NewPipelineRetentionAuditor()
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

	// 2. License Enforcement Phase (BSL 1.1)
	var targetPaths []string
	for _, p := range projects {
		targetPaths = append(targetPaths, p.PathWithNamespace)
	}
	baseURL := ""
	var serverTime time.Time
	if a.client != nil {
		baseURL = a.client.BaseURL()
		serverTime = a.client.ServerTime()
	}
	licStatus, err := license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: len(projects),
		IsDryRun:           a.dryRun,
		GitLabBaseURL:      baseURL,
		GitLabServerTime:   serverTime,
		TargetPaths:        targetPaths,
		LicenseKey:         a.licenseKey,
		LicenseFile:        a.licenseFile,
		Command:            "audit",
	})
	if err != nil {
		return nil, err
	}

	licenseAttestation := a.buildLicenseAttestation(licStatus, len(projects), serverTime)

	activeCount := 0
	archivedCount := 0
	for _, p := range projects {
		if p.Archived {
			archivedCount++
		} else {
			activeCount++
		}
	}

	// Determine authenticated token user identity
	var authUser *UserInfo
	var auditedBy string
	if a.client != nil && a.client.Users() != nil {
		currentUser, _, err := a.client.Users().CurrentUser(gitlab.WithContext(ctx))
		if err != nil {
			slog.Debug("Unable to retrieve authenticated token user", "error", err)
		} else if currentUser != nil {
			isBot := false
			acctType := "Human"
			if a.classifier != nil && a.classifier.IsBot(currentUser.ID, currentUser.Username, currentUser.Name, currentUser.Email) {
				isBot = true
				acctType = "Service Account / Bot"
			}
			authUser = &UserInfo{
				ID:          currentUser.ID,
				Username:    currentUser.Username,
				Name:        currentUser.Name,
				Email:       currentUser.Email,
				State:       currentUser.State,
				WebURL:      currentUser.WebURL,
				IsBot:       isBot,
				AccountType: acctType,
			}
			auditedBy = fmt.Sprintf("@%s", currentUser.Username)
			if currentUser.Name != "" && currentUser.Name != currentUser.Username {
				auditedBy += fmt.Sprintf(" (%s)", currentUser.Name)
			}
			if currentUser.Email != "" {
				auditedBy += fmt.Sprintf(" <%s>", currentUser.Email)
			}
			if currentUser.ID > 0 {
				auditedBy += fmt.Sprintf(" [ID: %d]", currentUser.ID)
			}
			slog.Info("Audit running under authenticated identity",
				"identity", auditedBy,
				"username", currentUser.Username,
				"id", currentUser.ID,
			)
		}
	}

	vInfo := version.Get()
	report := &AuditReport{
		Title:              "GitLab Fleet Compliance & Security Audit Report",
		GovernorVersion:    vInfo.Version,
		GeneratedAt:        startTime,
		AuthenticatedUser:  authUser,
		ActiveModules:      a.activeModuleList(),
		LicenseAttestation: licenseAttestation,
		Summary: SummaryMetrics{
			TotalProjectsScanned:  len(projects),
			ActiveProjectsCount:   activeCount,
			ArchivedProjectsCount: archivedCount,
			AuditedBy:             auditedBy,
		},
		UserAccessFindings:        make([]UserAccessFinding, 0),
		BotAccessFindings:         make([]UserAccessFinding, 0),
		ProtectedBranchFindings:   make([]ProtectedBranchFinding, 0),
		ProtectedEnvFindings:      make([]ProtectedEnvironmentFinding, 0),
		PipelineRetentionFindings: make([]PipelineRetentionFinding, 0),
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

			uFindings, bFindings, eFindings, rFindings, err := a.auditSingleProject(ctx, p)
			if err != nil {
				slog.Warn("Encountered warning or error auditing project", "project", p.PathWithNamespace, "error", err)
				mu.Lock()
				auditErrors = append(auditErrors, fmt.Errorf("project %s: %w", p.PathWithNamespace, err))
				mu.Unlock()
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
			if len(rFindings) > 0 {
				report.PipelineRetentionFindings = append(report.PipelineRetentionFindings, rFindings...)
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
	[]PipelineRetentionFinding,
	error,
) {
	var uFindings []UserAccessFinding
	var bFindings []ProtectedBranchFinding
	var eFindings []ProtectedEnvironmentFinding
	var rFindings []PipelineRetentionFinding
	var errs []error

	// User Access Module
	if a.isModuleActive(string(ModuleUserAccess)) && a.userAccessAuditor != nil {
		findings, err := a.userAccessAuditor.AuditProject(ctx, a.client, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("user_access: %w", err))
		} else {
			uFindings = findings
		}
	}

	// Protected Branches Module
	if a.isModuleActive(string(ModuleProtectedBranches)) && a.protectedBranchAud != nil {
		findings, err := a.protectedBranchAud.AuditProject(ctx, a.client, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("protected_branches: %w", err))
		} else {
			bFindings = findings
		}
	}

	// Protected Environments Module
	if a.isModuleActive(string(ModuleProtectedEnvironments)) && a.protectedEnvAud != nil {
		findings, err := a.protectedEnvAud.AuditProject(ctx, a.client, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("protected_environments: %w", err))
		} else {
			eFindings = findings
		}
	}

	// Pipeline Retention Module
	if a.isModuleActive(string(ModulePipelineRetention)) && a.pipelineRetentionAud != nil {
		findings, err := a.pipelineRetentionAud.AuditProject(ctx, a.client, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("pipeline_retention: %w", err))
		} else {
			rFindings = findings
		}
	}

	var combinedErr error
	if len(errs) > 0 {
		combinedErr = errors.Join(errs...)
	}
	return uFindings, bFindings, eFindings, rFindings, combinedErr
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

func (a *Auditor) buildLicenseAttestation(status *license.ValidationStatus, projectCount int, serverTime time.Time) *LicenseAttestation {
	vInfo := version.Get()
	changeDateStr := "Unknown"
	if cd, ok := vInfo.ChangeDate(); ok {
		changeDateStr = cd.Format("2006-01-02")
	}

	evalTime := time.Now().UTC()
	if !serverTime.IsZero() {
		evalTime = serverTime.UTC()
	}

	if vInfo.IsApacheConverted(evalTime) {
		return &LicenseAttestation{
			GovernorVersion:      vInfo.Version,
			Status:               "APACHE_2_CONVERTED",
			LicenseModel:         "Apache-2.0",
			Tier:                 "OPEN-SOURCE",
			LicensedTo:           "Open Source Commons",
			MaxProjects:          0,
			DiscoveredProjects:   projectCount,
			ChangeDate:           changeDateStr,
			AttestationStatement: fmt.Sprintf("Governed under the Apache License, Version 2.0 (converted on %s pursuant to BSL 1.1 Change Date terms). Unrestricted enterprise production governance permitted.", changeDateStr),
		}
	}

	if status != nil && status.Claims != nil {
		statusStr := "VALID_COMMERCIAL"
		if status.InGracePeriod {
			statusStr = "OPERATING_IN_GRACE_PERIOD"
		}
		return &LicenseAttestation{
			GovernorVersion:      vInfo.Version,
			Status:               statusStr,
			LicenseModel:         "BSL-1.1",
			Tier:                 strings.ToUpper(status.Claims.Tier),
			LicensedTo:           status.Claims.Customer.Name,
			LicenseID:            status.Claims.ID,
			MaxProjects:          status.Claims.MaxProjects,
			DiscoveredProjects:   projectCount,
			ChangeDate:           changeDateStr,
			AttestationStatement: fmt.Sprintf("Certified commercial governance under Business Source License 1.1. Licensed to %s (%s Tier, Capacity: %d projects, License ID: %s). Cryptographically attested via Ed25519 asymmetric signature.", status.Claims.Customer.Name, strings.ToUpper(status.Claims.Tier), status.Claims.MaxProjects, status.Claims.ID),
		}
	}

	if a.dryRun {
		return &LicenseAttestation{
			GovernorVersion:      vInfo.Version,
			Status:               "EXEMPTED_DRY_RUN",
			LicenseModel:         "BSL-1.1",
			Tier:                 "SIMULATION",
			LicensedTo:           "Non-Production / Dry-Run Simulation",
			MaxProjects:          0,
			DiscoveredProjects:   projectCount,
			ChangeDate:           changeDateStr,
			AttestationStatement: "Audit execution performed in non-destructive dry-run simulation mode, permitted free of charge under Business Source License 1.1 Additional Use Grant (a).",
		}
	}

	return &LicenseAttestation{
		GovernorVersion:      vInfo.Version,
		Status:               "COMMUNITY_TIER",
		LicenseModel:         "BSL-1.1",
		Tier:                 "COMMUNITY",
		LicensedTo:           "Community Tier (Unlicensed)",
		LicenseID:            "community",
		MaxProjects:          license.FreeTierMaxProjects,
		DiscoveredProjects:   projectCount,
		ChangeDate:           changeDateStr,
		AttestationStatement: fmt.Sprintf("Governed under Business Source License 1.1 Free Community Tier (governing %d/%d production projects). Unrestricted for fleets up to %d projects.", projectCount, license.FreeTierMaxProjects, license.FreeTierMaxProjects),
	}
}
