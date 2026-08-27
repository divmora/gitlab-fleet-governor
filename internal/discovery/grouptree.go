package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// DiscoveredGroup represents a normalized GitLab group resolved during fleet discovery.
type DiscoveredGroup struct {
	ID           int           `json:"id"`
	Name         string        `json:"name"`
	Path         string        `json:"path"`
	FullPath     string        `json:"full_path"`
	ParentID     int           `json:"parent_id"`
	RootParentID int           `json:"root_parent_id"`
	Depth        int           `json:"depth"`
	Visibility   string        `json:"visibility"`
	Description  string        `json:"description"`
	WebURL       string        `json:"web_url"`
	Raw          *gitlab.Group `json:"-"`
}

// GroupSelectorEvaluator encapsulates include/exclude matching rules.
type GroupSelectorEvaluator struct {
	includeIDs   map[int]bool
	excludeIDs   map[int]bool
	includePaths map[string]bool
	excludePaths map[string]bool
	hasIncludes  bool
	recursive    bool
}

// NewGroupSelectorEvaluator builds a compiled evaluator for fast O(1) lookups.
func NewGroupSelectorEvaluator(sel *config.GroupSelector) *GroupSelectorEvaluator {
	eval := &GroupSelectorEvaluator{
		includeIDs:   make(map[int]bool),
		excludeIDs:   make(map[int]bool),
		includePaths: make(map[string]bool),
		excludePaths: make(map[string]bool),
		recursive:    true, // default is recursive: true
	}

	if sel == nil {
		return eval
	}

	if sel.Recursive != nil {
		eval.recursive = *sel.Recursive
	}

	for _, id := range sel.GroupIDsInclude {
		eval.includeIDs[id] = true
	}
	for _, id := range sel.GroupIDsExclude {
		eval.excludeIDs[id] = true
	}
	for _, p := range sel.GroupPathsInclude {
		clean := strings.Trim(strings.ToLower(p), "/")
		if clean != "" {
			eval.includePaths[clean] = true
		}
	}
	for _, p := range sel.GroupPathsExclude {
		clean := strings.Trim(strings.ToLower(p), "/")
		if clean != "" {
			eval.excludePaths[clean] = true
		}
	}

	eval.hasIncludes = len(eval.includeIDs) > 0 || len(eval.includePaths) > 0
	return eval
}

// Matches evaluates whether a group satisfies the selector rules.
// Rule: Exclusions strictly take precedence over inclusions.
func (e *GroupSelectorEvaluator) Matches(g *gitlab.Group) bool {
	if g == nil {
		return false
	}

	cleanPath := strings.Trim(strings.ToLower(g.FullPath), "/")

	// 1. Check exclusions (Precedence 1)
	if e.excludeIDs[g.ID] || e.excludePaths[cleanPath] {
		return false
	}

	// 2. Check inclusions (Precedence 2)
	if !e.hasIncludes {
		return true // No includes defined -> match all non-excluded
	}

	if e.includeIDs[g.ID] || e.includePaths[cleanPath] {
		return true
	}

	return false
}

// IsExcluded checks if a group matches any exclusion rule.
func (e *GroupSelectorEvaluator) IsExcluded(g *gitlab.Group) bool {
	if g == nil {
		return true
	}
	cleanPath := strings.Trim(strings.ToLower(g.FullPath), "/")
	return e.excludeIDs[g.ID] || e.excludePaths[cleanPath]
}

// IsRecursive returns whether subgroup traversal is enabled.
func (e *GroupSelectorEvaluator) IsRecursive() bool {
	return e.recursive
}

// GroupTraverser defines the interface for discovering groups.
type GroupTraverser interface {
	Traverse(ctx context.Context, selector *config.GroupSelector) ([]*DiscoveredGroup, error)
}

// BFSGroupTraverser implements Breadth-First Search group hierarchy traversal.
type BFSGroupTraverser struct {
	client   gl.GitLabClient
	maxDepth int
}

// NewBFSGroupTraverser constructs a new BFSGroupTraverser.
func NewBFSGroupTraverser(client gl.GitLabClient) *BFSGroupTraverser {
	return &BFSGroupTraverser{
		client:   client,
		maxDepth: 100, // safe depth ceiling to protect against unbounded recursion
	}
}

// queueItem tracks a group in the BFS traversal queue.
type queueItem struct {
	group        *gitlab.Group
	depth        int
	rootParentID int
}

// Traverse executes BFS group tree traversal with cycle detection and selector filtering.
func (t *BFSGroupTraverser) Traverse(ctx context.Context, selector *config.GroupSelector) ([]*DiscoveredGroup, error) {
	eval := NewGroupSelectorEvaluator(selector)
	visited := make(map[int]bool)
	var queue []queueItem
	var matchedGroups []*DiscoveredGroup

	// Phase 1: Resolve Seed / Root Groups
	roots, err := t.resolveRoots(ctx, selector)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve root groups: %w", err)
	}

	for _, root := range roots {
		if root == nil || visited[root.ID] {
			continue
		}
		visited[root.ID] = true
		queue = append(queue, queueItem{
			group:        root,
			depth:        0,
			rootParentID: root.ID,
		})
	}

	// Phase 2: BFS Traversal Queue
	head := 0
	for head < len(queue) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		curr := queue[head]
		head++

		// Check if current group is NOT excluded
		if !eval.IsExcluded(curr.group) {
			norm := NormalizeGroup(curr.group, curr.depth, curr.rootParentID)
			matchedGroups = append(matchedGroups, norm)
		}

		// Phase 3: Expand Subgroups if Recursive
		if eval.IsRecursive() && (t.maxDepth <= 0 || curr.depth < t.maxDepth) {
			subgroups, err := t.listAllSubgroups(ctx, curr.group.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to list subgroups for group %d (%s): %w", curr.group.ID, curr.group.FullPath, err)
			}

			for _, child := range subgroups {
				if child == nil {
					continue
				}
				// Cycle Detection: check visited set
				if visited[child.ID] {
					continue // cycle detected or already processed, skip
				}
				visited[child.ID] = true

				rootID := curr.rootParentID
				if rootID == 0 {
					rootID = curr.group.ID
				}

				queue = append(queue, queueItem{
					group:        child,
					depth:        curr.depth + 1,
					rootParentID: rootID,
				})
			}
		}
	}

	return matchedGroups, nil
}

// resolveRoots fetches initial root groups based on include filters or instance top-level groups.
func (t *BFSGroupTraverser) resolveRoots(ctx context.Context, selector *config.GroupSelector) ([]*gitlab.Group, error) {
	var roots []*gitlab.Group
	seen := make(map[int]bool)

	if selector != nil && (len(selector.GroupIDsInclude) > 0 || len(selector.GroupPathsInclude) > 0) {
		// Resolve explicit group IDs
		for _, id := range selector.GroupIDsInclude {
			if seen[id] {
				continue
			}
			g, _, err := t.client.Groups().GetGroup(id, nil, gitlab.WithContext(ctx))
			if err != nil {
				return nil, fmt.Errorf("failed to get group by ID %d: %w", id, err)
			}
			if g != nil && !seen[g.ID] {
				seen[g.ID] = true
				roots = append(roots, g)
			}
		}

		// Resolve explicit group paths
		for _, path := range selector.GroupPathsInclude {
			clean := strings.Trim(path, "/")
			if clean == "" {
				continue
			}
			g, _, err := t.client.Groups().GetGroup(clean, nil, gitlab.WithContext(ctx))
			if err != nil {
				return nil, fmt.Errorf("failed to get group by path '%s': %w", clean, err)
			}
			if g != nil && !seen[g.ID] {
				seen[g.ID] = true
				roots = append(roots, g)
			}
		}
		return roots, nil
	}

	// No explicit includes: fetch all top-level groups on the instance
	opt := &gitlab.ListGroupsOptions{
		ListOptions:  gitlab.ListOptions{PerPage: 100, Page: 1},
		TopLevelOnly: gitlab.Ptr(true),
	}

	for {
		groups, resp, err := t.client.RawClient().Groups.ListGroups(opt, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to list top-level groups: %w", err)
		}
		for _, g := range groups {
			if g != nil && !seen[g.ID] {
				seen[g.ID] = true
				roots = append(roots, g)
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return roots, nil
}

// listAllSubgroups retrieves all direct child subgroups of a parent group using pagination.
func (t *BFSGroupTraverser) listAllSubgroups(ctx context.Context, groupID int) ([]*gitlab.Group, error) {
	var allSubgroups []*gitlab.Group
	opt := &gitlab.ListSubGroupsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100, Page: 1},
	}

	for {
		subgroups, resp, err := t.client.Groups().ListSubgroups(groupID, opt, gitlab.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		allSubgroups = append(allSubgroups, subgroups...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return allSubgroups, nil
}

// NormalizeGroup converts a raw SDK *gitlab.Group into a standard DiscoveredGroup.
func NormalizeGroup(raw *gitlab.Group, depth, rootParentID int) *DiscoveredGroup {
	if raw == nil {
		return nil
	}
	return &DiscoveredGroup{
		ID:           raw.ID,
		Name:         raw.Name,
		Path:         raw.Path,
		FullPath:     raw.FullPath,
		ParentID:     raw.ParentID,
		RootParentID: rootParentID,
		Depth:        depth,
		Visibility:   string(raw.Visibility),
		Description:  raw.Description,
		WebURL:       raw.WebURL,
		Raw:          raw,
	}
}
