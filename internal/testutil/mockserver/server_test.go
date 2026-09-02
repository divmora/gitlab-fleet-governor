package mockserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func TestMockGitLabServer_EndpointsComprehensive(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.Client()
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("Projects.ListProjects", func(t *testing.T) {
		projects, resp, err := client.Projects.ListProjects(&gitlab.ListProjectsOptions{})
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.GreaterOrEqual(t, len(projects), 3)
	})

	t.Run("Projects.GetProject by ID and by Path", func(t *testing.T) {
		p1, resp, err := client.Projects.GetProject(101, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "fleet-governor", p1.Name)

		p2, resp, err := client.Projects.GetProject("platform/fleet-governor", nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 101, p2.ID)
	})

	t.Run("Projects.EditProject", func(t *testing.T) {
		opt := &gitlab.EditProjectOptions{
			DefaultBranch: gitlab.Ptr("develop"),
			SquashOption:  gitlab.Ptr(gitlab.SquashOptionAlways),
		}
		p, resp, err := client.Projects.EditProject(101, opt)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "develop", p.DefaultBranch)
		assert.Equal(t, gitlab.SquashOptionAlways, p.SquashOption)
	})

	t.Run("Projects.PushRules", func(t *testing.T) {
		// Existing push rule for 101
		pr, resp, err := client.Projects.GetProjectPushRules(101)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, pr.PreventSecrets)

		// 404 for unconfigured project 102
		_, resp, err = client.Projects.GetProjectPushRules(102)
		assert.Error(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		// Add push rule to 102
		newPR, resp, err := client.Projects.AddProjectPushRule(102, &gitlab.AddProjectPushRuleOptions{
			AuthorEmailRegex: gitlab.Ptr(`@example\.org$`),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, `@example\.org$`, newPR.AuthorEmailRegex)
	})

	t.Run("ProtectedBranches", func(t *testing.T) {
		branches, resp, err := client.ProtectedBranches.ListProtectedBranches(101, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, branches, 1)
		assert.Equal(t, "main", branches[0].Name)

		// Protect a new branch
		newBranch, resp, err := client.ProtectedBranches.ProtectRepositoryBranches(101, &gitlab.ProtectRepositoryBranchesOptions{
			Name: gitlab.Ptr("release/*"),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, "release/*", newBranch.Name)

		// Unprotect
		resp, err = client.ProtectedBranches.UnprotectRepositoryBranches(101, "release/*")
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("ApprovalRules", func(t *testing.T) {
		rules, resp, err := client.Projects.GetProjectApprovalRules(101, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, rules, 1)
		assert.Equal(t, "Security Review", rules[0].Name)

		// Create approval rule
		newRule, resp, err := client.Projects.CreateProjectApprovalRule(101, &gitlab.CreateProjectLevelRuleOptions{
			Name:              gitlab.Ptr("Architecture Review"),
			ApprovalsRequired: gitlab.Ptr(2),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, "Architecture Review", newRule.Name)

		// Delete approval rule
		resp, err = client.Projects.DeleteProjectApprovalRule(101, newRule.ID)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("Variables.ProjectAndGroup", func(t *testing.T) {
		// Project variables
		pVars, resp, err := client.ProjectVariables.ListVariables(101, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, pVars, 1)
		assert.Equal(t, "AWS_REGION", pVars[0].Key)

		// Create project variable
		newVar, resp, err := client.ProjectVariables.CreateVariable(101, &gitlab.CreateProjectVariableOptions{
			Key:   gitlab.Ptr("DATABASE_URL"),
			Value: gitlab.Ptr("postgres://localhost:5432"),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, "DATABASE_URL", newVar.Key)

		// Group variables
		gVars, resp, err := client.GroupVariables.ListVariables(10, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, gVars, 1)
		assert.Equal(t, "ORGANIZATION_NAME", gVars[0].Key)
	})

	t.Run("Groups.HierarchyAndSubgroups", func(t *testing.T) {
		groups, resp, err := client.Groups.ListGroups(nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.GreaterOrEqual(t, len(groups), 2)

		// Subgroups of 10
		subgroups, resp, err := client.Groups.ListSubGroups(10, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, subgroups, 1)
		assert.Equal(t, 11, subgroups[0].ID)

		// Group projects of 10
		gProjects, resp, err := client.Groups.ListGroupProjects(10, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, gProjects, 1)
		assert.Equal(t, 101, gProjects[0].ID)
	})

	t.Run("Runners", func(t *testing.T) {
		runners, resp, err := client.Runners.ListProjectRunners(101, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.GreaterOrEqual(t, len(runners), 1)

		details, resp, err := client.Runners.GetRunnerDetails(1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "shared-runner-01", details.Description)
	})

	t.Run("Hooks", func(t *testing.T) {
		hooks, resp, err := client.Projects.ListProjectHooks(101, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, hooks, 1)
		assert.Equal(t, "https://webhook.fleetcorp.com/events", hooks[0].URL)
	})

	t.Run("Members", func(t *testing.T) {
		pMembers, resp, err := client.ProjectMembers.ListProjectMembers(101, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, pMembers, 1)
		assert.Equal(t, "alice", pMembers[0].Username)

		gMembers, resp, err := client.Groups.ListGroupMembers(10, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, gMembers, 1)
		assert.Equal(t, "carol", gMembers[0].Username)
	})

	t.Run("Users", func(t *testing.T) {
		users, resp, err := client.Users.ListUsers(&gitlab.ListUsersOptions{
			Username: gitlab.Ptr("bob"),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, users, 1)
		assert.Equal(t, 2, users[0].ID)

		user, resp, err := client.Users.GetUser(1, gitlab.GetUsersOptions{})
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "alice", user.Username)
	})

	t.Run("ComplianceFrameworkGraphQL", func(t *testing.T) {
		govClient, err := srv.GovernorClient()
		require.NoError(t, err)

		// Query compliance framework
		frameworks, err := govClient.Compliance().GetProjectComplianceFrameworks(ctx, 101)
		require.NoError(t, err)
		require.Len(t, frameworks, 1)
		assert.Equal(t, "SOC2", frameworks[0].Name)

		// Set new framework
		err = govClient.Compliance().SetProjectComplianceFramework(ctx, 101, "gid://gitlab/ComplianceManagement::Framework/2")
		require.NoError(t, err)

		// Remove framework
		err = govClient.Compliance().RemoveProjectComplianceFramework(ctx, 101, "")
		require.NoError(t, err)

		frameworksAfter, err := govClient.Compliance().GetProjectComplianceFrameworks(ctx, 101)
		require.NoError(t, err)
		assert.Empty(t, frameworksAfter)
	})
}

func TestMockGitLabServer_FaultInjectionAndRetries(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	// Inject 429 on projects endpoint for 2 attempts with 1s Retry-After
	srv.Faults().Inject429("GET", "/api/v4/projects/101", 2, 1)

	govClient, err := srv.GovernorClient()
	require.NoError(t, err)

	// High-level Governor client has GovernorTransport retry enabled
	p, resp, err := govClient.Projects().GetProject(101, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "fleet-governor", p.Name)
}

func TestMockGitLabServer_KeysetPagination(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	// Add 5 projects
	for i := 1; i <= 5; i++ {
		srv.State().AddProject(&gitlab.Project{
			ID:   i,
			Name: "proj",
		})
	}

	client, err := srv.Client()
	require.NoError(t, err)

	paginationStr := "keyset"
	opt := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage:    2,
			Pagination: paginationStr,
		},
	}

	// First page
	projects, resp, err := client.Projects.ListProjects(opt)
	require.NoError(t, err)
	assert.Len(t, projects, 2)
	assert.Equal(t, 1, projects[0].ID)
	assert.Equal(t, 2, projects[1].ID)
	assert.Contains(t, resp.Header.Get("Link"), `rel="next"`)
}

func TestMockGitLabServer_CircularSubgroups(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.SeedCircularSubgroups()

	client, err := srv.Client()
	require.NoError(t, err)

	// 500 has subgroup 501
	subs500, _, err := client.Groups.ListSubGroups(500, nil)
	require.NoError(t, err)
	require.Len(t, subs500, 1)
	assert.Equal(t, 501, subs500[0].ID)

	// 501 has subgroup 502
	subs501, _, err := client.Groups.ListSubGroups(501, nil)
	require.NoError(t, err)
	require.Len(t, subs501, 1)
	assert.Equal(t, 502, subs501[0].ID)

	// 502 has subgroup 500 (cycle!)
	subs502, _, err := client.Groups.ListSubGroups(502, nil)
	require.NoError(t, err)
	require.Len(t, subs502, 1)
	assert.Equal(t, 500, subs502[0].ID)
}

func TestMockGitLabServer_RateLimitHeaders(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	resetTime := time.Now().Add(60 * time.Second)
	srv.Faults().SetRateLimitHeaders(2000, 1999, resetTime)

	client, err := srv.Client()
	require.NoError(t, err)

	_, resp, err := client.Projects.GetProject(101, nil)
	require.NoError(t, err)
	assert.Equal(t, "2000", resp.Header.Get("RateLimit-Limit"))
	assert.Equal(t, "1999", resp.Header.Get("RateLimit-Remaining"))
}
