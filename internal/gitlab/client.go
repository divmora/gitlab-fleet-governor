package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// Client is the concrete high-level GitLab governance client implementation
// wrapping *gitlab.Client and providing mockable service implementations.
type Client struct {
	raw               *gitlab.Client
	baseURL           string
	auth              *ResolvedAuth
	httpClient        *http.Client
	transportConfig   *GovernorTransportConfig
	projects          ProjectsService
	groups            GroupsService
	protectedBranches ProtectedBranchesService
	pushRules         PushRulesService
	approvalRules     ApprovalRulesService
	variables         VariablesService
	runners           RunnersService
	compliance        ComplianceService
	webhooks          WebhooksService
	members           MembersService
	users             UsersService
}

// ClientOption defines functional configuration options for Client.
type ClientOption func(*Client)

// WithHTTPClient allows injecting a custom *http.Client (e.g. rate limited or retry transport).
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithRateLimit configures token-bucket rate limiting on the client transport.
func WithRateLimit(rps float64, burst int) ClientOption {
	return func(c *Client) {
		if c.transportConfig == nil {
			cfg := DefaultGovernorTransportConfig()
			c.transportConfig = &cfg
		}
		c.transportConfig.RateLimitRPS = rps
		c.transportConfig.RateLimitBurst = burst
	}
}

// WithMaxRetries configures maximum retry attempts.
func WithMaxRetries(retries int) ClientOption {
	return func(c *Client) {
		if c.transportConfig == nil {
			cfg := DefaultGovernorTransportConfig()
			c.transportConfig = &cfg
		}
		c.transportConfig.MaxRetries = retries
	}
}

// WithBackoff configures retry backoff parameters.
func WithBackoff(base, max time.Duration, jitter ...float64) ClientOption {
	return func(c *Client) {
		if c.transportConfig == nil {
			cfg := DefaultGovernorTransportConfig()
			c.transportConfig = &cfg
		}
		c.transportConfig.BaseBackoff = base
		c.transportConfig.MaxBackoff = max
		if len(jitter) > 0 {
			c.transportConfig.JitterRatio = jitter[0]
		}
	}
}

// WithRetryBaseDelay configures base delay for retries.
func WithRetryBaseDelay(delay time.Duration) ClientOption {
	return func(c *Client) {
		if c.transportConfig == nil {
			cfg := DefaultGovernorTransportConfig()
			c.transportConfig = &cfg
		}
		c.transportConfig.BaseBackoff = delay
	}
}

// WithProjectsService overrides the default ProjectsService.
func WithProjectsService(s ProjectsService) ClientOption {
	return func(c *Client) { c.projects = s }
}

// WithGroupsService overrides the default GroupsService.
func WithGroupsService(s GroupsService) ClientOption {
	return func(c *Client) { c.groups = s }
}

// WithProtectedBranchesService overrides the default ProtectedBranchesService.
func WithProtectedBranchesService(s ProtectedBranchesService) ClientOption {
	return func(c *Client) { c.protectedBranches = s }
}

// WithPushRulesService overrides the default PushRulesService.
func WithPushRulesService(s PushRulesService) ClientOption {
	return func(c *Client) { c.pushRules = s }
}

// WithApprovalRulesService overrides the default ApprovalRulesService.
func WithApprovalRulesService(s ApprovalRulesService) ClientOption {
	return func(c *Client) { c.approvalRules = s }
}

// WithVariablesService overrides the default VariablesService.
func WithVariablesService(s VariablesService) ClientOption {
	return func(c *Client) { c.variables = s }
}

// WithRunnersService overrides the default RunnersService.
func WithRunnersService(s RunnersService) ClientOption {
	return func(c *Client) { c.runners = s }
}

// WithComplianceService overrides the default ComplianceService.
func WithComplianceService(s ComplianceService) ClientOption {
	return func(c *Client) { c.compliance = s }
}

// WithWebhooksService overrides the default WebhooksService.
func WithWebhooksService(s WebhooksService) ClientOption {
	return func(c *Client) { c.webhooks = s }
}

// WithMembersService overrides the default MembersService.
func WithMembersService(s MembersService) ClientOption {
	return func(c *Client) { c.members = s }
}

// WithUsersService overrides the default UsersService.
func WithUsersService(s UsersService) ClientOption {
	return func(c *Client) { c.users = s }
}

// NewClient constructs a new GitLabClient wrapper from resolved authentication.
func NewClient(auth *ResolvedAuth, opts ...ClientOption) (*Client, error) {
	if auth == nil {
		return nil, fmt.Errorf("resolved auth is required")
	}

	c := &Client{
		baseURL: auth.BaseURL,
		auth:    auth,
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.httpClient == nil && c.transportConfig != nil {
		transport := NewGovernorTransport(*c.transportConfig)
		c.httpClient = &http.Client{Transport: transport}
	}

	sdkOptions := []gitlab.ClientOptionFunc{
		gitlab.WithBaseURL(auth.BaseURL),
	}

	if c.httpClient != nil {
		sdkOptions = append(sdkOptions, gitlab.WithHTTPClient(c.httpClient))
	}

	var rawClient *gitlab.Client
	var err error

	switch auth.TokenType {
	case TokenTypeOAuth:
		rawClient, err = gitlab.NewOAuthClient(auth.Token, sdkOptions...)
	case TokenTypeJob:
		rawClient, err = gitlab.NewJobClient(auth.Token, sdkOptions...)
	case TokenTypePrivate:
		fallthrough
	default:
		rawClient, err = gitlab.NewClient(auth.Token, sdkOptions...)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create gitlab sdk client: %w", err)
	}

	c.raw = rawClient

	// Wire default service adapters if not overridden
	if c.projects == nil {
		c.projects = &defaultProjectsService{client: rawClient}
	}
	if c.groups == nil {
		c.groups = &defaultGroupsService{client: rawClient}
	}
	if c.protectedBranches == nil {
		c.protectedBranches = &defaultProtectedBranchesService{client: rawClient}
	}
	if c.pushRules == nil {
		c.pushRules = &defaultPushRulesService{client: rawClient}
	}
	if c.approvalRules == nil {
		c.approvalRules = &defaultApprovalRulesService{client: rawClient}
	}
	if c.variables == nil {
		c.variables = &defaultVariablesService{client: rawClient}
	}
	if c.runners == nil {
		c.runners = &defaultRunnersService{client: rawClient}
	}
	if c.compliance == nil {
		c.compliance = &defaultComplianceService{
			httpClient: c.httpClient,
			baseURL:    auth.BaseURL,
			token:      auth.Token,
			tokenType:  auth.TokenType,
		}
	}
	if c.webhooks == nil {
		c.webhooks = &defaultWebhooksService{client: rawClient}
	}
	if c.members == nil {
		c.members = &defaultMembersService{client: rawClient}
	}
	if c.users == nil {
		c.users = &defaultUsersService{client: rawClient}
	}

	return c, nil
}

// NewClientFromConfig resolves credentials and instantiates a new Client.
func NewClientFromConfig(cfg *config.GitLabSettingsConfig, lookup ...EnvLookupFunc) (*Client, error) {
	auth, err := ResolveAuth(cfg, lookup...)
	if err != nil {
		return nil, err
	}

	transportCfg := DefaultGovernorTransportConfig()
	timeout := 30 * time.Second
	if cfg != nil {
		if cfg.RateLimitRPS > 0 {
			transportCfg.RateLimitRPS = cfg.RateLimitRPS
		}
		if cfg.RateLimitBurst > 0 {
			transportCfg.RateLimitBurst = cfg.RateLimitBurst
		}
		if cfg.MaxRetries >= 0 {
			transportCfg.MaxRetries = cfg.MaxRetries
		}
		if cfg.RetryBaseDelayMs > 0 {
			transportCfg.BaseBackoff = time.Duration(cfg.RetryBaseDelayMs) * time.Millisecond
		}
		if cfg.RetryMaxDelayMs > 0 {
			transportCfg.MaxBackoff = time.Duration(cfg.RetryMaxDelayMs) * time.Millisecond
		}
		if cfg.TimeoutSeconds > 0 {
			timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
		}
	}

	transport := NewGovernorTransport(transportCfg)
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return NewClient(auth, WithHTTPClient(httpClient))
}

// Accessor methods implementing GitLabClient interface:

func (c *Client) Projects() ProjectsService                   { return c.projects }
func (c *Client) Groups() GroupsService                       { return c.groups }
func (c *Client) ProtectedBranches() ProtectedBranchesService { return c.protectedBranches }
func (c *Client) PushRules() PushRulesService                 { return c.pushRules }
func (c *Client) ApprovalRules() ApprovalRulesService         { return c.approvalRules }
func (c *Client) Variables() VariablesService                 { return c.variables }
func (c *Client) Runners() RunnersService                     { return c.runners }
func (c *Client) Compliance() ComplianceService               { return c.compliance }
func (c *Client) Webhooks() WebhooksService                   { return c.webhooks }
func (c *Client) Members() MembersService                     { return c.members }
func (c *Client) Users() UsersService                         { return c.users }
func (c *Client) BaseURL() string                             { return c.baseURL }
func (c *Client) RawClient() *gitlab.Client                   { return c.raw }

// ----------------------------------------------------------------------------
// Default Concrete SDK Service Adapters
// ----------------------------------------------------------------------------

type defaultProjectsService struct{ client *gitlab.Client }

func (s *defaultProjectsService) GetProject(pid any, opt *gitlab.GetProjectOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Project, *gitlab.Response, error) {
	return s.client.Projects.GetProject(pid, opt, options...)
}
func (s *defaultProjectsService) EditProject(pid any, opt *gitlab.EditProjectOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Project, *gitlab.Response, error) {
	return s.client.Projects.EditProject(pid, opt, options...)
}
func (s *defaultProjectsService) ListProjects(opt *gitlab.ListProjectsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error) {
	return s.client.Projects.ListProjects(opt, options...)
}
func (s *defaultProjectsService) GetProjectPipelineRetention(pid any, options ...gitlab.RequestOptionFunc) (int, *gitlab.Response, error) {
	u := fmt.Sprintf("projects/%v", gitlab.PathEscape(fmt.Sprintf("%v", pid)))
	req, err := s.client.NewRequest(http.MethodGet, u, nil, options)
	if err != nil {
		return 0, nil, err
	}
	var proj struct {
		CIDeletePipelinesInSeconds int `json:"ci_delete_pipelines_in_seconds"`
	}
	resp, err := s.client.Do(req, &proj)
	if err != nil {
		return 0, resp, err
	}
	return proj.CIDeletePipelinesInSeconds, resp, nil
}
func (s *defaultProjectsService) SetProjectPipelineRetention(pid any, seconds int, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
	u := fmt.Sprintf("projects/%v", gitlab.PathEscape(fmt.Sprintf("%v", pid)))
	body := map[string]int{"ci_delete_pipelines_in_seconds": seconds}
	req, err := s.client.NewRequest(http.MethodPut, u, body, options)
	if err != nil {
		return nil, err
	}
	return s.client.Do(req, nil)
}

type defaultGroupsService struct{ client *gitlab.Client }

func (s *defaultGroupsService) GetGroup(gid any, opt *gitlab.GetGroupOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Group, *gitlab.Response, error) {
	return s.client.Groups.GetGroup(gid, opt, options...)
}
func (s *defaultGroupsService) ListSubgroups(gid any, opt *gitlab.ListSubGroupsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Group, *gitlab.Response, error) {
	return s.client.Groups.ListSubGroups(gid, opt, options...)
}
func (s *defaultGroupsService) ListGroupProjects(gid any, opt *gitlab.ListGroupProjectsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error) {
	return s.client.Groups.ListGroupProjects(gid, opt, options...)
}

type defaultProtectedBranchesService struct{ client *gitlab.Client }

func (s *defaultProtectedBranchesService) ListProtectedBranches(pid any, opt *gitlab.ListProtectedBranchesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProtectedBranch, *gitlab.Response, error) {
	return s.client.ProtectedBranches.ListProtectedBranches(pid, opt, options...)
}
func (s *defaultProtectedBranchesService) ProtectRepositoryBranches(pid any, opt *gitlab.ProtectRepositoryBranchesOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProtectedBranch, *gitlab.Response, error) {
	return s.client.ProtectedBranches.ProtectRepositoryBranches(pid, opt, options...)
}
func (s *defaultProtectedBranchesService) UnprotectRepositoryBranches(pid any, branch string, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
	return s.client.ProtectedBranches.UnprotectRepositoryBranches(pid, branch, options...)
}
func (s *defaultProtectedBranchesService) RequireCodeOwnerApprovals(pid any, branch string, opt *gitlab.RequireCodeOwnerApprovalsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
	return s.client.ProtectedBranches.RequireCodeOwnerApprovals(pid, branch, opt, options...)
}

type defaultPushRulesService struct{ client *gitlab.Client }

func (s *defaultPushRulesService) GetProjectPushRule(pid any, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectPushRules, *gitlab.Response, error) {
	return s.client.Projects.GetProjectPushRules(pid, options...)
}
func (s *defaultPushRulesService) AddProjectPushRule(pid any, opt *gitlab.AddProjectPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectPushRules, *gitlab.Response, error) {
	return s.client.Projects.AddProjectPushRule(pid, opt, options...)
}
func (s *defaultPushRulesService) EditProjectPushRule(pid any, opt *gitlab.EditProjectPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectPushRules, *gitlab.Response, error) {
	return s.client.Projects.EditProjectPushRule(pid, opt, options...)
}
func (s *defaultPushRulesService) GetGroupPushRule(gid any, options ...gitlab.RequestOptionFunc) (*gitlab.GroupPushRules, *gitlab.Response, error) {
	return s.client.Groups.GetGroupPushRules(gid, options...)
}
func (s *defaultPushRulesService) AddGroupPushRule(gid any, opt *gitlab.AddGroupPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupPushRules, *gitlab.Response, error) {
	return s.client.Groups.AddGroupPushRule(gid, opt, options...)
}
func (s *defaultPushRulesService) EditGroupPushRule(gid any, opt *gitlab.EditGroupPushRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupPushRules, *gitlab.Response, error) {
	return s.client.Groups.EditGroupPushRule(gid, opt, options...)
}

type defaultApprovalRulesService struct{ client *gitlab.Client }

func (s *defaultApprovalRulesService) GetApprovalConfiguration(pid any, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovals, *gitlab.Response, error) {
	return s.client.Projects.GetApprovalConfiguration(pid, options...)
}
func (s *defaultApprovalRulesService) ChangeApprovalConfiguration(pid any, opt *gitlab.ChangeApprovalConfigurationOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovals, *gitlab.Response, error) {
	return s.client.Projects.ChangeApprovalConfiguration(pid, opt, options...)
}
func (s *defaultApprovalRulesService) GetProjectApprovalRules(pid any, opt *gitlab.GetProjectApprovalRulesListsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectApprovalRule, *gitlab.Response, error) {
	return s.client.Projects.GetProjectApprovalRules(pid, opt, options...)
}
func (s *defaultApprovalRulesService) CreateProjectApprovalRule(pid any, opt *gitlab.CreateProjectLevelRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovalRule, *gitlab.Response, error) {
	return s.client.Projects.CreateProjectApprovalRule(pid, opt, options...)
}
func (s *defaultApprovalRulesService) UpdateProjectApprovalRule(pid any, rule int, opt *gitlab.UpdateProjectLevelRuleOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectApprovalRule, *gitlab.Response, error) {
	return s.client.Projects.UpdateProjectApprovalRule(pid, rule, opt, options...)
}
func (s *defaultApprovalRulesService) DeleteProjectApprovalRule(pid any, rule int, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
	return s.client.Projects.DeleteProjectApprovalRule(pid, rule, options...)
}

type defaultVariablesService struct{ client *gitlab.Client }

func (s *defaultVariablesService) ListProjectVariables(pid any, opt *gitlab.ListProjectVariablesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectVariable, *gitlab.Response, error) {
	return s.client.ProjectVariables.ListVariables(pid, opt, options...)
}
func (s *defaultVariablesService) CreateProjectVariable(pid any, opt *gitlab.CreateProjectVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error) {
	return s.client.ProjectVariables.CreateVariable(pid, opt, options...)
}
func (s *defaultVariablesService) UpdateProjectVariable(pid any, key string, opt *gitlab.UpdateProjectVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error) {
	return s.client.ProjectVariables.UpdateVariable(pid, key, opt, options...)
}
func (s *defaultVariablesService) RemoveProjectVariable(pid any, key string, opt *gitlab.RemoveProjectVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
	return s.client.ProjectVariables.RemoveVariable(pid, key, opt, options...)
}
func (s *defaultVariablesService) ListGroupVariables(gid any, opt *gitlab.ListGroupVariablesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.GroupVariable, *gitlab.Response, error) {
	return s.client.GroupVariables.ListVariables(gid, opt, options...)
}
func (s *defaultVariablesService) CreateGroupVariable(gid any, opt *gitlab.CreateGroupVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error) {
	return s.client.GroupVariables.CreateVariable(gid, opt, options...)
}
func (s *defaultVariablesService) UpdateGroupVariable(gid any, key string, opt *gitlab.UpdateGroupVariableOptions, options ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error) {
	return s.client.GroupVariables.UpdateVariable(gid, key, opt, options...)
}
func (s *defaultVariablesService) RemoveGroupVariable(gid any, key string, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
	return s.client.GroupVariables.RemoveVariable(gid, key, options...)
}

type defaultRunnersService struct{ client *gitlab.Client }

func (s *defaultRunnersService) ListProjectRunners(pid any, opt *gitlab.ListProjectRunnersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Runner, *gitlab.Response, error) {
	return s.client.Runners.ListProjectRunners(pid, opt, options...)
}
func (s *defaultRunnersService) ListGroupsRunners(gid any, opt *gitlab.ListGroupsRunnersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Runner, *gitlab.Response, error) {
	return s.client.Runners.ListGroupsRunners(gid, opt, options...)
}
func (s *defaultRunnersService) GetRunnerDetails(rid any, options ...gitlab.RequestOptionFunc) (*gitlab.RunnerDetails, *gitlab.Response, error) {
	return s.client.Runners.GetRunnerDetails(rid, options...)
}
func (s *defaultRunnersService) UpdateRunnerDetails(rid any, opt *gitlab.UpdateRunnerDetailsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.RunnerDetails, *gitlab.Response, error) {
	return s.client.Runners.UpdateRunnerDetails(rid, opt, options...)
}

type defaultComplianceService struct {
	httpClient *http.Client
	baseURL    string
	token      string
	tokenType  TokenType
}

func (s *defaultComplianceService) GetProjectComplianceFrameworks(ctx context.Context, projectID int) ([]ComplianceFramework, error) {
	query := fmt.Sprintf(`{"query": "{ project(fullPath: \"%d\") { complianceFrameworks { nodes { id name description color default } } } }"}`, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/graphql", bytes.NewBufferString(query))
	if err != nil {
		return nil, err
	}
	s.setAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graphql compliance query returned status %d", resp.StatusCode)
	}

	var res struct {
		Data struct {
			Project struct {
				ComplianceFrameworks struct {
					Nodes []ComplianceFramework `json:"nodes"`
				} `json:"complianceFrameworks"`
			} `json:"project"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res.Data.Project.ComplianceFrameworks.Nodes, nil
}

func (s *defaultComplianceService) SetProjectComplianceFramework(ctx context.Context, projectID int, frameworkID string) error {
	mutation := fmt.Sprintf(`{"query": "mutation { projectSetComplianceFramework(input: { projectId: \"gid://gitlab/Project/%d\", complianceFrameworkId: \"%s\" }) { clientMutationId errors } }"}`, projectID, frameworkID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/graphql", bytes.NewBufferString(mutation))
	if err != nil {
		return err
	}
	s.setAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set compliance framework failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (s *defaultComplianceService) RemoveProjectComplianceFramework(ctx context.Context, projectID int, frameworkID string) error {
	mutation := fmt.Sprintf(`{"query": "mutation { projectSetComplianceFramework(input: { projectId: \"gid://gitlab/Project/%d\", complianceFrameworkId: null }) { clientMutationId errors } }"}`, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/graphql", bytes.NewBufferString(mutation))
	if err != nil {
		return err
	}
	s.setAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove compliance framework failed (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (s *defaultComplianceService) setAuthHeader(req *http.Request) {
	switch s.tokenType {
	case TokenTypeOAuth:
		req.Header.Set("Authorization", "Bearer "+s.token)
	case TokenTypeJob:
		req.Header.Set("JOB-TOKEN", s.token)
	case TokenTypePrivate:
		fallthrough
	default:
		req.Header.Set("PRIVATE-TOKEN", s.token)
	}
}

type defaultWebhooksService struct{ client *gitlab.Client }

func (s *defaultWebhooksService) ListProjectHooks(pid any, opt *gitlab.ListProjectHooksOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectHook, *gitlab.Response, error) {
	return s.client.Projects.ListProjectHooks(pid, opt, options...)
}
func (s *defaultWebhooksService) AddProjectHook(pid any, opt *gitlab.AddProjectHookOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectHook, *gitlab.Response, error) {
	return s.client.Projects.AddProjectHook(pid, opt, options...)
}
func (s *defaultWebhooksService) EditProjectHook(pid any, hook int, opt *gitlab.EditProjectHookOptions, options ...gitlab.RequestOptionFunc) (*gitlab.ProjectHook, *gitlab.Response, error) {
	return s.client.Projects.EditProjectHook(pid, hook, opt, options...)
}
func (s *defaultWebhooksService) DeleteProjectHook(pid any, hook int, options ...gitlab.RequestOptionFunc) (*gitlab.Response, error) {
	return s.client.Projects.DeleteProjectHook(pid, hook, options...)
}

type defaultMembersService struct{ client *gitlab.Client }

func (s *defaultMembersService) ListProjectMembers(pid any, opt *gitlab.ListProjectMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectMember, *gitlab.Response, error) {
	return s.client.ProjectMembers.ListProjectMembers(pid, opt, options...)
}
func (s *defaultMembersService) ListAllProjectMembers(pid any, opt *gitlab.ListProjectMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.ProjectMember, *gitlab.Response, error) {
	return s.client.ProjectMembers.ListAllProjectMembers(pid, opt, options...)
}
func (s *defaultMembersService) ListGroupMembers(gid any, opt *gitlab.ListGroupMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.GroupMember, *gitlab.Response, error) {
	return s.client.Groups.ListGroupMembers(gid, opt, options...)
}
func (s *defaultMembersService) ListAllGroupMembers(gid any, opt *gitlab.ListGroupMembersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.GroupMember, *gitlab.Response, error) {
	return s.client.Groups.ListAllGroupMembers(gid, opt, options...)
}

type defaultUsersService struct{ client *gitlab.Client }

func (s *defaultUsersService) ListUsers(opt *gitlab.ListUsersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
	return s.client.Users.ListUsers(opt, options...)
}
func (s *defaultUsersService) GetUser(user int, opt gitlab.GetUsersOptions, options ...gitlab.RequestOptionFunc) (*gitlab.User, *gitlab.Response, error) {
	return s.client.Users.GetUser(user, opt, options...)
}
