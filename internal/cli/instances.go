package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// selectInstance returns the policy for the instance a single-instance
// command (export, merge) should use. A multi-instance policy needs
// --instance; a settings.gitlab policy must not be given one.
func selectInstance(cfg *config.PolicyConfig, name string) (*config.PolicyConfig, error) {
	name = strings.TrimSpace(name)
	if !cfg.IsMultiInstance() {
		if name != "" {
			return nil, fmt.Errorf("--instance %q: the policy has no instances block", name)
		}
		return cfg, nil
	}
	runs, err := cfg.InstanceRuns()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(runs))
	for _, run := range runs {
		if run.Name == name {
			return run.Config, nil
		}
		names = append(names, run.Name)
	}
	if name == "" {
		return nil, fmt.Errorf("the policy has several GitLab instances (%s); choose one with --instance", strings.Join(names, ", "))
	}
	return nil, fmt.Errorf("--instance %q: no such instance (have %s)", name, strings.Join(names, ", "))
}

// auditInstances audits each instance in turn and merges the reports. An
// instance that fails is recorded in the report and the rest still run. The
// returned error names the failed instances; the report is nil only when
// every instance failed.
func auditInstances(ctx context.Context, runs []config.InstanceRun, newAuditor func(*config.PolicyConfig, int) (*audit.Auditor, error)) (*audit.AuditReport, error) {
	started := time.Now()
	results := make([]audit.InstanceResult, 0, len(runs))
	var errs []error
	prior := 0
	for _, run := range runs {
		res := audit.InstanceResult{Name: run.Name, BaseURL: run.Config.Settings.GitLab.BaseURL}
		auditor, err := newAuditor(run.Config, prior)
		if err == nil {
			res.Report, err = auditor.Execute(ctx)
		}
		if err != nil {
			res.Err = err
			errs = append(errs, fmt.Errorf("instance %q: %w", run.Name, err))
			slog.Error("Audit of GitLab instance failed", "instance", run.Name, "error", err)
		} else {
			prior += res.Report.Summary.TotalProjectsScanned
		}
		results = append(results, res)
	}
	if len(errs) == len(runs) {
		return nil, fmt.Errorf("audit execution failed: %w", errors.Join(errs...))
	}
	merged := audit.MergeReports(results, started)
	if len(errs) > 0 {
		return merged, fmt.Errorf("audit failed on %d of %d instances: %w", len(errs), len(runs), errors.Join(errs...))
	}
	return merged, nil
}
