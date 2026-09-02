package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// Common discovery errors.
var (
	ErrNilClient = errors.New("gitlab client cannot be nil")
)

// TargetProject encapsulates a matched project and its targeting context.
type TargetProject struct {
	ID                int             `json:"id"`
	Name              string          `json:"name"`
	Path              string          `json:"path"`
	PathWithNamespace string          `json:"path_with_namespace"`
	DefaultBranch     string          `json:"default_branch"`
	Visibility        string          `json:"visibility"`
	Archived          bool            `json:"archived"`
	Topics            []string        `json:"topics"`
	NamespaceFullPath string          `json:"namespace_full_path"`
	ParentGroupID     int             `json:"parent_group_id,omitempty"`
	Raw               *gitlab.Project `json:"-"`
}

// TargetGroup encapsulates a matched group and its targeting context.
type TargetGroup struct {
	ID       int           `json:"id"`
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	FullPath string        `json:"full_path"`
	ParentID int           `json:"parent_id,omitempty"`
	Raw      *gitlab.Group `json:"-"`
}

// TargetFleet represents the fully resolved and deduplicated set of targeted
// GitLab groups and projects to govern.
type TargetFleet struct {
	Projects             map[int]*TargetProject `json:"projects"`
	Groups               map[int]*TargetGroup   `json:"groups"`
	ScannedProjectsCount int                    `json:"scanned_projects_count"`
	ScannedGroupsCount   int                    `json:"scanned_groups_count"`
	MatchedProjectsCount int                    `json:"matched_projects_count"`
	MatchedGroupsCount   int                    `json:"matched_groups_count"`
	Duration             time.Duration          `json:"duration"`
}

// NewTargetFleet creates an initialized TargetFleet.
func NewTargetFleet() *TargetFleet {
	return &TargetFleet{
		Projects: make(map[int]*TargetProject),
		Groups:   make(map[int]*TargetGroup),
	}
}

// IsEmpty returns true if no groups or projects were matched.
func (f *TargetFleet) IsEmpty() bool {
	return len(f.Projects) == 0 && len(f.Groups) == 0
}

// ProjectList returns a sorted slice of TargetProject items ordered by PathWithNamespace.
func (f *TargetFleet) ProjectList() []*TargetProject {
	list := make([]*TargetProject, 0, len(f.Projects))
	for _, p := range f.Projects {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].PathWithNamespace == list[j].PathWithNamespace {
			return list[i].ID < list[j].ID
		}
		return list[i].PathWithNamespace < list[j].PathWithNamespace
	})
	return list
}

// GroupList returns a sorted slice of TargetGroup items ordered by FullPath.
func (f *TargetFleet) GroupList() []*TargetGroup {
	list := make([]*TargetGroup, 0, len(f.Groups))
	for _, g := range f.Groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].FullPath == list[j].FullPath {
			return list[i].ID < list[j].ID
		}
		return list[i].FullPath < list[j].FullPath
	})
	return list
}

// ProjectIDs returns a sorted slice of all matched project IDs.
func (f *TargetFleet) ProjectIDs() []int {
	ids := make([]int, 0, len(f.Projects))
	for id := range f.Projects {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// GroupIDs returns a sorted slice of all matched group IDs.
func (f *TargetFleet) GroupIDs() []int {
	ids := make([]int, 0, len(f.Groups))
	for id := range f.Groups {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// DiscoveryOption configures DiscoverFleet execution.
type DiscoveryOption func(*discoveryCoordinator)

// WithConcurrency sets the parallel worker concurrency during group project discovery.
func WithConcurrency(c int) DiscoveryOption {
	return func(dc *discoveryCoordinator) {
		if c > 0 {
			dc.concurrency = c
		}
	}
}

// WithStreamOptions sets custom pagination stream options.
func WithStreamOptions(opts gl.StreamOptions) DiscoveryOption {
	return func(dc *discoveryCoordinator) {
		dc.streamOpts = opts
	}
}

// WithGroupTraverser sets a custom group tree traverser implementation.
func WithGroupTraverser(gt GroupTraverser) DiscoveryOption {
	return func(dc *discoveryCoordinator) {
		dc.groupTraverser = gt
	}
}

// DiscoverFleet resolves all targeted groups and projects declared in targets.
func DiscoverFleet(ctx context.Context, client gl.GitLabClient, targets config.TargetSelectors, opts ...DiscoveryOption) (*TargetFleet, error) {
	if client == nil {
		return nil, ErrNilClient
	}

	startTime := time.Now()

	dc := &discoveryCoordinator{
		client:         client,
		targets:        targets,
		concurrency:    10,
		streamOpts:     gl.DefaultStreamOptions(),
		groupTraverser: NewBFSGroupTraverser(client),
	}
	for _, opt := range opts {
		opt(dc)
	}

	fleet, err := dc.Execute(ctx)
	if err != nil {
		return nil, err
	}
	fleet.Duration = time.Since(startTime)
	return fleet, nil
}

type discoveryCoordinator struct {
	client         gl.GitLabClient
	targets        config.TargetSelectors
	concurrency    int
	streamOpts     gl.StreamOptions
	groupTraverser GroupTraverser
}

type fleetAccumulator struct {
	mu                   sync.Mutex
	projects             map[int]*TargetProject
	groups               map[int]*TargetGroup
	scannedProjectsCount int
	scannedGroupsCount   int
}

func newFleetAccumulator() *fleetAccumulator {
	return &fleetAccumulator{
		projects: make(map[int]*TargetProject),
		groups:   make(map[int]*TargetGroup),
	}
}

func (a *fleetAccumulator) AddDiscoveredGroup(g *DiscoveredGroup) {
	if g == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.scannedGroupsCount++
	if _, exists := a.groups[g.ID]; !exists {
		a.groups[g.ID] = &TargetGroup{
			ID:       g.ID,
			Name:     g.Name,
			Path:     g.Path,
			FullPath: g.FullPath,
			ParentID: g.ParentID,
			Raw:      g.Raw,
		}
	}
}

func (a *fleetAccumulator) AddGroup(g *gitlab.Group) {
	if g == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.scannedGroupsCount++
	if _, exists := a.groups[g.ID]; !exists {
		a.groups[g.ID] = &TargetGroup{
			ID:       g.ID,
			Name:     g.Name,
			Path:     g.Path,
			FullPath: g.FullPath,
			ParentID: g.ParentID,
			Raw:      g,
		}
	}
}

func (a *fleetAccumulator) AddProjectIfMatches(p *gitlab.Project, filter *ProjectFilter, parentGroupID int) bool {
	if p == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.scannedProjectsCount++

	if filter != nil && !filter.Matches(p) {
		return false
	}

	if _, exists := a.projects[p.ID]; !exists {
		a.projects[p.ID] = &TargetProject{
			ID:                p.ID,
			Name:              p.Name,
			Path:              p.Path,
			PathWithNamespace: p.PathWithNamespace,
			DefaultBranch:     p.DefaultBranch,
			Visibility:        string(p.Visibility),
			Archived:          p.Archived,
			Topics:            extractProjectTopics(p),
			NamespaceFullPath: extractProjectNamespace(p),
			ParentGroupID:     parentGroupID,
			Raw:               p,
		}
	}
	return true
}

func (a *fleetAccumulator) ToFleet() *TargetFleet {
	a.mu.Lock()
	defer a.mu.Unlock()

	fleet := NewTargetFleet()
	for k, v := range a.projects {
		fleet.Projects[k] = v
	}
	for k, v := range a.groups {
		fleet.Groups[k] = v
	}
	fleet.ScannedProjectsCount = a.scannedProjectsCount
	fleet.ScannedGroupsCount = a.scannedGroupsCount
	fleet.MatchedProjectsCount = len(fleet.Projects)
	fleet.MatchedGroupsCount = len(fleet.Groups)
	return fleet
}

func (dc *discoveryCoordinator) Execute(ctx context.Context) (*TargetFleet, error) {
	acc := newFleetAccumulator()

	// Compile project filter
	projectFilter, err := NewProjectFilter(dc.targets.ProjectSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize project filter: %w", err)
	}

	// Mode 1: Group-scoped discovery (with optional project filter)
	if dc.targets.GroupSelector != nil {
		groups, err := dc.groupTraverser.Traverse(ctx, dc.targets.GroupSelector)
		if err != nil {
			return nil, fmt.Errorf("group discovery failed: %w", err)
		}

		for _, g := range groups {
			acc.AddDiscoveredGroup(g)
		}

		// Concurrently scan projects within each targeted group
		if len(groups) > 0 {
			if err := dc.scanGroupProjects(ctx, groups, projectFilter, acc); err != nil {
				return nil, err
			}
		}

		return acc.ToFleet(), nil
	}

	// Mode 2: Instance-wide project discovery
	if err := dc.scanInstanceProjects(ctx, projectFilter, acc); err != nil {
		return nil, err
	}

	return acc.ToFleet(), nil
}

func (dc *discoveryCoordinator) scanGroupProjects(
	ctx context.Context,
	groups []*DiscoveredGroup,
	filter *ProjectFilter,
	acc *fleetAccumulator,
) error {
	sem := make(chan struct{}, dc.concurrency)
	errChan := make(chan error, len(groups))
	var wg sync.WaitGroup

	for _, g := range groups {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(group *DiscoveredGroup) {
			defer wg.Done()
			defer func() { <-sem }()

			ch := gl.StreamGroupProjects(
				ctx,
				dc.client.Groups().ListGroupProjects,
				group.ID,
				gitlab.ListGroupProjectsOptions{
					IncludeSubGroups: gitlab.Ptr(false), // Handled by group BFS traversal
				},
				dc.streamOpts,
			)

			for item := range ch {
				if item.Err != nil {
					select {
					case errChan <- fmt.Errorf("failed to stream projects for group %d (%s): %w", group.ID, group.FullPath, item.Err):
					default:
					}
					return
				}
				acc.AddProjectIfMatches(item.Value, filter, group.ID)
			}
		}(g)
	}

	wg.Wait()
	close(errChan)

	if len(errChan) > 0 {
		return <-errChan
	}
	return nil
}

func (dc *discoveryCoordinator) scanInstanceProjects(
	ctx context.Context,
	filter *ProjectFilter,
	acc *fleetAccumulator,
) error {
	ch := gl.StreamProjects(
		ctx,
		dc.client.Projects().ListProjects,
		gitlab.ListProjectsOptions{},
		dc.streamOpts,
	)

	for item := range ch {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if item.Err != nil {
			return fmt.Errorf("failed to stream instance projects: %w", item.Err)
		}
		acc.AddProjectIfMatches(item.Value, filter, 0)
	}

	return nil
}
