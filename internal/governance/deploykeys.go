package governance

import (
	"context"
	"fmt"
	"strings"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// DeployKeysReconciler governs the deploy keys enabled on each project:
// managed keys are added (when their public key is given) and kept at the
// declared access, unlisted keys can be made read-only or removed, and old
// keys are reported for rotation.
type DeployKeysReconciler struct {
	now func() time.Time
}

// NewDeployKeysReconciler instantiates a new DeployKeysReconciler.
func NewDeployKeysReconciler() *DeployKeysReconciler {
	return &DeployKeysReconciler{now: time.Now}
}

// Name returns the operation identifier.
func (r *DeployKeysReconciler) Name() string {
	return "deploy_keys"
}

// Order returns the execution order sequence (64), next to variables.
func (r *DeployKeysReconciler) Order() int {
	return 64
}

type deployKeyStep struct {
	action ActionType
	title  string
	add    *gogitlab.AddDeployKeyOptions
	keyID  int64
	push   *bool
}

// Plan reports the changes and findings for the project's deploy keys.
func (r *DeployKeysReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.DeployKeys == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	diffs, steps, err := r.plan(ctx, client, project, cfg.Policies.DeployKeys)
	if err != nil {
		return nil, err
	}
	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, planAction(steps), diffs), nil
}

// Apply carries out the planned changes. Findings (missing keys without a
// public key, old keys) are reported but need a person to act.
func (r *DeployKeysReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.DeployKeys == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}
	diffs, steps, err := r.plan(ctx, client, project, cfg.Policies.DeployKeys)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}
	if len(diffs) == 0 {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	svc := client.DeployKeys()
	opt := gogitlab.WithContext(ctx)
	for _, s := range steps {
		var stepErr error
		switch s.action {
		case ActionCreate:
			_, _, stepErr = svc.AddDeployKey(project.ID, s.add, opt)
		case ActionUpdate:
			_, _, stepErr = svc.UpdateDeployKey(project.ID, s.keyID, &gogitlab.UpdateDeployKeyOptions{CanPush: s.push}, opt)
		case ActionDelete:
			_, stepErr = svc.DeleteDeployKey(project.ID, s.keyID, opt)
		}
		if stepErr != nil {
			applyErr := fmt.Errorf("failed to %s deploy key %q on project %d: %w", strings.ToLower(string(s.action)), s.title, project.ID, stepErr)
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, s.action, StatusFailed, diffs, applyErr, start), applyErr
		}
	}
	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, planAction(steps), StatusSuccess, diffs, nil, start), nil
}

// PlanGroup returns ActionSkipped: deploy keys are enabled per project.
func (r *DeployKeysReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return NewSkippedPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Deploy keys are managed per project"), nil
}

// ApplyGroup returns ActionSkipped: deploy keys are enabled per project.
func (r *DeployKeysReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return NewSkippedApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, "Deploy keys are managed per project"), nil
}

func (r *DeployKeysReconciler) plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, d *config.DeployKeysConfig) ([]Diff, []deployKeyStep, error) {
	live, err := listAll(func(page int64) ([]*gogitlab.ProjectDeployKey, *gogitlab.Response, error) {
		return client.DeployKeys().ListProjectDeployKeys(project.ID, &gogitlab.ListProjectDeployKeysOptions{ListOptions: gogitlab.ListOptions{PerPage: 100, Page: page}}, gogitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list deploy keys for project %d: %w", project.ID, err)
	}

	var diffs []Diff
	var steps []deployKeyStep
	matched := map[int64]bool{}

	for _, want := range d.Keys {
		wantPush := want.CanPush != nil && *want.CanPush
		key := findDeployKey(live, want)
		resource := "deploy_key:" + want.Title
		if key == nil {
			b := NewDiffBuilder()
			if strings.TrimSpace(want.Key) == "" {
				b.AddField("missing", nil, want.Title, ActionAudit)
				b.SetDetails("deploy key is not enabled on this project; add its public key to the policy to have it added")
				diffs = append(diffs, b.Build(resource, ActionAudit))
				continue
			}
			b.AddField("can_push", nil, wantPush, ActionCreate)
			diffs = append(diffs, b.Build(resource, ActionCreate))
			steps = append(steps, deployKeyStep{action: ActionCreate, title: want.Title, add: &gogitlab.AddDeployKeyOptions{
				Title: gogitlab.Ptr(want.Title), Key: gogitlab.Ptr(strings.TrimSpace(want.Key)), CanPush: gogitlab.Ptr(wantPush),
			}})
			continue
		}
		matched[key.ID] = true
		if key.CanPush != wantPush {
			b := NewDiffBuilder()
			b.AddField("can_push", key.CanPush, wantPush, ActionUpdate)
			diffs = append(diffs, b.Build(resource, ActionUpdate))
			steps = append(steps, deployKeyStep{action: ActionUpdate, title: want.Title, keyID: key.ID, push: gogitlab.Ptr(wantPush)})
		}
		if f := r.ageFinding(key, d); f != nil {
			diffs = append(diffs, *f)
		}
	}

	prune := d.Prune != nil && *d.Prune
	denyPush := d.DenyPush != nil && *d.DenyPush
	for _, key := range live {
		if key == nil || matched[key.ID] {
			continue
		}
		resource := "deploy_key:" + key.Title
		switch {
		case prune:
			b := NewDiffBuilder()
			b.AddField("deploy_key", key.Title, nil, ActionDelete)
			b.SetDetails("deploy key is not in the policy")
			diffs = append(diffs, b.Build(resource, ActionDelete))
			steps = append(steps, deployKeyStep{action: ActionDelete, title: key.Title, keyID: key.ID})
			continue
		case denyPush && key.CanPush:
			b := NewDiffBuilder()
			b.AddField("can_push", true, false, ActionUpdate)
			b.SetDetails("unlisted deploy keys must be read-only")
			diffs = append(diffs, b.Build(resource, ActionUpdate))
			steps = append(steps, deployKeyStep{action: ActionUpdate, title: key.Title, keyID: key.ID, push: gogitlab.Ptr(false)})
		}
		if f := r.ageFinding(key, d); f != nil {
			diffs = append(diffs, *f)
		}
	}
	return diffs, steps, nil
}

func (r *DeployKeysReconciler) ageFinding(key *gogitlab.ProjectDeployKey, d *config.DeployKeysConfig) *Diff {
	if d.MaxAgeDays == nil || key.CreatedAt == nil {
		return nil
	}
	age := int(r.now().Sub(*key.CreatedAt).Hours() / 24)
	if age <= *d.MaxAgeDays {
		return nil
	}
	b := NewDiffBuilder()
	b.AddField("age_days", age, *d.MaxAgeDays, ActionAudit)
	b.SetDetails(fmt.Sprintf("deploy key is %d days old (limit %d); rotate it", age, *d.MaxAgeDays))
	diff := b.Build("deploy_key:"+key.Title, ActionAudit)
	return &diff
}

// findDeployKey matches a managed key by fingerprint when one is known,
// otherwise by title (case-insensitive).
func findDeployKey(live []*gogitlab.ProjectDeployKey, want config.DeployKeyConfig) *gogitlab.ProjectDeployKey {
	fp := strings.TrimRight(strings.TrimSpace(want.Fingerprint), "=")
	if fp == "" && want.Key != "" {
		fp, _ = config.SSHKeyFingerprint(want.Key)
	}
	for _, k := range live {
		if k == nil {
			continue
		}
		if fp != "" {
			if strings.TrimRight(k.FingerprintSHA256, "=") == fp {
				return k
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k.Title), strings.TrimSpace(want.Title)) {
			return k
		}
	}
	return nil
}

// planAction summarises the steps: Update when anything changes, Audit when
// there are only findings.
func planAction(steps []deployKeyStep) ActionType {
	if len(steps) == 0 {
		return ActionAudit
	}
	return ActionUpdate
}
