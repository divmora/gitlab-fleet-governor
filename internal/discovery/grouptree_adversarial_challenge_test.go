package discovery_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// mockGroupsClient implements gl.GitLabClient and gl.GroupsService for in-memory graph topologies.
type mockGraphClient struct {
	gl.GitLabClient
	mu        sync.RWMutex
	groups    map[int]*gitlab.Group
	subgroups map[int][]*gitlab.Group
	topLevels []*gitlab.Group
}

func newMockGraphClient() *mockGraphClient {
	return &mockGraphClient{
		groups:    make(map[int]*gitlab.Group),
		subgroups: make(map[int][]*gitlab.Group),
	}
}

func (m *mockGraphClient) Groups() gl.GroupsService {
	return m
}

func (m *mockGraphClient) AddGroup(id int, path, fullPath string, parentID int) *gitlab.Group {
	m.mu.Lock()
	defer m.mu.Unlock()

	g := &gitlab.Group{
		ID:         id,
		Name:       path,
		Path:       path,
		FullPath:   fullPath,
		ParentID:   parentID,
		Visibility: gitlab.PrivateVisibility,
	}
	m.groups[id] = g
	if parentID == 0 {
		m.topLevels = append(m.topLevels, g)
	} else {
		m.subgroups[parentID] = append(m.subgroups[parentID], g)
	}
	return g
}

func (m *mockGraphClient) AddSubgroupLink(parentID, childID int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if child, exists := m.groups[childID]; exists {
		m.subgroups[parentID] = append(m.subgroups[parentID], child)
	}
}

func (m *mockGraphClient) GetGroup(gid any, opt *gitlab.GetGroupOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Group, *gitlab.Response, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	switch v := gid.(type) {
	case int:
		if g, exists := m.groups[v]; exists {
			return g, &gitlab.Response{}, nil
		}
	case string:
		clean := strings.Trim(v, "/")
		for _, g := range m.groups {
			if strings.EqualFold(g.FullPath, clean) || strings.EqualFold(g.Path, clean) {
				return g, &gitlab.Response{}, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("group %v not found", gid)
}

func (m *mockGraphClient) ListSubgroups(gid any, opt *gitlab.ListSubGroupsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Group, *gitlab.Response, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var id int
	switch v := gid.(type) {
	case int:
		id = v
	case string:
		for _, g := range m.groups {
			if g.FullPath == v || g.Path == v {
				id = g.ID
				break
			}
		}
	}

	children := m.subgroups[id]
	return children, &gitlab.Response{NextPage: 0}, nil
}

func (m *mockGraphClient) ListGroupProjects(gid any, opt *gitlab.ListGroupProjectsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Project, *gitlab.Response, error) {
	return nil, &gitlab.Response{NextPage: 0}, nil
}

// ----------------------------------------------------------------------------
// 1. Multi-Node Circular Subgroup Stress Tests
// ----------------------------------------------------------------------------

func TestGroupTree_AdversarialSelfLoop(t *testing.T) {
	client := newMockGraphClient()
	g1 := client.AddGroup(1, "self-loop", "org/self-loop", 0)
	client.AddSubgroupLink(g1.ID, g1.ID) // 1 -> 1

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond, "Self-loop traversal must terminate within 100ms")
	require.Len(t, groups, 1, "Self-loop group must be visited exactly once")
	assert.Equal(t, 1, groups[0].ID)
}

func TestGroupTree_AdversarialThreeNodeCycle(t *testing.T) {
	client := newMockGraphClient()
	g1 := client.AddGroup(1, "node-a", "org/node-a", 0)
	g2 := client.AddGroup(2, "node-b", "org/node-b", 1)
	g3 := client.AddGroup(3, "node-c", "org/node-c", 2)
	_ = g2
	client.AddSubgroupLink(g3.ID, g1.ID) // 1 -> 2 -> 3 -> 1

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond, "3-node cycle must terminate within 100ms")
	require.Len(t, groups, 3)

	ids := []int{groups[0].ID, groups[1].ID, groups[2].ID}
	assert.ElementsMatch(t, []int{1, 2, 3}, ids)
}

func TestGroupTree_AdversarialFiveNodeCycle(t *testing.T) {
	client := newMockGraphClient()
	for i := 1; i <= 5; i++ {
		parent := i - 1
		if i == 1 {
			parent = 0
		}
		client.AddGroup(i, fmt.Sprintf("node-%d", i), fmt.Sprintf("org/node-%d", i), parent)
	}
	client.AddSubgroupLink(5, 1) // 5 -> 1 closing the cycle

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond)
	require.Len(t, groups, 5)

	var ids []int
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	assert.ElementsMatch(t, []int{1, 2, 3, 4, 5}, ids)
}

func TestGroupTree_AdversarialInterconnectedCycles(t *testing.T) {
	// Figure-8 topology: Cycle 1 (1 -> 2 -> 3 -> 1) and Cycle 2 (3 -> 4 -> 5 -> 3)
	// Group 3 is the bridge node.
	client := newMockGraphClient()
	client.AddGroup(1, "c1-a", "org/c1-a", 0)
	client.AddGroup(2, "c1-b", "org/c1-b", 1)
	client.AddGroup(3, "bridge", "org/bridge", 2)
	client.AddSubgroupLink(3, 1) // Close cycle 1

	client.AddGroup(4, "c2-a", "org/c2-a", 3)
	client.AddGroup(5, "c2-b", "org/c2-b", 4)
	client.AddSubgroupLink(5, 3) // Close cycle 2

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond)
	require.Len(t, groups, 5)

	var ids []int
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	assert.ElementsMatch(t, []int{1, 2, 3, 4, 5}, ids)
}

func TestGroupTree_AdversarialDiamondTopology(t *testing.T) {
	// Diamond DAG:
	//      1
	//     / \
	//    2   3
	//     \ /
	//      4
	client := newMockGraphClient()
	client.AddGroup(1, "root", "org/root", 0)
	client.AddGroup(2, "left", "org/root/left", 1)
	client.AddGroup(3, "right", "org/root/right", 1)
	client.AddGroup(4, "converge", "org/root/converge", 2)
	client.AddSubgroupLink(3, 4) // 3 -> 4

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond)
	require.Len(t, groups, 4, "Converged node in diamond must be visited exactly once")

	var ids []int
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	assert.ElementsMatch(t, []int{1, 2, 3, 4}, ids)
}

func TestGroupTree_AdversarialCompleteGraphK6(t *testing.T) {
	// Complete graph K6: 6 nodes, each node links to all 5 other nodes
	client := newMockGraphClient()
	for i := 1; i <= 6; i++ {
		client.AddGroup(i, fmt.Sprintf("k6-%d", i), fmt.Sprintf("org/k6-%d", i), 0)
	}
	for i := 1; i <= 6; i++ {
		for j := 1; j <= 6; j++ {
			if i != j {
				client.AddSubgroupLink(i, j)
			}
		}
	}

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond)
	require.Len(t, groups, 6, "Every node in K6 must be visited exactly once")
}

// ----------------------------------------------------------------------------
// 2. Deep Hierarchy & Depth Ceiling Stress Tests
// ----------------------------------------------------------------------------

func TestGroupTree_AdversarialDeepHierarchy_60Levels(t *testing.T) {
	client := newMockGraphClient()
	client.AddGroup(1, "level-0", "org/level-0", 0)

	for lvl := 1; lvl < 60; lvl++ {
		client.AddGroup(lvl+1, fmt.Sprintf("level-%d", lvl), fmt.Sprintf("org/level-%d", lvl), lvl)
	}

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond)
	require.Len(t, groups, 60, "All 60 hierarchy levels must be visited")

	// Verify depth tracking correctness
	for idx, g := range groups {
		assert.Equal(t, idx, g.Depth, "Depth must strictly increment per level")
		assert.Equal(t, 1, g.RootParentID, "RootParentID must point to initial seed root")
	}
}

func TestGroupTree_AdversarialDeepHierarchy_100Levels(t *testing.T) {
	client := newMockGraphClient()
	client.AddGroup(1, "level-0", "org/level-0", 0)

	for lvl := 1; lvl < 100; lvl++ {
		client.AddGroup(lvl+1, fmt.Sprintf("level-%d", lvl), fmt.Sprintf("org/level-%d", lvl), lvl)
	}

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond)
	require.Len(t, groups, 100)
}

func TestGroupTree_AdversarialDeepHierarchy_150Levels_MaxDepthCeiling(t *testing.T) {
	// 150 linear levels: traversal must halt at maxDepth (100)
	client := newMockGraphClient()
	client.AddGroup(1, "level-0", "org/level-0", 0)

	for lvl := 1; lvl < 150; lvl++ {
		client.AddGroup(lvl+1, fmt.Sprintf("level-%d", lvl), fmt.Sprintf("org/level-%d", lvl), lvl)
	}

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond)
	// BFS visits depth 0..100 (which is 101 nodes), then stops expanding children of depth 100
	require.Len(t, groups, 101, "Max depth ceiling must cap expansion to 101 nodes (depths 0..100)")
	assert.Equal(t, 100, groups[100].Depth)
}

// ----------------------------------------------------------------------------
// 3. Partial Exclusion & Boundary Rules
// ----------------------------------------------------------------------------

func TestGroupTree_AdversarialPartialExclusion_IntermediateSubgroup(t *testing.T) {
	// Hierarchy: 1 (Root) -> 2 (Intermediate, Excluded) -> 3 (Leaf, Included)
	client := newMockGraphClient()
	client.AddGroup(1, "root", "company", 0)
	client.AddGroup(2, "intermediate", "company/excluded", 1)
	client.AddGroup(3, "leaf", "company/excluded/leaf", 2)

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx := context.Background()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude: []int{1},
		GroupIDsExclude: []int{2},
	})
	require.NoError(t, err)

	// Groups 1 and 3 match, Group 2 is excluded
	require.Len(t, groups, 2)
	assert.Equal(t, 1, groups[0].ID)
	assert.Equal(t, 3, groups[1].ID)
}

func TestGroupTree_AdversarialPartialExclusion_DiamondBranch(t *testing.T) {
	// Diamond: 1 -> (2, 3) -> 4. Exclude 2.
	client := newMockGraphClient()
	client.AddGroup(1, "root", "org/root", 0)
	client.AddGroup(2, "left", "org/root/left", 1)
	client.AddGroup(3, "right", "org/root/right", 1)
	client.AddGroup(4, "converge", "org/root/converge", 2)
	client.AddSubgroupLink(3, 4)

	traverser := discovery.NewBFSGroupTraverser(client)

	ctx := context.Background()
	groups, err := traverser.Traverse(ctx, &config.GroupSelector{
		GroupIDsInclude:   []int{1},
		GroupPathsExclude: []string{"org/root/left"},
	})
	require.NoError(t, err)

	require.Len(t, groups, 3)
	var ids []int
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	assert.ElementsMatch(t, []int{1, 3, 4}, ids)
}

func TestGroupTree_AdversarialPartialExclusion_CaseAndSlashNormalization(t *testing.T) {
	client := newMockGraphClient()
	client.AddGroup(10, "billing", "PLATFORM/BILLING", 0)
	client.AddGroup(11, "invoicing", "PLATFORM/BILLING/INVOICING", 10)

	traverser := discovery.NewBFSGroupTraverser(client)

	groups, err := traverser.Traverse(context.Background(), &config.GroupSelector{
		GroupPathsInclude: []string{"/platform/billing/"},
		GroupPathsExclude: []string{"/platform/billing/invoicing/"},
	})
	require.NoError(t, err)

	require.Len(t, groups, 1)
	assert.Equal(t, 10, groups[0].ID)
}

// ----------------------------------------------------------------------------
// 4. Concurrency & Race Freedom Stress Tests
// ----------------------------------------------------------------------------

func TestGroupTree_AdversarialConcurrentTraversals(t *testing.T) {
	client := newMockGraphClient()
	// Build complex graph with multiple cyclic components
	for i := 1; i <= 20; i++ {
		client.AddGroup(i, fmt.Sprintf("node-%d", i), fmt.Sprintf("org/node-%d", i), (i-1)%5)
	}
	// Add cross-cutting cyclic edges
	client.AddSubgroupLink(5, 1)
	client.AddSubgroupLink(10, 6)
	client.AddSubgroupLink(15, 11)
	client.AddSubgroupLink(20, 16)

	traverser := discovery.NewBFSGroupTraverser(client)

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(iter int) {
			defer wg.Done()

			rootID := (iter%4)*5 + 1
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			res, err := traverser.Traverse(ctx, &config.GroupSelector{
				GroupIDsInclude: []int{rootID},
			})
			if err != nil {
				errs <- err
				return
			}
			if len(res) == 0 {
				errs <- fmt.Errorf("unexpected empty traversal result for root %d", rootID)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}

func TestGroupSelectorEvaluator_AdversarialConcurrentMatches(t *testing.T) {
	sel := &config.GroupSelector{
		GroupIDsInclude:   []int{1, 2, 3, 4, 5},
		GroupIDsExclude:   []int{3},
		GroupPathsInclude: []string{"org/platform", "org/infra"},
		GroupPathsExclude: []string{"org/platform/deprecated"},
	}
	eval := discovery.NewGroupSelectorEvaluator(sel)

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			gMatch := &gitlab.Group{ID: 1, FullPath: "org/platform"}
			assert.True(t, eval.Matches(gMatch))

			gExcludedID := &gitlab.Group{ID: 3, FullPath: "org/platform"}
			assert.False(t, eval.Matches(gExcludedID))

			gExcludedPath := &gitlab.Group{ID: 2, FullPath: "org/platform/deprecated"}
			assert.False(t, eval.Matches(gExcludedPath))
		}(i)
	}

	wg.Wait()
}
