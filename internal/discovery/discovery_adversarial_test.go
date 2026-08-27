package discovery_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestDiscoverFleet_AdversarialHighVolumeStreaming(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	// Seed 20 groups and 10 projects per group = 200 projects
	for gID := 1; gID <= 20; gID++ {
		g := srv.State().AddGroup(&gitlab.Group{
			ID:       gID,
			Name:     fmt.Sprintf("Group-%d", gID),
			Path:     fmt.Sprintf("group-%d", gID),
			FullPath: fmt.Sprintf("org/group-%d", gID),
		})
		for pID := 1; pID <= 10; pID++ {
			globalPID := gID*100 + pID
			p := srv.State().AddProject(&gitlab.Project{
				ID:                globalPID,
				Name:              fmt.Sprintf("project-%d", globalPID),
				Path:              fmt.Sprintf("project-%d", globalPID),
				PathWithNamespace: fmt.Sprintf("org/group-%d/project-%d", gID, globalPID),
				Visibility:        gitlab.PrivateVisibility,
				Archived:          false,
				Namespace: &gitlab.ProjectNamespace{
					FullPath: fmt.Sprintf("org/group-%d", gID),
				},
			})
			srv.State().AddGroupProject(g.ID, p.ID)
		}
	}

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	recursiveFalse := false
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupPathsInclude: []string{"org/group-1", "org/group-2", "org/group-3"},
			Recursive:         &recursiveFalse,
		},
	}

	fleet, err := discovery.DiscoverFleet(context.Background(), client, targets, discovery.WithConcurrency(5))
	require.NoError(t, err)
	require.NotNil(t, fleet)

	assert.Len(t, fleet.Groups, 3)
	assert.Len(t, fleet.Projects, 30)
	assert.Equal(t, 3, fleet.MatchedGroupsCount)
	assert.Equal(t, 30, fleet.MatchedProjectsCount)

	// Confirm all projects have correct parent group
	for _, p := range fleet.Projects {
		assert.Contains(t, []int{1, 2, 3}, p.ParentGroupID)
	}
}

func TestDiscoverFleet_AdversarialCircularSubgroupsWithProjects(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.SeedCircularSubgroups() // 500 -> 501 -> 502 -> 500

	// Seed projects attached to circular groups
	p500 := srv.State().AddProject(&gitlab.Project{
		ID:                5001,
		Name:              "proj-500",
		Path:              "proj-500",
		PathWithNamespace: "circ-root/proj-500",
		Visibility:        gitlab.InternalVisibility,
	})
	p501 := srv.State().AddProject(&gitlab.Project{
		ID:                5011,
		Name:              "proj-501",
		Path:              "proj-501",
		PathWithNamespace: "circ-root/child-a/proj-501",
		Visibility:        gitlab.InternalVisibility,
	})
	p502 := srv.State().AddProject(&gitlab.Project{
		ID:                5021,
		Name:              "proj-502",
		Path:              "proj-502",
		PathWithNamespace: "circ-root/child-a/child-b/proj-502",
		Visibility:        gitlab.InternalVisibility,
	})

	srv.State().AddGroupProject(500, p500.ID)
	srv.State().AddGroupProject(501, p501.ID)
	srv.State().AddGroupProject(502, p502.ID)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	recursiveTrue := true
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupIDsInclude: []int{500},
			Recursive:       &recursiveTrue,
		},
	}

	fleet, err := discovery.DiscoverFleet(ctx, client, targets)
	require.NoError(t, err)
	require.NotNil(t, fleet)

	// Must discover exactly 3 groups and 3 projects with zero duplicates
	assert.Len(t, fleet.Groups, 3)
	assert.ElementsMatch(t, []int{500, 501, 502}, fleet.GroupIDs())

	assert.Len(t, fleet.Projects, 3)
	assert.ElementsMatch(t, []int{5001, 5011, 5021}, fleet.ProjectIDs())
}

func TestDiscoverFleet_AdversarialSubgroupExclusionFiltering(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	// Hierarchy: Group 100 -> Subgroup 101 -> Subgroup 102
	g100 := srv.State().AddGroup(&gitlab.Group{ID: 100, Name: "Root", FullPath: "company"})
	g101 := srv.State().AddGroup(&gitlab.Group{ID: 101, Name: "Excluded Child", FullPath: "company/excluded", ParentID: 100})
	g102 := srv.State().AddGroup(&gitlab.Group{ID: 102, Name: "Allowed Leaf", FullPath: "company/allowed", ParentID: 100})

	srv.State().AddSubgroup(100, 101)
	srv.State().AddSubgroup(100, 102)

	p100 := srv.State().AddProject(&gitlab.Project{ID: 1, Name: "root-p", PathWithNamespace: "company/root-p"})
	p101 := srv.State().AddProject(&gitlab.Project{ID: 2, Name: "exc-p", PathWithNamespace: "company/excluded/exc-p"})
	p102 := srv.State().AddProject(&gitlab.Project{ID: 3, Name: "all-p", PathWithNamespace: "company/allowed/all-p"})

	srv.State().AddGroupProject(g100.ID, p100.ID)
	srv.State().AddGroupProject(g101.ID, p101.ID)
	srv.State().AddGroupProject(g102.ID, p102.ID)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	recursiveTrue := true
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupIDsInclude:   []int{100},
			GroupPathsExclude: []string{"company/excluded"},
			Recursive:         &recursiveTrue,
		},
	}

	fleet, err := discovery.DiscoverFleet(context.Background(), client, targets)
	require.NoError(t, err)

	assert.Len(t, fleet.Groups, 2)
	assert.Contains(t, fleet.Groups, 100)
	assert.Contains(t, fleet.Groups, 102)
	assert.NotContains(t, fleet.Groups, 101)

	// Since 101 is excluded from matched groups, its projects should not be scanned in group-scoped discovery
	assert.Contains(t, fleet.Projects, 1)
	assert.Contains(t, fleet.Projects, 3)
	assert.NotContains(t, fleet.Projects, 2)
}

func TestDiscoverFleet_AdversarialFaultInjection(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	// Inject 500 error on project list endpoint
	srv.Faults().AddRule(mockserver.FaultRule{
		PathPrefix: "/api/v4/projects",
		StatusCode: http.StatusInternalServerError,
		Count:      10, // Fail consecutive retries to trigger final error
	})

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	// Context with timeout to avoid lingering retry loops
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = discovery.DiscoverFleet(ctx, client, config.TargetSelectors{})
	require.Error(t, err)
}

func TestDiscoverFleet_AdversarialEmptyMatchAndZeroState(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	targets := config.TargetSelectors{
		ProjectSelector: &config.ProjectSelector{
			ProjectNameRegexInclude: "^non-existent-pattern-[0-9]+$",
		},
	}

	fleet, err := discovery.DiscoverFleet(context.Background(), client, targets)
	require.NoError(t, err)
	require.NotNil(t, fleet)

	assert.True(t, fleet.IsEmpty())
	assert.Empty(t, fleet.ProjectList())
	assert.Empty(t, fleet.GroupList())
	assert.Empty(t, fleet.ProjectIDs())
	assert.Empty(t, fleet.GroupIDs())
	assert.Equal(t, 0, fleet.MatchedProjectsCount)
	assert.Equal(t, 0, fleet.MatchedGroupsCount)
	assert.Greater(t, fleet.ScannedProjectsCount, 0)
}

func TestDiscoverFleet_AdversarialNamespaceBoundaryAndCasing(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	// Similar namespace prefix names: "core" vs "core-services"
	srv.State().AddProject(&gitlab.Project{
		ID:                1,
		Name:              "p1",
		PathWithNamespace: "CORE/APP/p1",
		Namespace:         &gitlab.ProjectNamespace{FullPath: "CORE/APP"},
	})
	srv.State().AddProject(&gitlab.Project{
		ID:                2,
		Name:              "p2",
		PathWithNamespace: "core-services/app/p2",
		Namespace:         &gitlab.ProjectNamespace{FullPath: "core-services/app"},
	})

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	targets := config.TargetSelectors{
		ProjectSelector: &config.ProjectSelector{
			NamespacesInclude: []string{"core"},
		},
	}

	fleet, err := discovery.DiscoverFleet(context.Background(), client, targets)
	require.NoError(t, err)

	assert.Len(t, fleet.Projects, 1)
	assert.Contains(t, fleet.Projects, 1)
	assert.NotContains(t, fleet.Projects, 2)
}
