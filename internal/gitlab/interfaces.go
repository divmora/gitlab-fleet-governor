package gitlab

import (
	"context"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// GitLabClient is the top-level interface unifying all GitLab governance sub-services.
type GitLabClient interface {
	Projects() ProjectsService
	Groups() GroupsService
	ProtectedBranches() ProtectedBranchesService
	PushRules() PushRulesService
	ApprovalRules() ApprovalRulesService
	Variables() VariablesService
	Runners() RunnersService
	Compliance() ComplianceService
	Webhooks() WebhooksService
	Members() MembersService
	Users() UsersService

	// BaseURL returns the configured base URL for the client.
	BaseURL() string

	// RawClient returns the underlying SDK client if direct access is required.
	RawClient() *gitlab.Client
}

// ProjectsService abstracts project retrieval, editing, and listing.
type ProjectsService interface {
	GetProject(pid any, opt *gitlab.GetProjectOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Project, *gitlab.Response, error)
	EditProject(pid any, opt *gitlab.EditProjectOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Project, *gitlab.Response, error)
	ListProjects(opt *gitlab.ListProjectsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error)
	GetProjectPipelineRetention(pid any, options ...gitlab.RequestOptionFunc) (int, *gitlab.Response, error)
	SetProjectPipelineRetention(pid any, seconds int, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
}

// GroupsService abstracts group retrieval, subgroup traversal, and group project queries.
type GroupsService interface {
	GetGroup(gid any, opt *gitlab.GetGroupOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Group, *gitlab.Response, error)
	ListSubgroups(gid any, opt *gitlab.ListSubGroupsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Group, *gitlab.Response, error)
	ListGroupProjects(gid any, opt *gitlab.ListGroupProjectsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error)
}

// ProtectedBranchesService abstracts project branch protection rules.
type ProtectedBranchesService interface {
	ListProtectedBranches(pid any, opt *gitlab.ListProtectedBranchesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProtectedBranch, *gitlab.Response, error)
	ProtectRepositoryBranches(pid any, opt *gitlab.ProtectRepositoryBranchesOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProtectedBranch, *gitlab.Response, error)
	UnprotectRepositoryBranches(pid any, branch string, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
	UpdateProtectedBranch(pid any, branch string, opt *gitlab.UpdateProtectedBranchOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProtectedBranch, *gitlab.Response, error)
}

// PushRulesService abstracts project-level and group-level push rules.
type PushRulesService interface {
	GetProjectPushRule(pid any, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectPushRules, *gitlab.Response, error)
	AddProjectPushRule(pid any, opt *gitlab.AddProjectPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectPushRules, *gitlab.Response, error)
	EditProjectPushRule(pid any, opt *gitlab.EditProjectPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectPushRules, *gitlab.Response, error)
	GetGroupPushRule(gid any, options ...gitlab.RequestOptionFunc) (*gitlab.GroupPushRules, *gitlab.Response, error)
	AddGroupPushRule(gid any, opt *gitlab.AddGroupPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupPushRules, *gitlab.Response, error)
	EditGroupPushRule(gid any, opt *gitlab.EditGroupPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupPushRules, *gitlab.Response, error)
}

// ApprovalRulesService abstracts project merge request approval configuration and named rules.
type ApprovalRulesService interface {
	GetApprovalConfiguration(pid any, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovals, *gitlab.Response, error)
	ChangeApprovalConfiguration(pid any, opt *gitlab.ChangeApprovalConfigurationOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovals, *gitlab.Response, error)
	GetProjectApprovalRules(pid any, opt *gitlab.GetProjectApprovalRulesListsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectApprovalRule, *gitlab.Response, error)
	CreateProjectApprovalRule(pid any, opt *gitlab.CreateProjectLevelRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovalRule, *gitlab.Response, error)
	UpdateProjectApprovalRule(pid any, rule int, opt *gitlab.UpdateProjectLevelRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovalRule, *gitlab.Response, error)
	DeleteProjectApprovalRule(pid any, rule int, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
}

// VariablesService abstracts CI/CD project and group variable operations.
type VariablesService interface {
	ListProjectVariables(pid any, opt *gitlab.ListProjectVariablesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectVariable, *gitlab.Response, error)
	CreateProjectVariable(pid any, opt *gitlab.CreateProjectVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error)
	UpdateProjectVariable(pid any, key string, opt *gitlab.UpdateProjectVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error)
	RemoveProjectVariable(pid any, key string, opt *gitlab.RemoveProjectVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
	ListGroupVariables(gid any, opt *gitlab.ListGroupVariablesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.GroupVariable, *gitlab.Response, error)
	CreateGroupVariable(gid any, opt *gitlab.CreateGroupVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error)
	UpdateGroupVariable(gid any, key string, opt *gitlab.UpdateGroupVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error)
	RemoveGroupVariable(gid any, key string, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
}

// RunnersService abstracts CI/CD runner inspection and management.
type RunnersService interface {
	ListProjectRunners(pid any, opt *gitlab.ListProjectRunnersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Runner, *gitlab.Response, error)
	ListGroupsRunners(gid any, opt *gitlab.ListGroupsRunnersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Runner, *gitlab.Response, error)
	GetRunnerDetails(rid any, options ...gitlab.RequestOptionFunc) (*gitlab.RunnerDetails, *gitlab.Response, error)
	UpdateRunnerDetails(rid any, opt *gitlab.UpdateRunnerDetailsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.RunnerDetails, *gitlab.Response, error)
}

// ComplianceFramework represents a compliance framework descriptor.
type ComplianceFramework struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// ComplianceService abstracts compliance framework label queries and assignments.
type ComplianceService interface {
	GetProjectComplianceFrameworks(ctx context.Context, projectID int) ([]ComplianceFramework, error)
	SetProjectComplianceFramework(ctx context.Context, projectID int, frameworkID string) error
	RemoveProjectComplianceFramework(ctx context.Context, projectID int, frameworkID string) error
}

// WebhooksService abstracts project webhook integrations.
type WebhooksService interface {
	ListProjectHooks(pid any, opt *gitlab.ListProjectHooksOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectHook, *gitlab.Response, error)
	AddProjectHook(pid any, opt *gitlab.AddProjectHookOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectHook, *gitlab.Response, error)
	EditProjectHook(pid any, hook int, opt *gitlab.EditProjectHookOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectHook, *gitlab.Response, error)
	DeleteProjectHook(pid any, hook int, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error)
}

// MembersService abstracts project and group member lists.
type MembersService interface {
	ListProjectMembers(pid any, opt *gitlab.ListProjectMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectMember, *gitlab.Response, error)
	ListAllProjectMembers(pid any, opt *gitlab.ListProjectMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectMember, *gitlab.Response, error)
	ListGroupMembers(gid any, opt *gitlab.ListGroupMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.GroupMember, *gitlab.Response, error)
	ListAllGroupMembers(gid any, opt *gitlab.ListGroupMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.GroupMember, *gitlab.Response, error)
}

// UsersService abstracts user lookup operations.
type UsersService interface {
	ListUsers(opt *gitlab.ListUsersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error)
	GetUser(user int, opt gitlab.GetUsersOptions, options ...gitlab.RequestOptionFunc) (*gitlab.User, *gitlab.Response, error)
}
