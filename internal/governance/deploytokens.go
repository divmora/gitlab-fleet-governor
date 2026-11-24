package governance

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// DeployTokensReconciler checks every project and group deploy token
// against the policy's rules (allowed scopes, required and maximum expiry)
// and reports or revokes expired and non-compliant tokens. It never creates
// tokens: GitLab shows a token's secret only once.
type DeployTokensReconciler struct {
	now func() time.Time
}

// NewDeployTokensReconciler instantiates a new DeployTokensReconciler.
func NewDeployTokensReconciler() *DeployTokensReconciler {
	return &DeployTokensReconciler{now: time.Now}
}

// Name returns the operation identifier.
func (r *DeployTokensReconciler) Name() string {
	return "deploy_tokens"
}

// Order returns the execution order sequence (66), next to deploy keys.
func (r *DeployTokensReconciler) Order() int {
	return 66
}

// tokenOwner abstracts the project and group deploy token APIs.
type tokenOwner struct {
	kind   ResourceType
	id     int64
	path   string
	list   func(page int64) ([]*gogitlab.DeployToken, *gogitlab.Response, error)
	revoke func(tokenID int64) error
}

func projectTokenOwner(ctx context.Context, client gitlab.GitLabClient, p *gogitlab.Project) tokenOwner {
	opt := gogitlab.WithContext(ctx)
	return tokenOwner{
		kind: ResourceTypeProject, id: p.ID, path: p.PathWithNamespace,
		list: func(page int64) ([]*gogitlab.DeployToken, *gogitlab.Response, error) {
			return client.DeployTokens().ListProjectDeployTokens(p.ID, &gogitlab.ListProjectDeployTokensOptions{ListOptions: gogitlab.ListOptions{PerPage: 100, Page: page}}, opt)
		},
		revoke: func(id int64) error {
			_, err := client.DeployTokens().DeleteProjectDeployToken(p.ID, id, opt)
			return err
		},
	}
}

func groupTokenOwner(ctx context.Context, client gitlab.GitLabClient, g *gogitlab.Group) tokenOwner {
	opt := gogitlab.WithContext(ctx)
	return tokenOwner{
		kind: ResourceTypeGroup, id: g.ID, path: g.FullPath,
		list: func(page int64) ([]*gogitlab.DeployToken, *gogitlab.Response, error) {
			return client.DeployTokens().ListGroupDeployTokens(g.ID, &gogitlab.ListGroupDeployTokensOptions{ListOptions: gogitlab.ListOptions{PerPage: 100, Page: page}}, opt)
		},
		revoke: func(id int64) error {
			_, err := client.DeployTokens().DeleteGroupDeployToken(g.ID, id, opt)
			return err
		},
	}
}

// Plan reports expired and non-compliant project deploy tokens.
func (r *DeployTokensReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	return r.planOwner(projectTokenOwner(ctx, client, project), cfg)
}

// Apply revokes the project deploy tokens the policy says to revoke.
func (r *DeployTokensReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return r.applyOwner(projectTokenOwner(ctx, client, project), cfg)
}

// PlanGroup reports expired and non-compliant group deploy tokens.
func (r *DeployTokensReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	return r.planOwner(groupTokenOwner(ctx, client, group), cfg)
}

// ApplyGroup revokes the group deploy tokens the policy says to revoke.
func (r *DeployTokensReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	return r.applyOwner(groupTokenOwner(ctx, client, group), cfg)
}

func (r *DeployTokensReconciler) planOwner(o tokenOwner, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.DeployTokens == nil {
		return NewNoopPlanResult(r.Name(), o.kind, o.id, o.path), nil
	}
	diffs, revoke, err := r.evaluate(o, cfg.Policies.DeployTokens)
	if err != nil {
		return nil, err
	}
	if len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), o.kind, o.id, o.path), nil
	}
	action := ActionAudit
	if len(revoke) > 0 {
		action = ActionDelete
	}
	return NewPlanResult(r.Name(), o.kind, o.id, o.path, action, diffs), nil
}

func (r *DeployTokensReconciler) applyOwner(o tokenOwner, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.DeployTokens == nil {
		return NewNoopApplyResult(r.Name(), o.kind, o.id, o.path), nil
	}
	diffs, revoke, err := r.evaluate(o, cfg.Policies.DeployTokens)
	if err != nil {
		return NewApplyResult(r.Name(), o.kind, o.id, o.path, ActionNoop, StatusFailed, nil, err, start), err
	}
	if len(diffs) == 0 {
		return NewNoopApplyResult(r.Name(), o.kind, o.id, o.path), nil
	}
	for _, t := range revoke {
		if err := o.revoke(t.ID); err != nil {
			applyErr := fmt.Errorf("failed to revoke deploy token %q (%d) on %s %d: %w", t.Name, t.ID, o.kind, o.id, err)
			return NewApplyResult(r.Name(), o.kind, o.id, o.path, ActionDelete, StatusFailed, diffs, applyErr, start), applyErr
		}
	}
	action := ActionAudit
	if len(revoke) > 0 {
		action = ActionDelete
	}
	return NewApplyResult(r.Name(), o.kind, o.id, o.path, action, StatusSuccess, diffs, nil, start), nil
}

// evaluate returns one diff per expired or non-compliant token, and the
// tokens to revoke.
func (r *DeployTokensReconciler) evaluate(o tokenOwner, d *config.DeployTokensConfig) ([]Diff, []*gogitlab.DeployToken, error) {
	tokens, err := listAll(o.list)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list deploy tokens for %s %d: %w", o.kind, o.id, err)
	}
	now := r.now()
	allowed := map[string]bool{}
	for _, s := range d.AllowedScopes {
		allowed[s] = true
	}
	revokeExpired := d.RevokeExpired != nil && *d.RevokeExpired
	revokeBad := d.RevokeNoncompliant != nil && *d.RevokeNoncompliant

	var diffs []Diff
	var revoke []*gogitlab.DeployToken
	for _, t := range tokens {
		if t == nil || t.Revoked {
			continue
		}
		resource := fmt.Sprintf("deploy_token:%s", t.Name)
		expired := t.Expired || (t.ExpiresAt != nil && !t.ExpiresAt.After(now))
		if expired {
			b := NewDiffBuilder()
			action := ActionAudit
			if revokeExpired {
				action = ActionDelete
				revoke = append(revoke, t)
			}
			expiredOn := "unknown date"
			if t.ExpiresAt != nil {
				expiredOn = t.ExpiresAt.Format(time.DateOnly)
			}
			b.AddField("expired", expiredOn, nil, action)
			b.SetDetails("deploy token has expired")
			diffs = append(diffs, b.Build(resource, action))
			continue
		}

		var problems []string
		if len(allowed) > 0 {
			var extra []string
			for _, s := range t.Scopes {
				if !allowed[s] {
					extra = append(extra, s)
				}
			}
			sort.Strings(extra)
			if len(extra) > 0 {
				problems = append(problems, "scopes not allowed: "+strings.Join(extra, ", "))
			}
		}
		if t.ExpiresAt == nil {
			if d.RequireExpiresAt != nil && *d.RequireExpiresAt {
				problems = append(problems, "no expiry date")
			}
		} else if d.MaxExpirationDays != nil {
			if limit := now.AddDate(0, 0, *d.MaxExpirationDays); t.ExpiresAt.After(limit) {
				problems = append(problems, fmt.Sprintf("expires %s, more than %d days from now", t.ExpiresAt.Format(time.DateOnly), *d.MaxExpirationDays))
			}
		}
		if len(problems) == 0 {
			continue
		}
		b := NewDiffBuilder()
		action := ActionAudit
		if revokeBad {
			action = ActionDelete
			revoke = append(revoke, t)
		}
		b.AddField("non_compliant", strings.Join(problems, "; "), nil, action)
		b.SetDetails(strings.Join(problems, "; "))
		diffs = append(diffs, b.Build(resource, action))
	}
	return diffs, revoke, nil
}
