package export

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gopkg.in/yaml.v3"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	glclient "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// Export divergence reconciliation strategies.
const (
	StrategyArchetype = "archetype"
	StrategyConsensus = "consensus"
	StrategyStrict    = "strict"
)

// ExportOptions specifies execution parameters for state export.
type ExportOptions struct {
	Client                    glclient.GitLabClient
	Config                    *config.PolicyConfig
	ProjectID                 int
	IncludeSecretsPlaceholder bool
	SkipReconcilers           map[string]bool
	FromProject               string
	Strategy                  string
	ErrOut                    io.Writer
}

// ProjectPolicyExport holds the inspected policy configuration for a single project.
type ProjectPolicyExport struct {
	ProjectID   int
	ProjectPath string
	Policies    config.PoliciesConfig
}

// ExportFleetPolicy inspects the target fleet and emits a normalized policy configuration YAML.
func ExportFleetPolicy(ctx context.Context, opts ExportOptions) ([]byte, error) {
	if opts.Client == nil {
		return nil, fmt.Errorf("gitlab client cannot be nil")
	}
	if opts.Config == nil {
		opts.Config = &config.PolicyConfig{}
		opts.Config.SetDefaults()
	}
	if opts.ProjectID > 0 && opts.Config.Targets.ProjectSelector == nil {
		opts.Config.Targets.ProjectSelector = &config.ProjectSelector{
			IDRange: &config.IDRange{
				Min: opts.ProjectID,
				Max: opts.ProjectID,
			},
		}
		opts.Config.Targets.GroupSelector = nil
	}
	if opts.SkipReconcilers == nil {
		opts.SkipReconcilers = make(map[string]bool)
	}
	if opts.Strategy == "" {
		opts.Strategy = StrategyArchetype
	}

	concurrency := opts.Config.Settings.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	// 1. Discover target fleet (with single-project direct lookup optimization)
	var fleet *discovery.TargetFleet
	isSingleProject := opts.Config.Targets.ProjectSelector != nil &&
		opts.Config.Targets.ProjectSelector.IDRange != nil &&
		opts.Config.Targets.ProjectSelector.IDRange.Min > 0 &&
		opts.Config.Targets.ProjectSelector.IDRange.Min == opts.Config.Targets.ProjectSelector.IDRange.Max &&
		opts.Config.Targets.GroupSelector == nil

	if isSingleProject {
		projectID := opts.Config.Targets.ProjectSelector.IDRange.Min
		proj, resp, err := opts.Client.Projects().GetProject(projectID, nil, gitlab.WithContext(ctx))
		if isNotFound(err, resp) || proj == nil {
			return nil, fmt.Errorf("target project %d not found", projectID)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to fetch target project %d: %w", projectID, err)
		}
		fleet = discovery.NewTargetFleet()
		fleet.Projects[proj.ID] = &discovery.TargetProject{
			ID:                proj.ID,
			Name:              proj.Name,
			Path:              proj.Path,
			PathWithNamespace: proj.PathWithNamespace,
			DefaultBranch:     proj.DefaultBranch,
			Visibility:        string(proj.Visibility),
			Archived:          proj.Archived,
			Raw:               proj,
		}
		fleet.MatchedProjectsCount = 1
	} else {
		discovered, err := discovery.DiscoverFleet(ctx, opts.Client, opts.Config.Targets, discovery.WithConcurrency(concurrency))
		if err != nil {
			return nil, fmt.Errorf("fleet discovery failed: %w", err)
		}
		fleet = discovered
	}

	if len(fleet.Projects) == 0 {
		slog.Warn("No projects discovered in target group/fleet for export")
	}

	// 2. Concurrently inspect each discovered project using bounded worker pool
	projects := make([]*discovery.TargetProject, 0, len(fleet.Projects))
	for _, p := range fleet.Projects {
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].ID < projects[j].ID
	})

	projectExports := make([]ProjectPolicyExport, len(projects))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var inspectErr error
	var errMu sync.Mutex

	for i, p := range projects {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, targetProj *discovery.TargetProject) {
			defer wg.Done()
			defer func() { <-sem }()

			if ctx.Err() != nil {
				errMu.Lock()
				if inspectErr == nil {
					inspectErr = ctx.Err()
				}
				errMu.Unlock()
				return
			}
			projectExports[idx] = inspectProject(ctx, opts.Client, targetProj, opts)
		}(i, p)
	}
	wg.Wait()

	if inspectErr != nil {
		return nil, inspectErr
	}

	// 3. Normalize policies across projects and detect divergence
	normalizedPolicies, warnings, err := normalizePolicies(projectExports, opts)
	if err != nil {
		return nil, err
	}

	// Log divergence warnings to stderr
	for _, w := range warnings {
		slog.Warn(w)
		if opts.ErrOut != nil {
			fmt.Fprintf(opts.ErrOut, "WARNING: %s\n", w)
		}
	}

	// 4. Construct exported PolicyConfig root schema
	exportCfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			DryRun: ptrBool(true),
		},
		Targets:  buildExportTargets(opts.Config.Targets, fleet),
		Policies: normalizedPolicies,
	}

	// 5. Serialize to YAML with warning comments
	yamlData, err := serializeWithComments(exportCfg, warnings)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize exported policy YAML: %w", err)
	}

	// 6. Round-trip validation check
	if _, err := config.LoadFromBytes(ctx, yamlData); err != nil {
		slog.Warn("Exported policy round-trip validation note", "error", err)
	}

	return yamlData, nil
}

func inspectProject(ctx context.Context, client glclient.GitLabClient, proj *discovery.TargetProject, opts ExportOptions) ProjectPolicyExport {
	export := ProjectPolicyExport{
		ProjectID:   proj.ID,
		ProjectPath: proj.PathWithNamespace,
	}
	if ctx.Err() != nil {
		return export
	}

	// Reuse existing project metadata or fetch once for project settings and runners
	var rawProj *gitlab.Project
	if proj.Raw != nil {
		rawProj = proj.Raw
	} else {
		p, _, err := client.Projects().GetProject(proj.ID, nil, gitlab.WithContext(ctx))
		if err == nil {
			rawProj = p
		}
	}

	// 1. Push Rules
	if !opts.SkipReconcilers["push_rules"] && ctx.Err() == nil {
		if pr, err := inspectPushRules(ctx, client, proj.ID); err != nil {
			logWarning(opts, proj.PathWithNamespace, "push_rules", err)
		} else {
			export.Policies.PushRules = pr
		}
	}

	// 2. Protected Branches
	if !opts.SkipReconcilers["protected_branches"] && ctx.Err() == nil {
		if pb, err := inspectProtectedBranches(ctx, client, proj.ID); err != nil {
			logWarning(opts, proj.PathWithNamespace, "protected_branches", err)
		} else {
			export.Policies.ProtectedBranches = pb
		}
	}

	// 3. Approval Rules
	if !opts.SkipReconcilers["approval_rules"] && ctx.Err() == nil {
		if ar, err := inspectApprovalRules(ctx, client, proj.ID); err != nil {
			logWarning(opts, proj.PathWithNamespace, "approval_rules", err)
		} else {
			export.Policies.ApprovalRules = ar
		}
	}

	// 4. Project Settings
	if !opts.SkipReconcilers["project_settings"] && ctx.Err() == nil {
		if ps, err := inspectProjectSettings(ctx, client, proj.ID, rawProj); err != nil {
			logWarning(opts, proj.PathWithNamespace, "project_settings", err)
		} else {
			export.Policies.ProjectSettings = ps
		}
	}

	// 5. Pipeline Retention
	if !opts.SkipReconcilers["pipeline_retention"] && ctx.Err() == nil {
		if pr, err := inspectPipelineRetention(ctx, client, proj.ID); err != nil {
			logWarning(opts, proj.PathWithNamespace, "pipeline_retention", err)
		} else {
			export.Policies.PipelineRetention = pr
		}
	}

	// 6. Variables
	if !opts.SkipReconcilers["variables"] && ctx.Err() == nil {
		if vars, err := inspectVariables(ctx, client, proj.ID, opts.IncludeSecretsPlaceholder); err != nil {
			logWarning(opts, proj.PathWithNamespace, "variables", err)
		} else {
			export.Policies.Variables = vars
		}
	}

	// 7. Runners
	if !opts.SkipReconcilers["runners"] && ctx.Err() == nil {
		if r, err := inspectRunners(ctx, client, proj.ID, rawProj); err != nil {
			logWarning(opts, proj.PathWithNamespace, "runners", err)
		} else {
			export.Policies.Runners = r
		}
	}

	// 8. Compliance
	if !opts.SkipReconcilers["compliance"] && ctx.Err() == nil {
		if comp, err := inspectCompliance(ctx, client, proj.ID); err != nil {
			logWarning(opts, proj.PathWithNamespace, "compliance", err)
		} else {
			export.Policies.Compliance = comp
		}
	}

	// 9. Webhooks
	if !opts.SkipReconcilers["webhooks"] && ctx.Err() == nil {
		if wh, err := inspectWebhooks(ctx, client, proj.ID); err != nil {
			logWarning(opts, proj.PathWithNamespace, "webhooks", err)
		} else {
			export.Policies.Webhooks = wh
		}
	}

	// 10. Members
	if !opts.SkipReconcilers["members"] && ctx.Err() == nil {
		if mem, err := inspectMembers(ctx, client, proj.ID); err != nil {
			logWarning(opts, proj.PathWithNamespace, "members", err)
		} else {
			export.Policies.Members = mem
		}
	}

	// 11. Target Branch Rules
	if !opts.SkipReconcilers["target_branch_rules"] && ctx.Err() == nil {
		if tbr, err := inspectTargetBranchRules(ctx, client, proj.PathWithNamespace); err != nil {
			logWarning(opts, proj.PathWithNamespace, "target_branch_rules", err)
		} else {
			export.Policies.TargetBranchRules = tbr
		}
	}

	return export
}

// ----------------------------------------------------------------------------
// Individual Reconciler Inspection Functions
// ----------------------------------------------------------------------------

func inspectPushRules(ctx context.Context, client glclient.GitLabClient, projectID int) (*config.PushRulesConfig, error) {
	rule, resp, err := client.PushRules().GetProjectPushRule(projectID, gitlab.WithContext(ctx))
	if isNotFound(err, resp) || rule == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	cfg := &config.PushRulesConfig{
		AuthorEmailRegex:           rule.AuthorEmailRegex,
		BranchNameRegex:            rule.BranchNameRegex,
		CommitMessageRegex:         rule.CommitMessageRegex,
		CommitMessageNegativeRegex: rule.CommitMessageNegativeRegex,
		FileNameRegex:              rule.FileNameRegex,
	}

	if rule.MaxFileSize > 0 {
		cfg.MaxFileSize = ptrInt(rule.MaxFileSize)
	}
	if rule.CommitCommitterCheck {
		cfg.CommitCommitterCheck = ptrBool(rule.CommitCommitterCheck)
	}
	if rule.MemberCheck {
		cfg.MemberCheck = ptrBool(rule.MemberCheck)
	}
	if rule.PreventSecrets {
		cfg.PreventSecrets = ptrBool(rule.PreventSecrets)
	}
	if rule.DenyDeleteTag {
		cfg.DenyDeleteTag = ptrBool(rule.DenyDeleteTag)
	}
	if rule.RejectUnsignedCommits {
		cfg.RejectUnsignedCommits = ptrBool(rule.RejectUnsignedCommits)
	}
	if rule.RejectNonDCOCommits {
		cfg.RejectNonDCOCommits = ptrBool(rule.RejectNonDCOCommits)
	}

	if isPushRulesEmpty(cfg) {
		return nil, nil
	}
	return cfg, nil
}

func inspectProtectedBranches(ctx context.Context, client glclient.GitLabClient, projectID int) ([]config.ProtectedBranchRuleConfig, error) {
	var rules []config.ProtectedBranchRuleConfig
	page := 1
	for {
		opts := &gitlab.ListProtectedBranchesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		}
		branches, resp, err := client.ProtectedBranches().ListProtectedBranches(projectID, opts, gitlab.WithContext(ctx))
		if isNotFound(err, resp) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}

		for _, b := range branches {
			rule := config.ProtectedBranchRuleConfig{
				Name: b.Name,
			}
			if b.AllowForcePush {
				rule.AllowForcePush = ptrBool(b.AllowForcePush)
			}
			if b.CodeOwnerApprovalRequired {
				rule.CodeOwnerApprovalRequired = ptrBool(b.CodeOwnerApprovalRequired)
			}

			rule.AllowedToPush = liveAccessToBranchDescriptions(b.PushAccessLevels)
			rule.AllowedToMerge = liveAccessToBranchDescriptions(b.MergeAccessLevels)
			rule.AllowedToUnprotect = liveAccessToBranchDescriptions(b.UnprotectAccessLevels)

			rules = append(rules, rule)
		}

		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return rules, nil
}

func liveAccessToBranchDescriptions(levels []*gitlab.BranchAccessDescription) []config.BranchAccessDescription {
	if len(levels) == 0 {
		return nil
	}
	var res []config.BranchAccessDescription
	for _, l := range levels {
		desc := config.BranchAccessDescription{}
		if l.AccessLevel > 0 {
			desc.AccessLevel = int(l.AccessLevel)
		}
		if l.UserID > 0 {
			desc.UserID = l.UserID
		}
		if l.GroupID > 0 {
			desc.GroupID = l.GroupID
		}
		res = append(res, desc)
	}
	return res
}

func inspectApprovalRules(ctx context.Context, client glclient.GitLabClient, projectID int) (*config.ApprovalRulesConfig, error) {
	app, resp, err := client.ApprovalRules().GetApprovalConfiguration(projectID, gitlab.WithContext(ctx))
	if isNotFound(err, resp) {
		app = nil
	} else if err != nil {
		return nil, err
	}

	rules, resp, err := client.ApprovalRules().GetProjectApprovalRules(projectID, nil, gitlab.WithContext(ctx))
	if isNotFound(err, resp) {
		rules = nil
	} else if err != nil {
		return nil, err
	}

	if app == nil && len(rules) == 0 {
		return nil, nil
	}

	cfg := &config.ApprovalRulesConfig{}

	if app != nil {
		if app.ApprovalsBeforeMerge > 0 {
			cfg.ApprovalsBeforeMerge = ptrInt(app.ApprovalsBeforeMerge)
		}
		if app.ResetApprovalsOnPush {
			cfg.ResetApprovalsOnPush = ptrBool(app.ResetApprovalsOnPush)
		}

		settings := &config.ApprovalSettingsConfig{}
		hasSettings := false
		if app.MergeRequestsAuthorApproval {
			settings.AllowAuthorApproval = ptrBool(app.MergeRequestsAuthorApproval)
			hasSettings = true
		}
		if app.MergeRequestsDisableCommittersApproval {
			settings.AllowCommitterApproval = ptrBool(!app.MergeRequestsDisableCommittersApproval)
			hasSettings = true
		}
		if app.DisableOverridingApproversPerMergeRequest {
			settings.AllowOverridesToApproverListPerMergeRequest = ptrBool(!app.DisableOverridingApproversPerMergeRequest)
			hasSettings = true
		}
		if app.SelectiveCodeOwnerRemovals {
			settings.SelectiveCodeOwnerRemovals = ptrBool(app.SelectiveCodeOwnerRemovals)
			hasSettings = true
		}
		if app.RequirePasswordToApprove {
			settings.RequirePasswordToApprove = ptrBool(app.RequirePasswordToApprove)
			hasSettings = true
		}
		if hasSettings {
			cfg.Settings = settings
		}
	}

	for _, r := range rules {
		ruleCfg := config.ApprovalRuleConfig{
			Name:              r.Name,
			ApprovalsRequired: r.ApprovalsRequired,
			RuleType:          string(r.RuleType),
		}

		usersList := r.Users
		if len(usersList) == 0 && len(r.EligibleApprovers) > 0 {
			for _, ea := range r.EligibleApprovers {
				if ea != nil {
					usersList = append(usersList, ea)
				}
			}
		}

		for _, u := range usersList {
			if u.Username != "" {
				ruleCfg.UserUsernames = append(ruleCfg.UserUsernames, u.Username)
			} else if u.ID > 0 {
				ruleCfg.UserIDs = append(ruleCfg.UserIDs, u.ID)
			}
		}

		for _, g := range r.Groups {
			if g.FullPath != "" {
				ruleCfg.GroupPaths = append(ruleCfg.GroupPaths, g.FullPath)
			} else if g.ID > 0 {
				ruleCfg.GroupIDs = append(ruleCfg.GroupIDs, g.ID)
			}
		}

		for _, pb := range r.ProtectedBranches {
			if pb.Name != "" {
				ruleCfg.ProtectedBranchNames = append(ruleCfg.ProtectedBranchNames, pb.Name)
			} else if pb.ID > 0 {
				ruleCfg.ProtectedBranchIDs = append(ruleCfg.ProtectedBranchIDs, pb.ID)
			}
		}

		hasApprovers := len(ruleCfg.UserUsernames) > 0 || len(ruleCfg.UserIDs) > 0 || len(ruleCfg.GroupPaths) > 0 || len(ruleCfg.GroupIDs) > 0
		if ruleCfg.RuleType != "any_approver" && !hasApprovers {
			// Skip system-generated report/code_owner rules that have no explicit approvers to avoid validate failure
			continue
		}

		cfg.Rules = append(cfg.Rules, ruleCfg)
	}

	if isApprovalRulesEmpty(cfg) {
		return nil, nil
	}

	return cfg, nil
}

func isApprovalRulesEmpty(cfg *config.ApprovalRulesConfig) bool {
	if cfg == nil {
		return true
	}
	return cfg.ApprovalsBeforeMerge == nil &&
		cfg.ResetApprovalsOnPush == nil &&
		cfg.Settings == nil &&
		len(cfg.Rules) == 0
}

func inspectProjectSettings(ctx context.Context, client glclient.GitLabClient, projectID int, proj *gitlab.Project) (*config.ProjectSettingsConfig, error) {
	if proj == nil {
		p, resp, err := client.Projects().GetProject(projectID, nil, gitlab.WithContext(ctx))
		if isNotFound(err, resp) || p == nil {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		proj = p
	}

	cfg := &config.ProjectSettingsConfig{
		DefaultBranch: proj.DefaultBranch,
		SquashOption:  string(proj.SquashOption),
		MergeMethod:   string(proj.MergeMethod),
	}

	if proj.OnlyAllowMergeIfPipelineSucceeds {
		cfg.OnlyAllowMergeIfPipelineSucceeds = ptrBool(proj.OnlyAllowMergeIfPipelineSucceeds)
	}
	if proj.AllowMergeOnSkippedPipeline {
		cfg.AllowMergeOnSkippedPipeline = ptrBool(proj.AllowMergeOnSkippedPipeline)
	}
	if proj.OnlyAllowMergeIfAllDiscussionsAreResolved {
		cfg.OnlyAllowMergeIfAllDiscussionsAreResolved = ptrBool(proj.OnlyAllowMergeIfAllDiscussionsAreResolved)
	}
	if proj.RemoveSourceBranchAfterMerge {
		cfg.RemoveSourceBranchAfterMerge = ptrBool(proj.RemoveSourceBranchAfterMerge)
	}
	if proj.KeepLatestArtifact {
		cfg.KeepLatestArtifact = ptrBool(proj.KeepLatestArtifact)
	}
	if proj.PrintingMergeRequestLinkEnabled {
		cfg.PrintingMergeRequestLinkEnabled = ptrBool(proj.PrintingMergeRequestLinkEnabled)
	}
	if proj.AutoCancelPendingPipelines != "" {
		cfg.AutoCancelPendingPipelines = proj.AutoCancelPendingPipelines
	}
	if proj.AutoDevopsEnabled {
		cfg.AutoDevopsEnabled = ptrBool(proj.AutoDevopsEnabled)
	}

	if proj.ContainerExpirationPolicy != nil {
		cep := proj.ContainerExpirationPolicy
		cfg.ContainerExpirationPolicy = &config.ContainerExpirationPolicyConfig{
			Cadence:         cep.Cadence,
			OlderThan:       cep.OlderThan,
			NameRegex:       cep.NameRegex,
			NameRegexDelete: cep.NameRegexDelete,
			NameRegexKeep:   cep.NameRegexKeep,
		}
		if cep.Enabled {
			cfg.ContainerExpirationPolicy.Enabled = ptrBool(cep.Enabled)
		}
		if cep.KeepN > 0 {
			cfg.ContainerExpirationPolicy.KeepN = ptrInt(cep.KeepN)
		}
	}

	return cfg, nil
}

func inspectPipelineRetention(ctx context.Context, client glclient.GitLabClient, projectID int) (*config.PipelineRetentionConfig, error) {
	seconds, resp, err := client.Projects().GetProjectPipelineRetention(projectID, gitlab.WithContext(ctx))
	if isNotFound(err, resp) || seconds <= 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	days := seconds / 86400
	if days <= 0 {
		return nil, nil
	}

	return &config.PipelineRetentionConfig{
		RetentionDays: days,
	}, nil
}

func inspectVariables(ctx context.Context, client glclient.GitLabClient, projectID int, includePlaceholder bool) ([]config.VariableConfig, error) {
	var allVars []*gitlab.ProjectVariable
	page := 1
	for {
		opts := &gitlab.ListProjectVariablesOptions{
			Page:    page,
			PerPage: 100,
		}
		vars, resp, err := client.Variables().ListProjectVariables(projectID, opts, gitlab.WithContext(ctx))
		if isNotFound(err, resp) {
			break
		}
		if err != nil {
			return nil, err
		}
		allVars = append(allVars, vars...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	var res []config.VariableConfig
	for _, v := range allVars {
		val := v.Value
		if includePlaceholder && (v.Masked || isSecretVariable(v.Key)) {
			val = fmt.Sprintf("${%s:-PLACEHOLDER}", v.Key)
		}

		varCfg := config.VariableConfig{
			Key:              v.Key,
			Value:            val,
			VariableType:     string(v.VariableType),
			EnvironmentScope: v.EnvironmentScope,
			Description:      v.Description,
		}
		if v.Protected {
			varCfg.Protected = ptrBool(v.Protected)
		}
		if v.Masked {
			varCfg.Masked = ptrBool(v.Masked)
		}
		if v.Raw {
			varCfg.Raw = ptrBool(v.Raw)
		}

		res = append(res, varCfg)
	}

	return res, nil
}

func isSecretVariable(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "KEY") ||
		strings.Contains(upper, "PASS") ||
		strings.Contains(upper, "CREDENTIAL")
}

func inspectRunners(ctx context.Context, client glclient.GitLabClient, projectID int, proj *gitlab.Project) (*config.RunnersConfig, error) {
	if proj == nil {
		p, _, _ := client.Projects().GetProject(projectID, nil, gitlab.WithContext(ctx))
		proj = p
	}

	var allRunners []*gitlab.Runner
	page := 1
	for {
		opts := &gitlab.ListProjectRunnersOptions{
			ListOptions: gitlab.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		}
		runners, resp, err := client.Runners().ListProjectRunners(projectID, opts, gitlab.WithContext(ctx))
		if isNotFound(err, resp) {
			break
		}
		if err != nil {
			return nil, err
		}
		allRunners = append(allRunners, runners...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	if proj == nil && len(allRunners) == 0 {
		return nil, nil
	}

	cfg := &config.RunnersConfig{}
	if proj != nil {
		if proj.SharedRunnersEnabled {
			cfg.SharedRunnersEnabled = ptrBool(proj.SharedRunnersEnabled)
		}
		if proj.GroupRunnersEnabled {
			cfg.GroupRunnersEnabled = ptrBool(proj.GroupRunnersEnabled)
		}
	}

	for _, r := range allRunners {
		details, _, _ := client.Runners().GetRunnerDetails(r.ID, gitlab.WithContext(ctx))

		rCfg := config.RunnerConfig{
			ID:          r.ID,
			Description: r.Description,
		}

		if details != nil {
			rCfg.AccessLevel = string(details.AccessLevel)
			if details.Paused {
				rCfg.Paused = ptrBool(details.Paused)
			}
			if details.Locked {
				rCfg.Locked = ptrBool(details.Locked)
			}
			if details.RunUntagged {
				rCfg.RunUntagged = ptrBool(details.RunUntagged)
			}
			if details.MaximumTimeout > 0 {
				rCfg.MaximumTimeout = ptrInt(details.MaximumTimeout)
			}
			if len(details.TagList) > 0 {
				rCfg.TagList = details.TagList
			}
		} else {
			if r.Paused {
				rCfg.Paused = ptrBool(r.Paused)
			}
		}

		cfg.Runners = append(cfg.Runners, rCfg)
	}

	if isRunnersEmpty(cfg) {
		return nil, nil
	}

	return cfg, nil
}

func isRunnersEmpty(cfg *config.RunnersConfig) bool {
	if cfg == nil {
		return true
	}
	return cfg.SharedRunnersEnabled == nil &&
		cfg.GroupRunnersEnabled == nil &&
		len(cfg.Runners) == 0
}

func inspectCompliance(ctx context.Context, client glclient.GitLabClient, projectID int) (*config.ComplianceConfig, error) {
	frameworks, err := client.Compliance().GetProjectComplianceFrameworks(ctx, projectID)
	if err != nil || len(frameworks) == 0 {
		return nil, nil
	}

	fw := frameworks[0]
	return &config.ComplianceConfig{
		FrameworkName: fw.Name,
	}, nil
}

func inspectWebhooks(ctx context.Context, client glclient.GitLabClient, projectID int) ([]config.WebhookConfig, error) {
	var allHooks []*gitlab.ProjectHook
	page := 1
	for {
		opts := &gitlab.ListProjectHooksOptions{
			Page:    page,
			PerPage: 100,
		}
		hooks, resp, err := client.Webhooks().ListProjectHooks(projectID, opts, gitlab.WithContext(ctx))
		if isNotFound(err, resp) {
			break
		}
		if err != nil {
			return nil, err
		}
		allHooks = append(allHooks, hooks...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	var res []config.WebhookConfig
	for _, h := range allHooks {
		wh := config.WebhookConfig{
			URL:                    h.URL,
			PushEventsBranchFilter: h.PushEventsBranchFilter,
		}
		if h.PushEvents {
			wh.PushEvents = ptrBool(h.PushEvents)
		}
		if h.MergeRequestsEvents {
			wh.MergeRequestsEvents = ptrBool(h.MergeRequestsEvents)
		}
		if h.TagPushEvents {
			wh.TagPushEvents = ptrBool(h.TagPushEvents)
		}
		if h.IssuesEvents {
			wh.IssuesEvents = ptrBool(h.IssuesEvents)
		}
		if h.PipelineEvents {
			wh.PipelineEvents = ptrBool(h.PipelineEvents)
		}
		if h.JobEvents {
			wh.JobEvents = ptrBool(h.JobEvents)
		}
		if h.ReleasesEvents {
			wh.ReleasesEvents = ptrBool(h.ReleasesEvents)
		}
		if h.EnableSSLVerification {
			wh.EnableSSLVerification = ptrBool(h.EnableSSLVerification)
		}

		res = append(res, wh)
	}

	return res, nil
}

func inspectMembers(ctx context.Context, client glclient.GitLabClient, projectID int) (*config.MembersConfig, error) {
	var allMembers []*gitlab.ProjectMember
	page := 1
	for {
		opts := &gitlab.ListProjectMembersOptions{
			ListOptions: gitlab.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		}
		members, resp, err := client.Members().ListProjectMembers(projectID, opts, gitlab.WithContext(ctx))
		if isNotFound(err, resp) {
			break
		}
		if err != nil {
			return nil, err
		}
		allMembers = append(allMembers, members...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	if len(allMembers) == 0 {
		return nil, nil
	}

	cfg := &config.MembersConfig{}
	for _, m := range allMembers {
		rule := config.MemberRuleConfig{
			Username:    m.Username,
			AccessLevel: int(m.AccessLevel),
		}
		if m.ExpiresAt != nil {
			rule.ExpiresAt = m.ExpiresAt.String()
		}
		cfg.AllowedMembers = append(cfg.AllowedMembers, rule)
	}

	return cfg, nil
}

func inspectTargetBranchRules(ctx context.Context, client glclient.GitLabClient, projectPath string) (*config.TargetBranchRulesConfig, error) {
	rules, err := client.TargetBranchRules().GetTargetBranchRules(ctx, projectPath)
	if err != nil || len(rules) == 0 {
		return nil, nil
	}

	cfg := &config.TargetBranchRulesConfig{}
	for _, r := range rules {
		cfg.Rules = append(cfg.Rules, config.TargetBranchRuleConfig{
			Name:         r.Name,
			TargetBranch: r.TargetBranch,
		})
	}

	return cfg, nil
}

// ----------------------------------------------------------------------------
// Policy Normalization & Divergence Warning Helper
// ----------------------------------------------------------------------------

func normalizePolicies(projectExports []ProjectPolicyExport, opts ExportOptions) (config.PoliciesConfig, []string, error) {
	var warnings []string
	norm := config.PoliciesConfig{}

	if len(projectExports) == 0 {
		return norm, warnings, nil
	}

	strategy := opts.Strategy
	if strategy == "" {
		strategy = "archetype"
	}

	archetypeIdx := 0
	if opts.FromProject != "" {
		found := false
		for i, pe := range projectExports {
			if fmt.Sprintf("%d", pe.ProjectID) == opts.FromProject ||
				pe.ProjectPath == opts.FromProject ||
				strings.HasSuffix(pe.ProjectPath, "/"+opts.FromProject) {
				archetypeIdx = i
				found = true
				break
			}
		}
		if !found {
			return norm, nil, fmt.Errorf("from-project '%s' not found in discovered fleet", opts.FromProject)
		}
	}

	archetypeExport := projectExports[archetypeIdx]
	totalProjects := len(projectExports)

	divergenceDesc := func(reconciler string) string {
		if opts.FromProject != "" {
			return fmt.Sprintf("%d projects diverge from archetype project '%s' on %s — only archetype project's values shown", totalProjects, archetypeExport.ProjectPath, reconciler)
		}
		return fmt.Sprintf("%d projects have divergent %s — only first project's values shown", totalProjects, reconciler)
	}

	resolveDivergence := func(reconciler string, isUniform bool, assignUniform func(), assignArchetype func()) error {
		if isUniform {
			assignUniform()
			return nil
		}
		if totalProjects <= 1 {
			assignArchetype()
			return nil
		}
		switch strategy {
		case "strict":
			return fmt.Errorf("strict export failed: %d projects have divergent %s", totalProjects, reconciler)
		case "consensus":
			warnings = append(warnings, fmt.Sprintf("%d projects have divergent %s — omitted under consensus strategy", totalProjects, reconciler))
		case "archetype":
			fallthrough
		default:
			assignArchetype()
			warnings = append(warnings, divergenceDesc(reconciler))
		}
		return nil
	}

	// 1. Push Rules
	var pushRulesList []*config.PushRulesConfig
	for _, pe := range projectExports {
		if pe.Policies.PushRules != nil {
			pushRulesList = append(pushRulesList, pe.Policies.PushRules)
		}
	}
	pushRulesUniform := allPushRulesEqual(pushRulesList, totalProjects)
	if err := resolveDivergence("push_rules", pushRulesUniform,
		func() {
			if len(pushRulesList) > 0 {
				norm.PushRules = pushRulesList[0]
			}
		},
		func() {
			norm.PushRules = archetypeExport.Policies.PushRules
		}); err != nil {
		return norm, nil, err
	}

	// 2. Protected Branches
	var pbList [][]config.ProtectedBranchRuleConfig
	for _, pe := range projectExports {
		if len(pe.Policies.ProtectedBranches) > 0 {
			pbList = append(pbList, pe.Policies.ProtectedBranches)
		}
	}
	pbUniform := allProtectedBranchesEqual(pbList, totalProjects)
	if err := resolveDivergence("protected_branches", pbUniform,
		func() {
			if len(pbList) > 0 {
				norm.ProtectedBranches = pbList[0]
			}
		},
		func() {
			norm.ProtectedBranches = archetypeExport.Policies.ProtectedBranches
		}); err != nil {
		return norm, nil, err
	}

	// 3. Approval Rules
	var arList []*config.ApprovalRulesConfig
	for _, pe := range projectExports {
		if pe.Policies.ApprovalRules != nil {
			arList = append(arList, pe.Policies.ApprovalRules)
		}
	}
	arUniform := allApprovalRulesEqual(arList, totalProjects)
	if err := resolveDivergence("approval_rules", arUniform,
		func() {
			if len(arList) > 0 {
				norm.ApprovalRules = arList[0]
			}
		},
		func() {
			norm.ApprovalRules = archetypeExport.Policies.ApprovalRules
		}); err != nil {
		return norm, nil, err
	}

	// 4. Project Settings
	var psList []*config.ProjectSettingsConfig
	for _, pe := range projectExports {
		if pe.Policies.ProjectSettings != nil {
			psList = append(psList, pe.Policies.ProjectSettings)
		}
	}
	psUniform := allProjectSettingsEqual(psList, totalProjects)
	if err := resolveDivergence("project_settings", psUniform,
		func() {
			if len(psList) > 0 {
				norm.ProjectSettings = psList[0]
			}
		},
		func() {
			norm.ProjectSettings = archetypeExport.Policies.ProjectSettings
		}); err != nil {
		return norm, nil, err
	}

	// 5. Pipeline Retention
	var prList []*config.PipelineRetentionConfig
	for _, pe := range projectExports {
		if pe.Policies.PipelineRetention != nil {
			prList = append(prList, pe.Policies.PipelineRetention)
		}
	}
	retentionUniform := allPipelineRetentionEqual(prList, totalProjects)
	if err := resolveDivergence("pipeline_retention", retentionUniform,
		func() {
			if len(prList) > 0 {
				norm.PipelineRetention = prList[0]
			}
		},
		func() {
			norm.PipelineRetention = archetypeExport.Policies.PipelineRetention
		}); err != nil {
		return norm, nil, err
	}

	// 6. Variables
	var varList [][]config.VariableConfig
	for _, pe := range projectExports {
		if len(pe.Policies.Variables) > 0 {
			varList = append(varList, pe.Policies.Variables)
		}
	}
	varUniform := allVariablesEqual(varList, totalProjects)
	if err := resolveDivergence("variables", varUniform,
		func() {
			if len(varList) > 0 {
				norm.Variables = varList[0]
			}
		},
		func() {
			norm.Variables = archetypeExport.Policies.Variables
		}); err != nil {
		return norm, nil, err
	}

	// 7. Runners
	var runnerList []*config.RunnersConfig
	for _, pe := range projectExports {
		if pe.Policies.Runners != nil {
			runnerList = append(runnerList, pe.Policies.Runners)
		}
	}
	runnerUniform := allRunnersEqual(runnerList, totalProjects)
	if err := resolveDivergence("runners", runnerUniform,
		func() {
			if len(runnerList) > 0 {
				norm.Runners = runnerList[0]
			}
		},
		func() {
			norm.Runners = archetypeExport.Policies.Runners
		}); err != nil {
		return norm, nil, err
	}

	// 8. Compliance
	var compList []*config.ComplianceConfig
	for _, pe := range projectExports {
		if pe.Policies.Compliance != nil {
			compList = append(compList, pe.Policies.Compliance)
		}
	}
	compUniform := allComplianceEqual(compList, totalProjects)
	if err := resolveDivergence("compliance", compUniform,
		func() {
			if len(compList) > 0 {
				norm.Compliance = compList[0]
			}
		},
		func() {
			norm.Compliance = archetypeExport.Policies.Compliance
		}); err != nil {
		return norm, nil, err
	}

	// 9. Webhooks
	var whList [][]config.WebhookConfig
	for _, pe := range projectExports {
		if len(pe.Policies.Webhooks) > 0 {
			whList = append(whList, pe.Policies.Webhooks)
		}
	}
	whUniform := allWebhooksEqual(whList, totalProjects)
	if err := resolveDivergence("webhooks", whUniform,
		func() {
			if len(whList) > 0 {
				norm.Webhooks = whList[0]
			}
		},
		func() {
			norm.Webhooks = archetypeExport.Policies.Webhooks
		}); err != nil {
		return norm, nil, err
	}

	// 10. Members
	var memList []*config.MembersConfig
	for _, pe := range projectExports {
		if pe.Policies.Members != nil {
			memList = append(memList, pe.Policies.Members)
		}
	}
	memUniform := allMembersEqual(memList, totalProjects)
	if err := resolveDivergence("members", memUniform,
		func() {
			if len(memList) > 0 {
				norm.Members = memList[0]
			}
		},
		func() {
			norm.Members = archetypeExport.Policies.Members
		}); err != nil {
		return norm, nil, err
	}

	// 11. Target Branch Rules
	var tbrList []*config.TargetBranchRulesConfig
	for _, pe := range projectExports {
		if pe.Policies.TargetBranchRules != nil {
			tbrList = append(tbrList, pe.Policies.TargetBranchRules)
		}
	}
	tbrUniform := allTargetBranchRulesEqual(tbrList, totalProjects)
	if err := resolveDivergence("target_branch_rules", tbrUniform,
		func() {
			if len(tbrList) > 0 {
				norm.TargetBranchRules = tbrList[0]
			}
		},
		func() {
			norm.TargetBranchRules = archetypeExport.Policies.TargetBranchRules
		}); err != nil {
		return norm, nil, err
	}

	return norm, warnings, nil
}

// Equality comparison helpers for normalization
func allPushRulesEqual(list []*config.PushRulesConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allProtectedBranchesEqual(list [][]config.ProtectedBranchRuleConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allApprovalRulesEqual(list []*config.ApprovalRulesConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allProjectSettingsEqual(list []*config.ProjectSettingsConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allPipelineRetentionEqual(list []*config.PipelineRetentionConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allVariablesEqual(list [][]config.VariableConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allRunnersEqual(list []*config.RunnersConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allComplianceEqual(list []*config.ComplianceConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allWebhooksEqual(list [][]config.WebhookConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allMembersEqual(list []*config.MembersConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

func allTargetBranchRulesEqual(list []*config.TargetBranchRulesConfig, totalProjects int) bool {
	if len(list) != totalProjects {
		return false
	}
	for i := 1; i < len(list); i++ {
		if !reflect.DeepEqual(list[0], list[i]) {
			return false
		}
	}
	return true
}

// ----------------------------------------------------------------------------
// Target Selector Construction Helper
// ----------------------------------------------------------------------------

func buildExportTargets(srcSelectors config.TargetSelectors, fleet *discovery.TargetFleet) config.TargetSelectors {
	if srcSelectors.GroupSelector != nil {
		return config.TargetSelectors{
			GroupSelector: srcSelectors.GroupSelector,
		}
	}
	if srcSelectors.ProjectSelector != nil {
		return config.TargetSelectors{
			ProjectSelector: srcSelectors.ProjectSelector,
		}
	}
	if len(fleet.Groups) > 0 {
		paths := make([]string, 0, len(fleet.Groups))
		for _, g := range fleet.Groups {
			paths = append(paths, g.FullPath)
		}
		rec := true
		return config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupPathsInclude: paths,
				Recursive:         &rec,
			},
		}
	}
	if len(fleet.Projects) > 0 {
		return config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				IDRange: &config.IDRange{
					Min: fleet.Projects[0].ID,
					Max: fleet.Projects[0].ID,
				},
			},
		}
	}
	return config.TargetSelectors{}
}

// ----------------------------------------------------------------------------
// Serialization with Comment Insertion Helper
// ----------------------------------------------------------------------------

func serializeWithComments(cfg *config.PolicyConfig, warnings []string) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	var docNode yaml.Node
	if err := docNode.Encode(cfg); err != nil {
		return nil, err
	}

	if len(warnings) > 0 {
		var rootMapping *yaml.Node
		if docNode.Kind == yaml.DocumentNode && len(docNode.Content) > 0 {
			rootMapping = docNode.Content[0]
		} else if docNode.Kind == yaml.MappingNode {
			rootMapping = &docNode
		}

		if rootMapping != nil {
			for i := 0; i < len(rootMapping.Content)-1; i += 2 {
				keyNode := rootMapping.Content[i]
				valNode := rootMapping.Content[i+1]
				if keyNode.Value == "policies" && valNode.Kind == yaml.MappingNode {
					matchedWarnings := make(map[string]bool)
					for j := 0; j < len(valNode.Content)-1; j += 2 {
						polKeyNode := valNode.Content[j]
						for _, w := range warnings {
							if strings.Contains(w, polKeyNode.Value) {
								polKeyNode.HeadComment = "WARNING: " + w
								matchedWarnings[w] = true
							}
						}
					}
					var unmatchedWarnings []string
					for _, w := range warnings {
						if !matchedWarnings[w] {
							unmatchedWarnings = append(unmatchedWarnings, w)
						}
					}
					if len(unmatchedWarnings) > 0 {
						comment := keyNode.HeadComment
						if comment != "" {
							comment += "\n"
						}
						for _, uw := range unmatchedWarnings {
							comment += "WARNING: " + uw + "\n"
						}
						keyNode.HeadComment = strings.TrimRight(comment, "\n")
					}
				}
			}
		}
	}

	if err := enc.Encode(&docNode); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func isNotFound(err error, resp *gitlab.Response) bool {
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return true
	}
	if err != nil {
		errStr := strings.ToLower(err.Error())
		return strings.Contains(errStr, "404") || strings.Contains(errStr, "not found")
	}
	return false
}

func isPushRulesEmpty(p *config.PushRulesConfig) bool {
	if p == nil {
		return true
	}
	return p.AuthorEmailRegex == "" &&
		p.BranchNameRegex == "" &&
		p.CommitMessageRegex == "" &&
		p.CommitMessageNegativeRegex == "" &&
		p.FileNameRegex == "" &&
		p.MaxFileSize == nil &&
		p.CommitCommitterCheck == nil &&
		p.MemberCheck == nil &&
		p.PreventSecrets == nil &&
		p.DenyDeleteTag == nil &&
		p.RejectUnsignedCommits == nil &&
		p.RejectNonDCOCommits == nil
}

func logWarning(opts ExportOptions, projectPath, reconciler string, err error) {
	msg := fmt.Sprintf("failed to inspect %s for project %s: %v", reconciler, projectPath, err)
	slog.Warn(msg)
	if opts.ErrOut != nil {
		fmt.Fprintf(opts.ErrOut, "WARNING: %s\n", msg)
	}
}

func ptrBool(b bool) *bool { return &b }
func ptrInt(i int) *int    { return &i }
