package governance

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// PushRulesReconciler implements GovernanceOperation for Project and Group push rules.
type PushRulesReconciler struct{}

// NewPushRulesReconciler instantiates a new push rules reconciler.
func NewPushRulesReconciler() *PushRulesReconciler {
	return &PushRulesReconciler{}
}

// NewPushRulesOperation creates a new push rules operation instance.
func NewPushRulesOperation() *PushRulesReconciler {
	return NewPushRulesReconciler()
}

// Name returns the canonical identifier.
func (r *PushRulesReconciler) Name() string {
	return "push_rules"
}

// Order returns the execution order sequence (10).
func (r *PushRulesReconciler) Order() int {
	return 10
}

// ============================================================================
// Project-Level Push Rules Reconciliation
// ============================================================================

// Plan evaluates project push rules against policy config (dry-run).
func (r *PushRulesReconciler) Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.PushRules == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	ruleCfg := cfg.Policies.PushRules
	liveRule, resp, err := client.PushRules().GetProjectPushRule(project.ID, gogitlab.WithContext(ctx))
	if err != nil && !isNotFound(err, resp) {
		return nil, fmt.Errorf("failed to fetch project push rules for project %d: %w", project.ID, err)
	}

	if isNotFound(err, resp) || liveRule == nil {
		// 404: Push rules not yet created -> ActionCreate
		diff := r.buildProjectCreateDiff(ruleCfg)
		return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, []Diff{diff}), nil
	}

	// 200: Push rules exist -> calculate update diff
	diff := r.buildProjectUpdateDiff(liveRule, ruleCfg)
	if !diff.HasChanges() {
		return NewNoopPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, []Diff{diff}), nil
}

// Apply executes project push rules reconciliation (live mutation).
func (r *PushRulesReconciler) Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.PushRules == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	ruleCfg := cfg.Policies.PushRules
	liveRule, resp, err := client.PushRules().GetProjectPushRule(project.ID, gogitlab.WithContext(ctx))
	if err != nil && !isNotFound(err, resp) {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionNoop, StatusFailed, nil, err, start), err
	}

	if isNotFound(err, resp) || liveRule == nil {
		// CREATE via AddProjectPushRule (POST)
		diff := r.buildProjectCreateDiff(ruleCfg)
		addOpt := r.toAddProjectOptions(ruleCfg)
		_, _, createErr := client.PushRules().AddProjectPushRule(project.ID, addOpt, gogitlab.WithContext(ctx))
		if createErr != nil {
			return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, StatusFailed, []Diff{diff}, createErr, start), createErr
		}
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionCreate, StatusSuccess, []Diff{diff}, nil, start), nil
	}

	// UPDATE via EditProjectPushRule (PUT)
	diff := r.buildProjectUpdateDiff(liveRule, ruleCfg)
	if !diff.HasChanges() {
		return NewNoopApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace), nil
	}

	editOpt := r.toEditProjectOptions(ruleCfg)
	_, _, editErr := client.PushRules().EditProjectPushRule(project.ID, editOpt, gogitlab.WithContext(ctx))
	if editErr != nil {
		return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusFailed, []Diff{diff}, editErr, start), editErr
	}

	return NewApplyResult(r.Name(), ResourceTypeProject, project.ID, project.PathWithNamespace, ActionUpdate, StatusSuccess, []Diff{diff}, nil, start), nil
}

// ============================================================================
// Group-Level Push Rules Reconciliation
// ============================================================================

// PlanGroup evaluates group push rules against policy config (dry-run).
func (r *PushRulesReconciler) PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error) {
	if cfg == nil || cfg.Policies.PushRules == nil {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	ruleCfg := cfg.Policies.PushRules
	liveRule, resp, err := client.PushRules().GetGroupPushRule(group.ID, gogitlab.WithContext(ctx))
	if err != nil && !isNotFound(err, resp) {
		return nil, fmt.Errorf("failed to fetch group push rules for group %d: %w", group.ID, err)
	}

	if isNotFound(err, resp) || liveRule == nil {
		diff := r.buildGroupCreateDiff(ruleCfg)
		return NewPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionCreate, []Diff{diff}), nil
	}

	diff := r.buildGroupUpdateDiff(liveRule, ruleCfg)
	if !diff.HasChanges() {
		return NewNoopPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	return NewPlanResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, []Diff{diff}), nil
}

// ApplyGroup executes group push rules reconciliation (live mutation).
func (r *PushRulesReconciler) ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error) {
	start := time.Now()
	if cfg == nil || cfg.Policies.PushRules == nil {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	ruleCfg := cfg.Policies.PushRules
	liveRule, resp, err := client.PushRules().GetGroupPushRule(group.ID, gogitlab.WithContext(ctx))
	if err != nil && !isNotFound(err, resp) {
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionNoop, StatusFailed, nil, err, start), err
	}

	if isNotFound(err, resp) || liveRule == nil {
		// CREATE via AddGroupPushRule (POST)
		diff := r.buildGroupCreateDiff(ruleCfg)
		addOpt := r.toAddGroupOptions(ruleCfg)
		_, _, createErr := client.PushRules().AddGroupPushRule(group.ID, addOpt, gogitlab.WithContext(ctx))
		if createErr != nil {
			return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionCreate, StatusFailed, []Diff{diff}, createErr, start), createErr
		}
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionCreate, StatusSuccess, []Diff{diff}, nil, start), nil
	}

	// UPDATE via EditGroupPushRule (PUT)
	diff := r.buildGroupUpdateDiff(liveRule, ruleCfg)
	if !diff.HasChanges() {
		return NewNoopApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath), nil
	}

	editOpt := r.toEditGroupOptions(ruleCfg)
	_, _, editErr := client.PushRules().EditGroupPushRule(group.ID, editOpt, gogitlab.WithContext(ctx))
	if editErr != nil {
		return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, StatusFailed, []Diff{diff}, editErr, start), editErr
	}

	return NewApplyResult(r.Name(), ResourceTypeGroup, group.ID, group.FullPath, ActionUpdate, StatusSuccess, []Diff{diff}, nil, start), nil
}

// ============================================================================
// Diff & Options Construction Helpers
// ============================================================================

func (r *PushRulesReconciler) buildProjectCreateDiff(cfg *config.PushRulesConfig) Diff {
	builder := NewDiffBuilder()
	if cfg.AuthorEmailRegex != "" {
		builder.AddField("author_email_regex", nil, cfg.AuthorEmailRegex, ActionCreate)
	}
	if cfg.BranchNameRegex != "" {
		builder.AddField("branch_name_regex", nil, cfg.BranchNameRegex, ActionCreate)
	}
	if cfg.CommitMessageRegex != "" {
		builder.AddField("commit_message_regex", nil, cfg.CommitMessageRegex, ActionCreate)
	}
	if cfg.CommitMessageNegativeRegex != "" {
		builder.AddField("commit_message_negative_regex", nil, cfg.CommitMessageNegativeRegex, ActionCreate)
	}
	if cfg.FileNameRegex != "" {
		builder.AddField("file_name_regex", nil, cfg.FileNameRegex, ActionCreate)
	}
	if cfg.MaxFileSize != nil {
		builder.AddField("max_file_size", nil, *cfg.MaxFileSize, ActionCreate)
	}
	if cfg.CommitCommitterCheck != nil {
		builder.AddField("commit_committer_check", nil, *cfg.CommitCommitterCheck, ActionCreate)
	}
	if cfg.MemberCheck != nil {
		builder.AddField("member_check", nil, *cfg.MemberCheck, ActionCreate)
	}
	if cfg.PreventSecrets != nil {
		builder.AddField("prevent_secrets", nil, *cfg.PreventSecrets, ActionCreate)
	}
	if cfg.DenyDeleteTag != nil {
		builder.AddField("deny_delete_tag", nil, *cfg.DenyDeleteTag, ActionCreate)
	}
	if cfg.RejectUnsignedCommits != nil {
		builder.AddField("reject_unsigned_commits", nil, *cfg.RejectUnsignedCommits, ActionCreate)
	}
	if cfg.RejectNonDCOCommits != nil {
		builder.AddField("reject_non_dco_commits", nil, *cfg.RejectNonDCOCommits, ActionCreate)
	}
	return builder.Build("push_rule", ActionCreate)
}

func (r *PushRulesReconciler) buildProjectUpdateDiff(live *gogitlab.ProjectPushRules, cfg *config.PushRulesConfig) Diff {
	builder := NewDiffBuilder()
	builder.Add(CompareString("author_email_regex", live.AuthorEmailRegex, cfg.AuthorEmailRegex))
	builder.Add(CompareString("branch_name_regex", live.BranchNameRegex, cfg.BranchNameRegex))
	builder.Add(CompareString("commit_message_regex", live.CommitMessageRegex, cfg.CommitMessageRegex))
	builder.Add(CompareString("commit_message_negative_regex", live.CommitMessageNegativeRegex, cfg.CommitMessageNegativeRegex))
	builder.Add(CompareString("file_name_regex", live.FileNameRegex, cfg.FileNameRegex))
	builder.Add(CompareIntPtr("max_file_size", live.MaxFileSize, cfg.MaxFileSize))
	builder.Add(CompareBoolPtr("commit_committer_check", live.CommitCommitterCheck, cfg.CommitCommitterCheck))
	builder.Add(CompareBoolPtr("member_check", live.MemberCheck, cfg.MemberCheck))
	builder.Add(CompareBoolPtr("prevent_secrets", live.PreventSecrets, cfg.PreventSecrets))
	builder.Add(CompareBoolPtr("deny_delete_tag", live.DenyDeleteTag, cfg.DenyDeleteTag))
	builder.Add(CompareBoolPtr("reject_unsigned_commits", live.RejectUnsignedCommits, cfg.RejectUnsignedCommits))
	builder.Add(CompareBoolPtr("reject_non_dco_commits", live.RejectNonDCOCommits, cfg.RejectNonDCOCommits))
	return builder.Build("push_rule", ActionUpdate)
}

func (r *PushRulesReconciler) buildGroupCreateDiff(cfg *config.PushRulesConfig) Diff {
	return r.buildProjectCreateDiff(cfg)
}

func (r *PushRulesReconciler) buildGroupUpdateDiff(live *gogitlab.GroupPushRules, cfg *config.PushRulesConfig) Diff {
	builder := NewDiffBuilder()
	builder.Add(CompareString("author_email_regex", live.AuthorEmailRegex, cfg.AuthorEmailRegex))
	builder.Add(CompareString("branch_name_regex", live.BranchNameRegex, cfg.BranchNameRegex))
	builder.Add(CompareString("commit_message_regex", live.CommitMessageRegex, cfg.CommitMessageRegex))
	builder.Add(CompareString("commit_message_negative_regex", live.CommitMessageNegativeRegex, cfg.CommitMessageNegativeRegex))
	builder.Add(CompareString("file_name_regex", live.FileNameRegex, cfg.FileNameRegex))
	builder.Add(CompareIntPtr("max_file_size", live.MaxFileSize, cfg.MaxFileSize))
	builder.Add(CompareBoolPtr("commit_committer_check", live.CommitCommitterCheck, cfg.CommitCommitterCheck))
	builder.Add(CompareBoolPtr("member_check", live.MemberCheck, cfg.MemberCheck))
	builder.Add(CompareBoolPtr("prevent_secrets", live.PreventSecrets, cfg.PreventSecrets))
	builder.Add(CompareBoolPtr("deny_delete_tag", live.DenyDeleteTag, cfg.DenyDeleteTag))
	builder.Add(CompareBoolPtr("reject_unsigned_commits", live.RejectUnsignedCommits, cfg.RejectUnsignedCommits))
	builder.Add(CompareBoolPtr("reject_non_dco_commits", live.RejectNonDCOCommits, cfg.RejectNonDCOCommits))
	return builder.Build("push_rule", ActionUpdate)
}

func (r *PushRulesReconciler) toAddProjectOptions(cfg *config.PushRulesConfig) *gogitlab.AddProjectPushRuleOptions {
	opt := &gogitlab.AddProjectPushRuleOptions{}
	if cfg.AuthorEmailRegex != "" {
		opt.AuthorEmailRegex = gogitlab.Ptr(cfg.AuthorEmailRegex)
	}
	if cfg.BranchNameRegex != "" {
		opt.BranchNameRegex = gogitlab.Ptr(cfg.BranchNameRegex)
	}
	if cfg.CommitMessageRegex != "" {
		opt.CommitMessageRegex = gogitlab.Ptr(cfg.CommitMessageRegex)
	}
	if cfg.CommitMessageNegativeRegex != "" {
		opt.CommitMessageNegativeRegex = gogitlab.Ptr(cfg.CommitMessageNegativeRegex)
	}
	if cfg.FileNameRegex != "" {
		opt.FileNameRegex = gogitlab.Ptr(cfg.FileNameRegex)
	}
	if cfg.MaxFileSize != nil {
		opt.MaxFileSize = cfg.MaxFileSize
	}
	if cfg.CommitCommitterCheck != nil {
		opt.CommitCommitterCheck = cfg.CommitCommitterCheck
	}
	if cfg.MemberCheck != nil {
		opt.MemberCheck = cfg.MemberCheck
	}
	if cfg.PreventSecrets != nil {
		opt.PreventSecrets = cfg.PreventSecrets
	}
	if cfg.DenyDeleteTag != nil {
		opt.DenyDeleteTag = cfg.DenyDeleteTag
	}
	if cfg.RejectUnsignedCommits != nil {
		opt.RejectUnsignedCommits = cfg.RejectUnsignedCommits
	}
	if cfg.RejectNonDCOCommits != nil {
		opt.RejectNonDCOCommits = cfg.RejectNonDCOCommits
	}
	return opt
}

func (r *PushRulesReconciler) toEditProjectOptions(cfg *config.PushRulesConfig) *gogitlab.EditProjectPushRuleOptions {
	opt := &gogitlab.EditProjectPushRuleOptions{}
	if cfg.AuthorEmailRegex != "" {
		opt.AuthorEmailRegex = gogitlab.Ptr(cfg.AuthorEmailRegex)
	}
	if cfg.BranchNameRegex != "" {
		opt.BranchNameRegex = gogitlab.Ptr(cfg.BranchNameRegex)
	}
	if cfg.CommitMessageRegex != "" {
		opt.CommitMessageRegex = gogitlab.Ptr(cfg.CommitMessageRegex)
	}
	if cfg.CommitMessageNegativeRegex != "" {
		opt.CommitMessageNegativeRegex = gogitlab.Ptr(cfg.CommitMessageNegativeRegex)
	}
	if cfg.FileNameRegex != "" {
		opt.FileNameRegex = gogitlab.Ptr(cfg.FileNameRegex)
	}
	if cfg.MaxFileSize != nil {
		opt.MaxFileSize = cfg.MaxFileSize
	}
	if cfg.CommitCommitterCheck != nil {
		opt.CommitCommitterCheck = cfg.CommitCommitterCheck
	}
	if cfg.MemberCheck != nil {
		opt.MemberCheck = cfg.MemberCheck
	}
	if cfg.PreventSecrets != nil {
		opt.PreventSecrets = cfg.PreventSecrets
	}
	if cfg.DenyDeleteTag != nil {
		opt.DenyDeleteTag = cfg.DenyDeleteTag
	}
	if cfg.RejectUnsignedCommits != nil {
		opt.RejectUnsignedCommits = cfg.RejectUnsignedCommits
	}
	if cfg.RejectNonDCOCommits != nil {
		opt.RejectNonDCOCommits = cfg.RejectNonDCOCommits
	}
	return opt
}

func (r *PushRulesReconciler) toAddGroupOptions(cfg *config.PushRulesConfig) *gogitlab.AddGroupPushRuleOptions {
	opt := &gogitlab.AddGroupPushRuleOptions{}
	if cfg.AuthorEmailRegex != "" {
		opt.AuthorEmailRegex = gogitlab.Ptr(cfg.AuthorEmailRegex)
	}
	if cfg.BranchNameRegex != "" {
		opt.BranchNameRegex = gogitlab.Ptr(cfg.BranchNameRegex)
	}
	if cfg.CommitMessageRegex != "" {
		opt.CommitMessageRegex = gogitlab.Ptr(cfg.CommitMessageRegex)
	}
	if cfg.CommitMessageNegativeRegex != "" {
		opt.CommitMessageNegativeRegex = gogitlab.Ptr(cfg.CommitMessageNegativeRegex)
	}
	if cfg.FileNameRegex != "" {
		opt.FileNameRegex = gogitlab.Ptr(cfg.FileNameRegex)
	}
	if cfg.MaxFileSize != nil {
		opt.MaxFileSize = cfg.MaxFileSize
	}
	if cfg.CommitCommitterCheck != nil {
		opt.CommitCommitterCheck = cfg.CommitCommitterCheck
	}
	if cfg.MemberCheck != nil {
		opt.MemberCheck = cfg.MemberCheck
	}
	if cfg.PreventSecrets != nil {
		opt.PreventSecrets = cfg.PreventSecrets
	}
	if cfg.DenyDeleteTag != nil {
		opt.DenyDeleteTag = cfg.DenyDeleteTag
	}
	if cfg.RejectUnsignedCommits != nil {
		opt.RejectUnsignedCommits = cfg.RejectUnsignedCommits
	}
	if cfg.RejectNonDCOCommits != nil {
		opt.RejectNonDCOCommits = cfg.RejectNonDCOCommits
	}
	return opt
}

func (r *PushRulesReconciler) toEditGroupOptions(cfg *config.PushRulesConfig) *gogitlab.EditGroupPushRuleOptions {
	opt := &gogitlab.EditGroupPushRuleOptions{}
	if cfg.AuthorEmailRegex != "" {
		opt.AuthorEmailRegex = gogitlab.Ptr(cfg.AuthorEmailRegex)
	}
	if cfg.BranchNameRegex != "" {
		opt.BranchNameRegex = gogitlab.Ptr(cfg.BranchNameRegex)
	}
	if cfg.CommitMessageRegex != "" {
		opt.CommitMessageRegex = gogitlab.Ptr(cfg.CommitMessageRegex)
	}
	if cfg.CommitMessageNegativeRegex != "" {
		opt.CommitMessageNegativeRegex = gogitlab.Ptr(cfg.CommitMessageNegativeRegex)
	}
	if cfg.FileNameRegex != "" {
		opt.FileNameRegex = gogitlab.Ptr(cfg.FileNameRegex)
	}
	if cfg.MaxFileSize != nil {
		opt.MaxFileSize = cfg.MaxFileSize
	}
	if cfg.CommitCommitterCheck != nil {
		opt.CommitCommitterCheck = cfg.CommitCommitterCheck
	}
	if cfg.MemberCheck != nil {
		opt.MemberCheck = cfg.MemberCheck
	}
	if cfg.PreventSecrets != nil {
		opt.PreventSecrets = cfg.PreventSecrets
	}
	if cfg.DenyDeleteTag != nil {
		opt.DenyDeleteTag = cfg.DenyDeleteTag
	}
	if cfg.RejectUnsignedCommits != nil {
		opt.RejectUnsignedCommits = cfg.RejectUnsignedCommits
	}
	if cfg.RejectNonDCOCommits != nil {
		opt.RejectNonDCOCommits = cfg.RejectNonDCOCommits
	}
	return opt
}

// isNotFound checks whether the GitLab response/error indicates a 404 resource absence.
func isNotFound(err error, resp *gogitlab.Response) bool {
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return true
	}
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "404") || strings.Contains(errStr, "not found") {
			return true
		}
	}
	return false
}
