package audit

import "time"

// InstanceResult is the audit of one GitLab instance in a multi-instance run.
type InstanceResult struct {
	Name    string
	BaseURL string
	Report  *AuditReport // nil when the audit of this instance failed
	Err     error
}

// tagInstance records the instance name on every finding and user.
func (r *AuditReport) tagInstance(name string) {
	if name == "" {
		return
	}
	for i := range r.UserAccessFindings {
		r.UserAccessFindings[i].Instance = name
	}
	for i := range r.BotAccessFindings {
		r.BotAccessFindings[i].Instance = name
	}
	for i := range r.ProtectedBranchFindings {
		r.ProtectedBranchFindings[i].Instance = name
	}
	for i := range r.ProtectedEnvFindings {
		r.ProtectedEnvFindings[i].Instance = name
	}
	for i := range r.PipelineRetentionFindings {
		r.PipelineRetentionFindings[i].Instance = name
	}
	for i := range r.RepositoryFileFindings {
		r.RepositoryFileFindings[i].Instance = name
	}
	for i := range r.ProtectedTagFindings {
		r.ProtectedTagFindings[i].Instance = name
	}
	for i, u := range r.UserDirectory {
		if u == nil {
			continue
		}
		cp := *u
		cp.Instance = name
		r.UserDirectory[i] = &cp
	}
	if r.AuthenticatedUser != nil {
		cp := *r.AuthenticatedUser
		cp.Instance = name
		r.AuthenticatedUser = &cp
	}
}

// MergeReports combines per-instance audits into one report. Findings keep
// their instance tag and stay grouped by instance in the order given. The
// licence attestation is taken from the last successful instance, whose
// project count already includes the earlier instances.
func MergeReports(results []InstanceResult, startedAt time.Time) *AuditReport {
	merged := &AuditReport{
		Title:                     "GitLab Fleet Compliance & Security Audit Report",
		GeneratedAt:               startedAt,
		UserAccessFindings:        make([]UserAccessFinding, 0),
		BotAccessFindings:         make([]UserAccessFinding, 0),
		ProtectedBranchFindings:   make([]ProtectedBranchFinding, 0),
		ProtectedEnvFindings:      make([]ProtectedEnvironmentFinding, 0),
		PipelineRetentionFindings: make([]PipelineRetentionFinding, 0),
		RepositoryFileFindings:    make([]RepositoryFileFinding, 0),
		ProtectedTagFindings:      make([]ProtectedTagFinding, 0),
		Instances:                 make([]InstanceAudit, 0, len(results)),
	}

	seenModules := map[string]bool{}
	var auditedBy []string
	for _, res := range results {
		ia := InstanceAudit{Name: res.Name, BaseURL: res.BaseURL}
		if res.Err != nil {
			ia.Error = res.Err.Error()
		}
		r := res.Report
		if r == nil {
			merged.Instances = append(merged.Instances, ia)
			continue
		}
		ia.AuthenticatedUser = r.AuthenticatedUser
		ia.ProjectsScanned = r.Summary.TotalProjectsScanned
		ia.Violations = r.Summary.TotalViolations
		merged.Instances = append(merged.Instances, ia)

		if merged.GovernorVersion == "" {
			merged.GovernorVersion = r.GovernorVersion
		}
		for _, m := range r.ActiveModules {
			if !seenModules[m] {
				seenModules[m] = true
				merged.ActiveModules = append(merged.ActiveModules, m)
			}
		}
		if r.LicenseAttestation != nil {
			merged.LicenseAttestation = r.LicenseAttestation
		}
		if r.Summary.AuditedBy != "" {
			auditedBy = append(auditedBy, Label(res.Name, r.Summary.AuditedBy))
		}

		merged.Summary.TotalProjectsScanned += r.Summary.TotalProjectsScanned
		merged.Summary.ActiveProjectsCount += r.Summary.ActiveProjectsCount
		merged.Summary.ArchivedProjectsCount += r.Summary.ArchivedProjectsCount
		merged.Summary.PendingDeletionProjectsCount += r.Summary.PendingDeletionProjectsCount

		merged.UserAccessFindings = append(merged.UserAccessFindings, r.UserAccessFindings...)
		merged.BotAccessFindings = append(merged.BotAccessFindings, r.BotAccessFindings...)
		merged.ProtectedBranchFindings = append(merged.ProtectedBranchFindings, r.ProtectedBranchFindings...)
		merged.ProtectedEnvFindings = append(merged.ProtectedEnvFindings, r.ProtectedEnvFindings...)
		merged.PipelineRetentionFindings = append(merged.PipelineRetentionFindings, r.PipelineRetentionFindings...)
		merged.RepositoryFileFindings = append(merged.RepositoryFileFindings, r.RepositoryFileFindings...)
		merged.ProtectedTagFindings = append(merged.ProtectedTagFindings, r.ProtectedTagFindings...)
		merged.UserDirectory = append(merged.UserDirectory, r.UserDirectory...)
	}

	for i, a := range auditedBy {
		if i > 0 {
			merged.Summary.AuditedBy += "; "
		}
		merged.Summary.AuditedBy += a
	}

	merged.ComputeSummary()
	merged.Duration = time.Since(startedAt)
	merged.DurationString = merged.Duration.Round(time.Millisecond).String()
	return merged
}
