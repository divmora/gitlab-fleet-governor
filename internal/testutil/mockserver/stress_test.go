package mockserver_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ----------------------------------------------------------------------------
// Challenge 9: Mock Server High Concurrency with Dynamic Fault Injection
// ----------------------------------------------------------------------------
func TestAdversarial_MockServerConcurrentLoadWithFaults(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()
	server.Seed()

	// Inject 429 faults for projects endpoint
	server.Faults().Inject429("GET", "/api/v4/projects", 5, 1)

	// Create GovernorTransport with high concurrency
	transportCfg := gl.GovernorTransportConfig{
		BaseTransport:  server.HTTPClient().Transport,
		RateLimitRPS:   200,
		RateLimitBurst: 200,
		MaxRetries:     5,
		BaseBackoff:    10 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
		JitterRatio:    0.10,
	}
	transport := gl.NewGovernorTransport(transportCfg)
	httpClient := &http.Client{Transport: transport}

	governorClient, err := server.GovernorClient(gl.WithHTTPClient(httpClient))
	require.NoError(t, err)

	const numWorkers = 30
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*10)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ctx := context.Background()

			// 1. Projects operations
			projects, _, err := governorClient.Projects().ListProjects(&gitlab.ListProjectsOptions{})
			if err != nil {
				errCh <- fmt.Errorf("worker %d ListProjects failed: %w", workerID, err)
				return
			}
			if len(projects) == 0 {
				errCh <- fmt.Errorf("worker %d expected projects, got 0", workerID)
				return
			}

			// 2. Protected branches operations
			branches, _, err := governorClient.ProtectedBranches().ListProtectedBranches(101, &gitlab.ListProtectedBranchesOptions{})
			if err != nil {
				errCh <- fmt.Errorf("worker %d ListProtectedBranches failed: %w", workerID, err)
				return
			}
			if len(branches) == 0 {
				errCh <- fmt.Errorf("worker %d expected protected branches, got 0", workerID)
				return
			}

			// 3. Approval rules operations
			rules, _, err := governorClient.ApprovalRules().GetProjectApprovalRules(101, &gitlab.GetProjectApprovalRulesListsOptions{})
			if err != nil {
				errCh <- fmt.Errorf("worker %d GetProjectApprovalRules failed: %w", workerID, err)
				return
			}
			if len(rules) == 0 {
				errCh <- fmt.Errorf("worker %d expected approval rules, got 0", workerID)
				return
			}

			// 4. Compliance framework operations
			frameworks, err := governorClient.Compliance().GetProjectComplianceFrameworks(ctx, 101)
			if err != nil {
				errCh <- fmt.Errorf("worker %d GetProjectComplianceFrameworks failed: %w", workerID, err)
				return
			}
			if len(frameworks) == 0 {
				errCh <- fmt.Errorf("worker %d expected compliance frameworks, got 0", workerID)
				return
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}
}

// ----------------------------------------------------------------------------
// Challenge 10: Circular Subgroup Traversal Integrity
// ----------------------------------------------------------------------------
func TestAdversarial_CircularSubgroupStructure(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	// Seed circular subgroup structure: 500 -> 501 -> 502 -> 500
	server.SeedCircularSubgroups()

	client, err := server.Client()
	require.NoError(t, err)

	// Fetch subgroups of 500
	subgroups500, _, err := client.Groups.ListSubGroups(500, &gitlab.ListSubGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, subgroups500, 1)
	assert.Equal(t, 501, subgroups500[0].ID)

	// Fetch subgroups of 501
	subgroups501, _, err := client.Groups.ListSubGroups(501, &gitlab.ListSubGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, subgroups501, 1)
	assert.Equal(t, 502, subgroups501[0].ID)

	// Fetch subgroups of 502 (artificially points back to 500)
	subgroups502, _, err := client.Groups.ListSubGroups(502, &gitlab.ListSubGroupsOptions{})
	require.NoError(t, err)
	require.Len(t, subgroups502, 1)
	assert.Equal(t, 500, subgroups502[0].ID)
}
