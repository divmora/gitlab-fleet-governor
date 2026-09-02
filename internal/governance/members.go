package governance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ViolationSeverity classifies member audit findings.
type ViolationSeverity string

const (
	SeverityCritical ViolationSeverity = "CRITICAL"
	SeverityHigh     ViolationSeverity = "HIGH"
	SeverityMedium   ViolationSeverity = "MEDIUM"
	SeverityLow      ViolationSeverity = "LOW"
)

// MemberViolation represents an audit finding for an individual user/member.
type MemberViolation struct {
	Username      string            `json:"username"`
	AccessLevel   int               `json:"access_level"`
	IsDirect      bool              `json:"is_direct"`
	Severity      ViolationSeverity `json:"severity"`
	ViolationType string            `json:"violation_type"`
	Message       string            `json:"message"`
}

// MembersReconciler audits project and group memberships against governance policies.
type MembersReconciler struct{}

// NewMembersReconciler creates a new MembersReconciler instance.
func NewMembersReconciler() *MembersReconciler {
	return &MembersReconciler{}
}

// NewMembersOperation creates a MembersReconciler.
func NewMembersOperation() *MembersReconciler {
	return NewMembersReconciler()
}

// Name returns the operation identifier.
func (r *MembersReconciler) Name() string {
	return "members"
}

// Order returns the execution order sequence (100).
func (r *MembersReconciler) Order() int {
	return 100
}

// Plan executes member governance audits on the targeted project.
func (r *MembersReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.Members == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	violations, diffs, err := r.auditProjectMembers(ctx, client, cfg.Policies.Members, project)
	if err != nil {
		return nil, err
	}

	if len(violations) == 0 && len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionAudit, diffs), nil
}

// Apply applies member audit evaluation (non-destructive reporting mode).
func (r *MembersReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.Members == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	planRes, err := r.Plan(ctx, client, project, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !planRes.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, planRes.Action, StatusSuccess, planRes.Diffs, nil, start), nil
}

// PlanGroup executes member governance audits on the targeted group.
func (r *MembersReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.Members == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	violations, diffs, err := r.auditGroupMembers(ctx, client, cfg.Policies.Members, group)
	if err != nil {
		return nil, err
	}

	if len(violations) == 0 && len(diffs) == 0 {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionAudit, diffs), nil
}

// ApplyGroup applies member audit evaluation on groups.
func (r *MembersReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.Members == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	planRes, err := r.PlanGroup(ctx, client, group, cfg)
	if err != nil {
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionNoop, StatusFailed, nil, err, start), err
	}

	if !planRes.HasChanges {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, planRes.Action, StatusSuccess, planRes.Diffs, nil, start), nil
}

// ============================================================================
// Internal Helpers & Diff Computations
// ============================================================================

func (r *MembersReconciler) auditProjectMembers(ctx context.Context, client gitlab.GitLabClient, policy *config.MembersConfig, project *gogitlab.Project) ([]MemberViolation, []Diff, error) {
	var violations []MemberViolation
	var diffs []Diff

	// Direct members
	directMembers, _, err := client.Members().ListProjectMembers(project.ID, &gogitlab.ListProjectMembersOptions{}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list direct project members for %d: %w", project.ID, err)
	}

	// All members (direct + inherited)
	allMembers, _, err := client.Members().ListAllProjectMembers(project.ID, &gogitlab.ListProjectMembersOptions{}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list all project members for %d: %w", project.ID, err)
	}

	directMap := make(map[int]*gogitlab.ProjectMember)
	for _, m := range directMembers {
		if m != nil {
			directMap[m.ID] = m
		}
	}

	deniedSet := make(map[string]struct{})
	for _, u := range policy.DeniedMembers {
		deniedSet[strings.ToLower(u)] = struct{}{}
	}

	now := time.Now()

	for _, member := range allMembers {
		if member == nil {
			continue
		}

		isDirect := false
		if _, ok := directMap[member.ID]; ok {
			isDirect = true
		}

		uLower := strings.ToLower(member.Username)

		// 1. Denied Member Check
		if _, denied := deniedSet[uLower]; denied {
			v := MemberViolation{
				Username:      member.Username,
				AccessLevel:   int(member.AccessLevel),
				IsDirect:      isDirect,
				Severity:      SeverityCritical,
				ViolationType: "DENIED_MEMBER",
				Message:       fmt.Sprintf("User '%s' is present in denied_members policy", member.Username),
			}
			violations = append(violations, v)
			builder := NewDiffBuilder()
			builder.AddField("denied_member", member.Username, nil, ActionAudit)
			builder.SetDetails(v.Message)
			diffs = append(diffs, builder.Build(fmt.Sprintf("member:%s", member.Username), ActionAudit))
		}

		// 2. Max Access Level Check (Over-Privileged)
		if policy.MaxAccessLevel != nil && int(member.AccessLevel) > *policy.MaxAccessLevel {
			v := MemberViolation{
				Username:      member.Username,
				AccessLevel:   int(member.AccessLevel),
				IsDirect:      isDirect,
				Severity:      SeverityHigh,
				ViolationType: "OVER_PRIVILEGED",
				Message:       fmt.Sprintf("User '%s' has access level %d exceeding max %d", member.Username, member.AccessLevel, *policy.MaxAccessLevel),
			}
			violations = append(violations, v)
			builder := NewDiffBuilder()
			builder.AddField("access_level", int(member.AccessLevel), *policy.MaxAccessLevel, ActionAudit)
			builder.SetDetails(v.Message)
			diffs = append(diffs, builder.Build(fmt.Sprintf("member:%s", member.Username), ActionAudit))
		}

		// 3. Min Access Level Check
		if policy.MinAccessLevel != nil && int(member.AccessLevel) < *policy.MinAccessLevel {
			v := MemberViolation{
				Username:      member.Username,
				AccessLevel:   int(member.AccessLevel),
				IsDirect:      isDirect,
				Severity:      SeverityLow,
				ViolationType: "UNDER_PRIVILEGED",
				Message:       fmt.Sprintf("User '%s' has access level %d below min %d", member.Username, member.AccessLevel, *policy.MinAccessLevel),
			}
			violations = append(violations, v)
			builder := NewDiffBuilder()
			builder.AddField("access_level", int(member.AccessLevel), *policy.MinAccessLevel, ActionAudit)
			builder.SetDetails(v.Message)
			diffs = append(diffs, builder.Build(fmt.Sprintf("member:%s", member.Username), ActionAudit))
		}

		// 4. Direct Expiration Checks
		if isDirect {
			if policy.EnforceExpiresAt != nil && *policy.EnforceExpiresAt {
				if member.ExpiresAt == nil {
					v := MemberViolation{
						Username:      member.Username,
						AccessLevel:   int(member.AccessLevel),
						IsDirect:      true,
						Severity:      SeverityMedium,
						ViolationType: "MISSING_EXPIRATION",
						Message:       fmt.Sprintf("Direct member '%s' has no expiration date configured", member.Username),
					}
					violations = append(violations, v)
					builder := NewDiffBuilder()
					builder.AddField("expires_at", nil, "<configured_date>", ActionAudit)
					builder.SetDetails(v.Message)
					diffs = append(diffs, builder.Build(fmt.Sprintf("member:%s", member.Username), ActionAudit))
				}
			}

			if policy.MaxExpirationDays != nil && member.ExpiresAt != nil {
				maxAllowedDate := now.AddDate(0, 0, *policy.MaxExpirationDays)
				expTime := time.Time(*member.ExpiresAt)
				if expTime.After(maxAllowedDate) {
					v := MemberViolation{
						Username:      member.Username,
						AccessLevel:   int(member.AccessLevel),
						IsDirect:      true,
						Severity:      SeverityMedium,
						ViolationType: "EXCESSIVE_EXPIRATION",
						Message:       fmt.Sprintf("Direct member '%s' expiration %s exceeds max %d days", member.Username, expTime.Format("2006-01-02"), *policy.MaxExpirationDays),
					}
					violations = append(violations, v)
					builder := NewDiffBuilder()
					builder.AddField("expires_at", expTime.Format("2006-01-02"), maxAllowedDate.Format("2006-01-02"), ActionAudit)
					builder.SetDetails(v.Message)
					diffs = append(diffs, builder.Build(fmt.Sprintf("member:%s", member.Username), ActionAudit))
				}
			}
		}
	}

	return violations, diffs, nil
}

func (r *MembersReconciler) auditGroupMembers(ctx context.Context, client gitlab.GitLabClient, policy *config.MembersConfig, group *gogitlab.Group) ([]MemberViolation, []Diff, error) {
	var violations []MemberViolation
	var diffs []Diff

	allMembers, _, err := client.Members().ListAllGroupMembers(group.ID, &gogitlab.ListGroupMembersOptions{}, gogitlab.WithContext(ctx))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list all group members for %d: %w", group.ID, err)
	}

	deniedSet := make(map[string]struct{})
	for _, u := range policy.DeniedMembers {
		deniedSet[strings.ToLower(u)] = struct{}{}
	}

	for _, member := range allMembers {
		if member == nil {
			continue
		}
		uLower := strings.ToLower(member.Username)
		if _, denied := deniedSet[uLower]; denied {
			v := MemberViolation{
				Username:      member.Username,
				AccessLevel:   int(member.AccessLevel),
				IsDirect:      false,
				Severity:      SeverityCritical,
				ViolationType: "DENIED_MEMBER",
				Message:       fmt.Sprintf("User '%s' is present in denied_members policy", member.Username),
			}
			violations = append(violations, v)
			builder := NewDiffBuilder()
			builder.AddField("denied_member", member.Username, nil, ActionAudit)
			builder.SetDetails(v.Message)
			diffs = append(diffs, builder.Build(fmt.Sprintf("member:%s", member.Username), ActionAudit))
		}
	}

	return violations, diffs, nil
}
