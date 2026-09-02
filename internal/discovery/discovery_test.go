package discovery_test

import (
	"context"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestDiscoverFleet_NilClient(t *testing.T) {
	_, err := discovery.DiscoverFleet(context.Background(), nil, config.TargetSelectors{})
	require.ErrorIs(t, err, discovery.ErrNilClient)
}

func TestDiscoverFleet_InstanceWideProjectDiscovery(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Seeds projects: 101 (private), 102 (internal), 103 (public)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("FilterByVisibilityAndName", func(t *testing.T) {
		targets := config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				Visibility:              "private",
				ProjectNameRegexInclude: "^fleet-.*",
			},
		}

		fleet, err := discovery.DiscoverFleet(ctx, client, targets)
		require.NoError(t, err)
		require.NotNil(t, fleet)

		assert.Equal(t, 1, fleet.MatchedProjectsCount)
		assert.Contains(t, fleet.Projects, 101)
		assert.Equal(t, "fleet-governor", fleet.Projects[101].Name)
		assert.Equal(t, "platform/fleet-governor", fleet.Projects[101].PathWithNamespace)
	})

	t.Run("TargetFleetHelpers", func(t *testing.T) {
		targets := config.TargetSelectors{}
		fleet, err := discovery.DiscoverFleet(ctx, client, targets)
		require.NoError(t, err)

		list := fleet.ProjectList()
		assert.Len(t, list, 3)
		assert.Equal(t, []int{101, 102, 103}, fleet.ProjectIDs())
		assert.False(t, fleet.IsEmpty())
		assert.Greater(t, fleet.ScannedProjectsCount, 0)
		assert.Greater(t, fleet.Duration.Nanoseconds(), int64(0))
	})
}

func TestDiscoverFleet_GroupAndProjectDiscovery(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx := context.Background()

	recursiveTrue := true
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupPathsInclude: []string{"platform"},
			Recursive:         &recursiveTrue,
		},
		ProjectSelector: &config.ProjectSelector{
			Visibility: "internal",
		},
	}

	fleet, err := discovery.DiscoverFleet(ctx, client, targets)
	require.NoError(t, err)
	require.NotNil(t, fleet)

	// Platform group tree contains group 10 (platform) and subgroup 11 (platform/core)
	assert.Contains(t, fleet.Groups, 10)
	assert.Contains(t, fleet.Groups, 11)
	assert.Equal(t, 2, fleet.MatchedGroupsCount)

	groupList := fleet.GroupList()
	assert.Len(t, groupList, 2)
	assert.Equal(t, []int{10, 11}, fleet.GroupIDs())

	// Project 102 (cloud-infra) is internal under platform/core
	assert.Equal(t, 1, fleet.MatchedProjectsCount)
	assert.Contains(t, fleet.Projects, 102)
	assert.Equal(t, "cloud-infra", fleet.Projects[102].Name)
}

func TestDiscoverFleet_Deduplication(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	// Seed multiple groups sharing the same project ID
	g1 := srv.State().AddGroup(&gitlab.Group{ID: 1, Name: "G1", FullPath: "g1"})
	g2 := srv.State().AddGroup(&gitlab.Group{ID: 2, Name: "G2", FullPath: "g2"})
	p1 := srv.State().AddProject(&gitlab.Project{
		ID:                999,
		Name:              "shared-proj",
		Path:              "shared-proj",
		PathWithNamespace: "g1/shared-proj",
	})

	srv.State().AddGroupProject(g1.ID, p1.ID)
	srv.State().AddGroupProject(g2.ID, p1.ID)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	recursiveFalse := false
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupIDsInclude: []int{1, 2},
			Recursive:       &recursiveFalse,
		},
	}

	fleet, err := discovery.DiscoverFleet(context.Background(), client, targets)
	require.NoError(t, err)
	assert.Len(t, fleet.Projects, 1, "project 999 must appear exactly once in deduplicated fleet")
	assert.Len(t, fleet.Groups, 2)
}

func TestDiscoverFleet_ContextCancellation(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = discovery.DiscoverFleet(ctx, client, config.TargetSelectors{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestDiscoverFleet_CustomOptions(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	targets := config.TargetSelectors{}
	streamOpts := gl.DefaultStreamOptions()
	streamOpts.BufferSize = 50

	customTraverser := discovery.NewBFSGroupTraverser(client)

	fleet, err := discovery.DiscoverFleet(
		context.Background(),
		client,
		targets,
		discovery.WithConcurrency(4),
		discovery.WithStreamOptions(streamOpts),
		discovery.WithGroupTraverser(customTraverser),
	)
	require.NoError(t, err)
	assert.False(t, fleet.IsEmpty())
}
