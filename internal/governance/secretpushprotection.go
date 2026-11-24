package governance

import (
	"context"
	"fmt"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// SecretPushProtectionReconciler turns GitLab secret push protection on or
// off for each project (GitLab Ultimate).
type SecretPushProtectionReconciler struct{}

// NewSecretPushProtectionReconciler instantiates a new SecretPushProtectionReconciler.
func NewSecretPushProtectionReconciler() *SecretPushProtectionReconciler {
	return &SecretPushProtectionReconciler{}
}

// Name returns the operation identifier.
func (r *SecretPushProtectionReconciler) Name() string {
	return "secret_push_protection"
}

// Order returns the execution order sequence (12), next to push rules.
func (r *SecretPushProtectionReconciler) Order() int {
	return 12
}

// Plan compares the desired setting with the project's.
func (r *SecretPushProtectionReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	want := r.desired(cfg)
	if want == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	diff, err := r.diff(ctx, client, project, *want)
	if err != nil {
		return nil, err
	}
	if !diff.HasChanges() {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, []Diff{diff}), nil
}

// Apply updates the setting when it differs.
func (r *SecretPushProtectionReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	want := r.desired(cfg)
	if want == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	diff, err := r.diff(ctx, client, project, *want)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}
	if !diff.HasChanges() {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	opts := gogitlab.UpdateProjectSecuritySettingsOptions{SecretPushProtectionEnabled: gogitlab.Ptr(*want)}
	if _, _, err := client.SecuritySettings().UpdateSecretPushProtectionEnabledSetting(project.ID, opts, gogitlab.WithContext(ctx)); err != nil {
		applyErr := fmt.Errorf("failed to update secret push protection for project %d: %w", project.ID, err)
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, []Diff{diff}, applyErr, start), applyErr
	}
	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, []Diff{diff}, nil, start), nil
}

// PlanGroup returns ActionSkipped: this reconciler manages the project setting.
func (r *SecretPushProtectionReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Secret push protection is managed per project"), nil
}

// ApplyGroup returns ActionSkipped: this reconciler manages the project setting.
func (r *SecretPushProtectionReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Secret push protection is managed per project"), nil
}

func (r *SecretPushProtectionReconciler) desired(cfg *config.PolicyConfig) *bool {
	if cfg == nil || cfg.Policies.SecretPushProtection == nil {
		return nil
	}
	return cfg.Policies.SecretPushProtection.Enabled
}

func (r *SecretPushProtectionReconciler) diff(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, want bool) (Diff, error) {
	live, _, err := client.SecuritySettings().ListProjectSecuritySettings(project.ID, gogitlab.WithContext(ctx))
	if err != nil {
		return Diff{}, fmt.Errorf("failed to get security settings for project %d: %w", project.ID, err)
	}
	b := NewDiffBuilder()
	b.Add(CompareBoolPtr("secret_push_protection_enabled", live.SecretPushProtectionEnabled, &want))
	return b.Build("secret_push_protection", ActionUpdate), nil
}
