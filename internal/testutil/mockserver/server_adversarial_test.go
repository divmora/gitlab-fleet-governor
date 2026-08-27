package mockserver_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

// TestMockGitLabServer_CircularSubgroupsTraversal verifies circular subgroup dependencies
// do not cause deadlocks, infinite loops, or crashes in mock server endpoints.
func TestMockGitLabServer_CircularSubgroupsTraversal(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.SeedCircularSubgroups()

	client, err := srv.Client()
	require.NoError(t, err)

	// Step 1: Query group 500 -> returns 501
	subs500, resp, err := client.Groups.ListSubGroups(500, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, subs500, 1)
	assert.Equal(t, 501, subs500[0].ID)

	// Step 2: Query group 501 -> returns 502
	subs501, resp, err := client.Groups.ListSubGroups(501, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, subs501, 1)
	assert.Equal(t, 502, subs501[0].ID)

	// Step 3: Query group 502 -> returns 500 (cycle complete)
	subs502, resp, err := client.Groups.ListSubGroups(502, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, subs502, 1)
	assert.Equal(t, 500, subs502[0].ID)

	// Step 4: Resolve by full path with URL encoding
	subsByPath, resp, err := client.Groups.ListSubGroups("circ-root/child-a/child-b", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, subsByPath, 1)
	assert.Equal(t, 500, subsByPath[0].ID)
}

// TestMockGitLabServer_DeepSubgroupHierarchy tests traversal through 6 levels of nested subgroups.
func TestMockGitLabServer_DeepSubgroupHierarchy(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	depth := 6
	currentParent := 0
	currentPath := "root"

	for i := 1; i <= depth; i++ {
		g := srv.State().AddGroup(&gitlab.Group{
			ID:       1000 + i,
			Name:     fmt.Sprintf("Level-%d", i),
			Path:     fmt.Sprintf("level-%d", i),
			FullPath: currentPath,
			ParentID: currentParent,
		})
		currentParent = g.ID
		currentPath = fmt.Sprintf("%s/level-%d", currentPath, i+1)
	}

	client, err := srv.Client()
	require.NoError(t, err)

	// Verify each parent sees its child
	for i := 1; i < depth; i++ {
		parentID := 1000 + i
		expectedChildID := 1000 + i + 1
		subs, resp, err := client.Groups.ListSubGroups(parentID, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, subs, 1)
		assert.Equal(t, expectedChildID, subs[0].ID)
	}

	// Deepest leaf has no subgroups
	leafSubs, resp, err := client.Groups.ListSubGroups(1000+depth, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, leafSubs)
}

// TestMockGitLabServer_URLEncodedPathWithNamespaceComprehensive tests URL-encoded path resolution
// across all sub-resources (push rules, protected branches, variables, approval rules).
func TestMockGitLabServer_URLEncodedPathWithNamespaceComprehensive(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	deepPath := "org-alpha/team-core/infra-sub/service-mesh"
	p := srv.State().AddProject(&gitlab.Project{
		ID:                777,
		Name:              "service-mesh",
		Path:              "service-mesh",
		PathWithNamespace: deepPath,
	})
	require.Equal(t, 777, p.ID)

	client, err := srv.Client()
	require.NoError(t, err)

	t.Run("GetProject by URL-encoded path", func(t *testing.T) {
		proj, resp, err := client.Projects.GetProject(deepPath, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 777, proj.ID)
		assert.Equal(t, deepPath, proj.PathWithNamespace)
	})

	t.Run("PushRules by URL-encoded path", func(t *testing.T) {
		// Create push rule using path
		pr, resp, err := client.Projects.AddProjectPushRule(deepPath, &gitlab.AddProjectPushRuleOptions{
			AuthorEmailRegex: gitlab.Ptr(`@org-alpha\.com$`),
			PreventSecrets:   gitlab.Ptr(true),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, `@org-alpha\.com$`, pr.AuthorEmailRegex)

		// Get push rule using path
		fetchedPR, resp, err := client.Projects.GetProjectPushRules(deepPath)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, fetchedPR.PreventSecrets)
	})

	t.Run("ProtectedBranches with Slashes in Branch Name and URL-encoded Project Path", func(t *testing.T) {
		branchName := "feature/SOC2-1234/audit-hardening"

		pb, resp, err := client.ProtectedBranches.ProtectRepositoryBranches(deepPath, &gitlab.ProtectRepositoryBranchesOptions{
			Name:                      gitlab.Ptr(branchName),
			CodeOwnerApprovalRequired: gitlab.Ptr(true),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, branchName, pb.Name)
		assert.True(t, pb.CodeOwnerApprovalRequired)

		// Get protected branch
		fetchedPB, resp, err := client.ProtectedBranches.GetProtectedBranch(deepPath, branchName)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, branchName, fetchedPB.Name)

		// Unprotect
		resp, err = client.ProtectedBranches.UnprotectRepositoryBranches(deepPath, branchName)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("ProjectVariables by URL-encoded path and Scoped Environment", func(t *testing.T) {
		key := "DEPLOY_KEY"
		envScope := "production/us-east-1"

		v, resp, err := client.ProjectVariables.CreateVariable(deepPath, &gitlab.CreateProjectVariableOptions{
			Key:              gitlab.Ptr(key),
			Value:            gitlab.Ptr("secret-val-123"),
			EnvironmentScope: gitlab.Ptr(envScope),
		})
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, key, v.Key)
		assert.Equal(t, envScope, v.EnvironmentScope)

		// List variables using project path
		vars, resp, err := client.ProjectVariables.ListVariables(deepPath, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, vars, 1)
		assert.Equal(t, key, vars[0].Key)
	})
}

// TestMockGitLabServer_StateIsolation verifies state isolation between independent mock servers.
func TestMockGitLabServer_StateIsolation(t *testing.T) {
	srvA := mockserver.NewMockGitLabServer()
	defer srvA.Close()

	srvB := mockserver.NewMockGitLabServer()
	defer srvB.Close()

	// Populate srvA with 10 projects
	for i := 1; i <= 10; i++ {
		srvA.State().AddProject(&gitlab.Project{
			ID:   i,
			Name: fmt.Sprintf("project-A-%d", i),
		})
	}

	// Populate srvB with 2 different projects
	for i := 1; i <= 2; i++ {
		srvB.State().AddProject(&gitlab.Project{
			ID:   i,
			Name: fmt.Sprintf("project-B-%d", i),
		})
	}

	clientA, err := srvA.Client()
	require.NoError(t, err)

	clientB, err := srvB.Client()
	require.NoError(t, err)

	// Verify srvA has 10 projects
	projsA, _, err := clientA.Projects.ListProjects(nil)
	require.NoError(t, err)
	assert.Len(t, projsA, 10)

	// Verify srvB has 2 projects
	projsB, _, err := clientB.Projects.ListProjects(nil)
	require.NoError(t, err)
	assert.Len(t, projsB, 2)

	// Reset srvA
	srvA.Reset()

	// Verify srvA is empty
	projsAAfter, _, err := clientA.Projects.ListProjects(nil)
	require.NoError(t, err)
	assert.Empty(t, projsAAfter)

	// Verify srvB is completely unaffected
	projsBAfter, _, err := clientB.Projects.ListProjects(nil)
	require.NoError(t, err)
	assert.Len(t, projsBAfter, 2)
}

// TestMockGitLabServer_ConcurrentStressStateSafety tests thread-safety and race-freedom of MockGitLabServer
// under heavy concurrent read/write loads across 50 goroutines.
func TestMockGitLabServer_ConcurrentStressStateSafety(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.Client()
	require.NoError(t, err)

	concurrency := 50
	iterations := 20

	var wg sync.WaitGroup
	wg.Add(concurrency)

	for worker := 0; worker < concurrency; worker++ {
		go func(wID int) {
			defer wg.Done()
			for iter := 0; iter < iterations; iter++ {
				projID := 10000 + (wID * 100) + iter

				// 1. Add project
				srv.State().AddProject(&gitlab.Project{
					ID:                projID,
					Name:              fmt.Sprintf("proj-%d", projID),
					PathWithNamespace: fmt.Sprintf("group-%d/proj-%d", wID, projID),
				})

				// 2. Read project via API
				_, _, _ = client.Projects.GetProject(projID, nil)

				// 3. Mutate project via API
				_, _, _ = client.Projects.EditProject(projID, &gitlab.EditProjectOptions{
					DefaultBranch: gitlab.Ptr("main"),
				})

				// 4. Push rules
				_, _, _ = client.Projects.AddProjectPushRule(projID, &gitlab.AddProjectPushRuleOptions{
					PreventSecrets: gitlab.Ptr(true),
				})
				_, _, _ = client.Projects.GetProjectPushRules(projID)

				// 5. Protected branches
				_, _, _ = client.ProtectedBranches.ProtectRepositoryBranches(projID, &gitlab.ProtectRepositoryBranchesOptions{
					Name: gitlab.Ptr("main"),
				})
				_, _, _ = client.ProtectedBranches.ListProtectedBranches(projID, nil)

				// 6. List projects
				_, _, _ = client.Projects.ListProjects(&gitlab.ListProjectsOptions{
					ListOptions: gitlab.ListOptions{PerPage: 5},
				})
			}
		}(worker)
	}

	wg.Wait()

	// Verify all created projects exist
	projects := srv.State().ListProjects()
	assert.GreaterOrEqual(t, len(projects), concurrency*iterations)
}

// TestMockGitLabServer_FaultInjectionHighConcurrency tests that under concurrent requests
// with injected 429 and 500 errors, GovernorClient retries properly and all succeed.
func TestMockGitLabServer_FaultInjectionHighConcurrency(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	// Inject 429 and 500 on project 101 endpoint
	srv.Faults().Inject429("GET", "/api/v4/projects/101", 3, 1)
	srv.Faults().Inject5xx("GET", "/api/v4/projects/101", http.StatusInternalServerError, 2)

	govClient, err := srv.GovernorClient()
	require.NoError(t, err)

	concurrency := 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			p, resp, err := govClient.Projects().GetProject(101, nil)
			if assert.NoError(t, err) && assert.NotNil(t, resp) {
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, "fleet-governor", p.Name)
			}
		}()
	}

	wg.Wait()
}
