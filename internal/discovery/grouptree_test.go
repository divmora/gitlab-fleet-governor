package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestGroupSelectorEvaluator_Inclusions(t *testing.T) {
	sel := &config.GroupSelector{
		GroupIDsInclude:   []int{10, 20},
		GroupPathsInclude: []string{"platform/core", "infra/networking"},
	}
	eval := discovery.NewGroupSelectorEvaluator(sel)

	assert.True(t, eval.IsRecursive())

	tests := []struct {
		name     string
		group    *gitlab.Group
		expected bool
	}{
		{
			name:     "Matches included ID",
			group:    &gitlab.Group{ID: 10, FullPath: "other/group"},
			expected: true,
		},
		{
			name:     "Matches included path",
			group:    &gitlab.Group{ID: 99, FullPath: "platform/core"},
			expected: true,
		},
		{
			name:     "Matches included path case-insensitively",
			group:    &gitlab.Group{ID: 98, FullPath: "Platform/Core"},
			expected: true,
		},
		{
			name:     "Does not match unlisted ID and path",
			group:    &gitlab.Group{ID: 30, FullPath: "security/tools"},
			expected: false,
		},
		{
			name:     "Nil group returns false",
			group:    nil,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, eval.Matches(tc.group))
		})
	}
}

func TestGroupSelectorEvaluator_ExclusionPrecedence(t *testing.T) {
	sel := &config.GroupSelector{
		GroupIDsInclude:   []int{10, 20, 30},
		GroupIDsExclude:   []int{20},
		GroupPathsInclude: []string{"platform/core", "platform/sandbox"},
		GroupPathsExclude: []string{"platform/sandbox"},
	}
	eval := discovery.NewGroupSelectorEvaluator(sel)

	// Included and not excluded
	assert.True(t, eval.Matches(&gitlab.Group{ID: 10, FullPath: "platform"}))
	assert.True(t, eval.Matches(&gitlab.Group{ID: 11, FullPath: "platform/core"}))

	// Included ID but excluded ID (exclusion strictly wins)
	assert.False(t, eval.Matches(&gitlab.Group{ID: 20, FullPath: "platform/billing"}))

	// Included path but excluded path (exclusion strictly wins)
	assert.False(t, eval.Matches(&gitlab.Group{ID: 99, FullPath: "platform/sandbox"}))

	// Included ID but excluded path
	assert.False(t, eval.Matches(&gitlab.Group{ID: 30, FullPath: "platform/sandbox"}))
}

func TestGroupSelectorEvaluator_EmptySelector(t *testing.T) {
	t.Run("NilSelector", func(t *testing.T) {
		eval := discovery.NewGroupSelectorEvaluator(nil)
		assert.True(t, eval.IsRecursive())
		assert.True(t, eval.Matches(&gitlab.Group{ID: 1, FullPath: "any/group"}))
	})

	t.Run("EmptyStruct", func(t *testing.T) {
		eval := discovery.NewGroupSelectorEvaluator(&config.GroupSelector{})
		assert.True(t, eval.IsRecursive())
		assert.True(t, eval.Matches(&gitlab.Group{ID: 1, FullPath: "any/group"}))
	})

	t.Run("NonRecursiveToggle", func(t *testing.T) {
		recursiveFalse := false
		eval := discovery.NewGroupSelectorEvaluator(&config.GroupSelector{
			Recursive: &recursiveFalse,
		})
		assert.False(t, eval.IsRecursive())
		assert.True(t, eval.Matches(&gitlab.Group{ID: 1, FullPath: "any/group"}))
	})
}

func TestGroupSelectorEvaluator_PathNormalization(t *testing.T) {
	sel := &config.GroupSelector{
		GroupPathsInclude: []string{"/Platform/Core/"},
		GroupPathsExclude: []string{"/Platform/Sandbox/"},
	}
	eval := discovery.NewGroupSelectorEvaluator(sel)

	assert.True(t, eval.Matches(&gitlab.Group{ID: 1, FullPath: "platform/core"}))
	assert.True(t, eval.Matches(&gitlab.Group{ID: 2, FullPath: "/Platform/Core"}))
	assert.False(t, eval.Matches(&gitlab.Group{ID: 3, FullPath: "platform/sandbox"}))
	assert.False(t, eval.Matches(&gitlab.Group{ID: 4, FullPath: "/Platform/Sandbox/"}))
}

func TestBFSGroupTraverser_StandardHierarchy(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed() // Seeds: 10 (platform), 11 (platform/core, parent 10), 20 (security)

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	traverser := discovery.NewBFSGroupTraverser(client)
	ctx := context.Background()

	groups, err := traverser.Traverse(ctx, nil)
	require.NoError(t, err)
	require.Len(t, groups, 3)

	groupMap := make(map[int]*discovery.DiscoveredGroup)
	for _, g := range groups {
		groupMap[g.ID] = g
	}

	assert.Contains(t, groupMap, 10)
	assert.Contains(t, groupMap, 11)
	assert.Contains(t, groupMap, 20)

	// Check depth and parentage
	assert.Equal(t, 0, groupMap[10].Depth)
	assert.Equal(t, 10, groupMap[10].RootParentID)

	assert.Equal(t, 1, groupMap[11].Depth)
	assert.Equal(t, 10, groupMap[11].ParentID)
	assert.Equal(t, 10, groupMap[11].RootParentID)

	assert.Equal(t, 0, groupMap[20].Depth)
	assert.Equal(t, 20, groupMap[20].RootParentID)
}

func TestBFSGroupTraverser_NonRecursive(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	traverser := discovery.NewBFSGroupTraverser(client)
	ctx := context.Background()

	recursiveFalse := false
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		Recursive: &recursiveFalse,
	})
	require.NoError(t, err)
	require.Len(t, groups, 2)

	ids := make([]int, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
		assert.Equal(t, 0, g.Depth)
	}

	assert.ElementsMatch(t, []int{10, 20}, ids)
}

func TestBFSGroupTraverser_IncludeSpecificGroup(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	traverser := discovery.NewBFSGroupTraverser(client)
	ctx := context.Background()

	t.Run("IncludeByIDWithSubgroups", func(t *testing.T) {
		groups, err := traverser.Traverse(ctx, &config.GroupSelector{
			GroupIDsInclude: []int{10},
		})
		require.NoError(t, err)
		require.Len(t, groups, 2)

		ids := make([]int, 0, len(groups))
		for _, g := range groups {
			ids = append(ids, g.ID)
		}
		assert.ElementsMatch(t, []int{10, 11}, ids)
	})

	t.Run("IncludeByPath", func(t *testing.T) {
		groups, err := traverser.Traverse(ctx, &config.GroupSelector{
			GroupPathsInclude: []string{"security"},
		})
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Equal(t, 20, groups[0].ID)
		assert.Equal(t, "security", groups[0].FullPath)
	})
}

func TestBFSGroupTraverser_ExcludeSubgroup(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	traverser := discovery.NewBFSGroupTraverser(client)
	ctx := context.Background()

	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupPathsExclude: []string{"platform/core"},
	})
	require.NoError(t, err)
	require.Len(t, groups, 2)

	ids := make([]int, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	assert.ElementsMatch(t, []int{10, 20}, ids)
}

func TestBFSGroupTraverser_CycleDetection(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.SeedCircularSubgroups() // Cycle: 500 -> 501 -> 502 -> 500

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	startTime := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{500},
	})
	elapsed := time.Since(startTime)

	require.NoError(t, err)
	assert.Less(t, elapsed, 500*time.Millisecond, "cycle detection must terminate promptly without looping")

	// Must discover exactly groups 500, 501, 502 without infinite duplication
	require.Len(t, groups, 3)

	ids := make([]int, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	assert.ElementsMatch(t, []int{500, 501, 502}, ids)
}

func TestBFSGroupTraverser_ContextCancellation(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately

	_, err = traverser.Traverse(ctx, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestBFSGroupTraverser_InvalidGroupID(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	traverser := discovery.NewBFSGroupTraverser(client)

	_, err = traverser.Traverse(context.Background(), &config.GroupSelector{
		GroupIDsInclude: []int{99999},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get group by ID 99999")
}
