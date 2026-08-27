package discovery_test

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ============================================================================
// PART 1: Combinatorial Edge Case Testing for ProjectFilter
// ============================================================================

func TestProjectFilter_NamespaceCombinatorialEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		selector    *config.ProjectSelector
		project     *gitlab.Project
		wantMatch   bool
		wantDecis   discovery.FilterDecision
		description string
	}{
		{
			name: "PartialPrefixBoundary_PlatVsPlatform",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"plat"},
			},
			project: &gitlab.Project{
				ID:                1,
				Name:              "svc",
				PathWithNamespace: "platform/svc",
				Namespace:         &gitlab.ProjectNamespace{FullPath: "platform"},
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionMissingNamespace,
			description: "'plat' must not match 'platform' as a partial prefix",
		},
		{
			name: "PartialPrefixBoundary_PlatHyphenVsPlat",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"plat"},
			},
			project: &gitlab.Project{
				ID:                2,
				Name:              "svc",
				PathWithNamespace: "plat-team/svc",
				Namespace:         &gitlab.ProjectNamespace{FullPath: "plat-team"},
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionMissingNamespace,
			description: "'plat' must not match 'plat-team'",
		},
		{
			name: "ExactNamespaceMatch",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"platform"},
			},
			project: &gitlab.Project{
				ID:                3,
				Name:              "svc",
				PathWithNamespace: "platform/svc",
				Namespace:         &gitlab.ProjectNamespace{FullPath: "platform"},
			},
			wantMatch:   true,
			wantDecis:   discovery.DecisionMatched,
			description: "Exact namespace match",
		},
		{
			name: "SubNamespaceHierarchyMatch",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"platform"},
			},
			project: &gitlab.Project{
				ID:                4,
				Name:              "svc",
				PathWithNamespace: "platform/infra/k8s/svc",
				Namespace:         &gitlab.ProjectNamespace{FullPath: "platform/infra/k8s"},
			},
			wantMatch:   true,
			wantDecis:   discovery.DecisionMatched,
			description: "Deep subgroup hierarchy matches root included namespace",
		},
		{
			name: "ParentNamespaceDoesNotMatchChildSelector",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"platform/infra/k8s"},
			},
			project: &gitlab.Project{
				ID:                5,
				Name:              "svc",
				PathWithNamespace: "platform/svc",
				Namespace:         &gitlab.ProjectNamespace{FullPath: "platform"},
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionMissingNamespace,
			description: "Parent namespace 'platform' does not match child selector 'platform/infra/k8s'",
		},
		{
			name: "SlashNormalization_MixedLeadingTrailingSlashes",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"///platform/core///"},
			},
			project: &gitlab.Project{
				ID:                6,
				Name:              "svc",
				PathWithNamespace: "platform/core/sub/svc",
				Namespace:         &gitlab.ProjectNamespace{FullPath: "/PLATFORM/CORE/SUB/"},
			},
			wantMatch:   true,
			wantDecis:   discovery.DecisionMatched,
			description: "Leading/trailing slashes and casing normalized correctly",
		},
		{
			name: "ExcludeTakesPrecedenceOverChildInclude",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"platform"},
				NamespacesExclude: []string{"platform/sandbox"},
			},
			project: &gitlab.Project{
				ID:                7,
				Name:              "svc",
				PathWithNamespace: "platform/sandbox/experimental/svc",
				Namespace:         &gitlab.ProjectNamespace{FullPath: "platform/sandbox/experimental"},
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionExcludedNamespace,
			description: "Exclude takes strict precedence on subgroup hierarchy",
		},
		{
			name: "FallbackToPathWithNamespaceWhenNamespaceNil",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"engineering/backend"},
			},
			project: &gitlab.Project{
				ID:                8,
				Name:              "svc",
				PathWithNamespace: "engineering/backend/api/svc",
				Namespace:         nil,
			},
			wantMatch:   true,
			wantDecis:   discovery.DecisionMatched,
			description: "Nil Namespace falls back to PathWithNamespace prefix",
		},
		{
			name: "RootProjectWithoutSlashNamespace",
			selector: &config.ProjectSelector{
				NamespacesInclude: []string{"engineering"},
			},
			project: &gitlab.Project{
				ID:                9,
				Name:              "root-svc",
				PathWithNamespace: "root-svc",
				Namespace:         nil,
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionMissingNamespace,
			description: "Root project with no namespace does not match namespace selector",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := discovery.NewProjectFilter(tc.selector)
			require.NoError(t, err)

			res := f.Evaluate(tc.project)
			assert.Equal(t, tc.wantMatch, res.Matched, tc.description)
			assert.Equal(t, tc.wantDecis, res.Decision, tc.description)
			assert.Equal(t, tc.wantMatch, f.Matches(tc.project), tc.description)
		})
	}
}

func TestProjectFilter_ConflictingRegexCombinatorialCases(t *testing.T) {
	tests := []struct {
		name        string
		selector    *config.ProjectSelector
		project     *gitlab.Project
		wantMatch   bool
		wantDecis   discovery.FilterDecision
		description string
	}{
		{
			name: "StrictExcludePrecedence_IdenticalPatterns",
			selector: &config.ProjectSelector{
				ProjectNameRegexInclude: `^billing-.*`,
				ProjectNameRegexExclude: `^billing-.*`,
			},
			project: &gitlab.Project{
				ID:   1,
				Name: "billing-service",
				Path: "billing-service",
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionExcludedRegex,
			description: "When include and exclude match the same string, exclude MUST win",
		},
		{
			name: "IncludeMatchesPath_ExcludeMatchesName",
			selector: &config.ProjectSelector{
				ProjectNameRegexInclude: `^backend-api$`,
				ProjectNameRegexExclude: `.*-deprecated$`,
			},
			project: &gitlab.Project{
				ID:   2,
				Name: "API Gateway - Deprecated",
				Path: "backend-api",
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionExcludedRegex,
			description: "Exclude matching project Name overrides include matching project Path",
		},
		{
			name: "IncludeMatchesName_ExcludeMatchesPath",
			selector: &config.ProjectSelector{
				ProjectNameRegexInclude: `(?i)^Payments Engine$`,
				ProjectNameRegexExclude: `^legacy-.*`,
			},
			project: &gitlab.Project{
				ID:   3,
				Name: "Payments Engine",
				Path: "legacy-payments",
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionExcludedRegex,
			description: "Exclude matching project Path overrides include matching project Name",
		},
		{
			name: "ComplexRegexWithAnchorsAndQuantifiers",
			selector: &config.ProjectSelector{
				ProjectNameRegexInclude: `^(srv|svc)-[a-z0-9]+-(v[1-9][0-9]*|prod|staging)$`,
			},
			project: &gitlab.Project{
				ID:   4,
				Name: "svc-auth-v2",
				Path: "svc-auth-v2",
			},
			wantMatch:   true,
			wantDecis:   discovery.DecisionMatched,
			description: "Complex regex matching valid token format",
		},
		{
			name: "ComplexRegexNonMatchingToken",
			selector: &config.ProjectSelector{
				ProjectNameRegexInclude: `^(srv|svc)-[a-z0-9]+-(v[1-9][0-9]*|prod|staging)$`,
			},
			project: &gitlab.Project{
				ID:   5,
				Name: "svc-auth-v0",
				Path: "svc-auth-v0",
			},
			wantMatch:   false,
			wantDecis:   discovery.DecisionMissingRegex,
			description: "Complex regex rejects invalid token format (v0)",
		},
		{
			name: "WhitespaceRegexHandledAsEmpty",
			selector: &config.ProjectSelector{
				ProjectNameRegexInclude: "   \t\n  ",
			},
			project: &gitlab.Project{
				ID:   6,
				Name: "any-project",
				Path: "any-project",
			},
			wantMatch:   true,
			wantDecis:   discovery.DecisionMatched,
			description: "Whitespace-only regex selector treated as no regex configured",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := discovery.NewProjectFilter(tc.selector)
			require.NoError(t, err)

			res := f.Evaluate(tc.project)
			assert.Equal(t, tc.wantMatch, res.Matched, tc.description)
			assert.Equal(t, tc.wantDecis, res.Decision, tc.description)
		})
	}
}

func TestProjectFilter_TopicPermutations(t *testing.T) {
	tests := []struct {
		name      string
		selector  *config.ProjectSelector
		project   *gitlab.Project
		wantMatch bool
		wantDecis discovery.FilterDecision
	}{
		{
			name: "MixedCasingAndWhitespaceInProjectAndSelector",
			selector: &config.ProjectSelector{
				TopicsInclude: []string{"  Golang  ", "PCI-DSS"},
			},
			project: &gitlab.Project{
				ID:      1,
				Topics:  []string{"GOLANG", "microservice"},
				TagList: []string{"pci-dss"},
			},
			wantMatch: true,
			wantDecis: discovery.DecisionMatched,
		},
		{
			name: "TopicExcludePrecedenceOverInclude",
			selector: &config.ProjectSelector{
				TopicsInclude: []string{"golang", "production"},
				TopicsExclude: []string{"archived-soon", "sandbox"},
			},
			project: &gitlab.Project{
				ID:     2,
				Topics: []string{"GOLANG", "production", "SANDBOX"},
			},
			wantMatch: false,
			wantDecis: discovery.DecisionExcludedTopic,
		},
		{
			name: "DuplicateTopicsInBothTopicsAndTagList",
			selector: &config.ProjectSelector{
				TopicsInclude: []string{"compliance"},
			},
			project: &gitlab.Project{
				ID:      3,
				Topics:  []string{"compliance", "COMPLIANCE"},
				TagList: []string{"compliance", "sec"},
			},
			wantMatch: true,
			wantDecis: discovery.DecisionMatched,
		},
		{
			name: "ProjectWithNoTopicsFailsInclude",
			selector: &config.ProjectSelector{
				TopicsInclude: []string{"required-topic"},
			},
			project: &gitlab.Project{
				ID:      4,
				Topics:  []string{},
				TagList: []string{},
			},
			wantMatch: false,
			wantDecis: discovery.DecisionMissingTopic,
		},
		{
			name: "EmptyTopicsInSelectorIgnored",
			selector: &config.ProjectSelector{
				TopicsInclude: []string{"", "  ", "\t"},
			},
			project: &gitlab.Project{
				ID:     5,
				Topics: []string{"some-topic"},
			},
			wantMatch: true,
			wantDecis: discovery.DecisionMatched,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := discovery.NewProjectFilter(tc.selector)
			require.NoError(t, err)

			res := f.Evaluate(tc.project)
			assert.Equal(t, tc.wantMatch, res.Matched)
			assert.Equal(t, tc.wantDecis, res.Decision)
		})
	}
}

func TestProjectFilter_IDRangeBoundaryExhaustive(t *testing.T) {
	tests := []struct {
		name      string
		idRange   *config.IDRange
		projectID int
		wantMatch bool
	}{
		// Exact match point
		{"ExactPointMatch_Below", &config.IDRange{Min: 50, Max: 50}, 49, false},
		{"ExactPointMatch_Exact", &config.IDRange{Min: 50, Max: 50}, 50, true},
		{"ExactPointMatch_Above", &config.IDRange{Min: 50, Max: 50}, 51, false},

		// Standard range [100, 200]
		{"Range_LowerBoundaryMinus1", &config.IDRange{Min: 100, Max: 200}, 99, false},
		{"Range_LowerBoundaryExact", &config.IDRange{Min: 100, Max: 200}, 100, true},
		{"Range_Midpoint", &config.IDRange{Min: 100, Max: 200}, 150, true},
		{"Range_UpperBoundaryExact", &config.IDRange{Min: 100, Max: 200}, 200, true},
		{"Range_UpperBoundaryPlus1", &config.IDRange{Min: 100, Max: 200}, 201, false},

		// Min-only [100, 0]
		{"MinOnly_Below", &config.IDRange{Min: 100, Max: 0}, 99, false},
		{"MinOnly_Exact", &config.IDRange{Min: 100, Max: 0}, 100, true},
		{"MinOnly_Large", &config.IDRange{Min: 100, Max: 0}, 1000000, true},

		// Max-only [0, 500]
		{"MaxOnly_Zero", &config.IDRange{Min: 0, Max: 500}, 0, true},
		{"MaxOnly_Inside", &config.IDRange{Min: 0, Max: 500}, 250, true},
		{"MaxOnly_Exact", &config.IDRange{Min: 0, Max: 500}, 500, true},
		{"MaxOnly_Above", &config.IDRange{Min: 0, Max: 500}, 501, false},

		// Inverted Range [500, 100] -> impossible to satisfy
		{"Inverted_BelowAll", &config.IDRange{Min: 500, Max: 100}, 50, false},
		{"Inverted_Inside", &config.IDRange{Min: 500, Max: 100}, 300, false},
		{"Inverted_AboveAll", &config.IDRange{Min: 500, Max: 100}, 600, false},

		// Extreme integers
		{"Extreme_MaxInt", &config.IDRange{Min: 1000, Max: math.MaxInt32}, math.MaxInt32, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := discovery.NewProjectFilter(&config.ProjectSelector{
				IDRange: tc.idRange,
			})
			require.NoError(t, err)

			p := &gitlab.Project{ID: tc.projectID, Name: "test"}
			assert.Equal(t, tc.wantMatch, f.Matches(p))
		})
	}
}

func TestProjectFilter_VisibilityPermutations(t *testing.T) {
	visibilities := []struct {
		configVis   string
		projVis     gitlab.VisibilityValue
		shouldMatch bool
	}{
		{"public", gitlab.PublicVisibility, true},
		{"PUBLIC", gitlab.PublicVisibility, true},
		{"public", gitlab.PrivateVisibility, false},
		{"public", gitlab.InternalVisibility, false},

		{"private", gitlab.PrivateVisibility, true},
		{"PRIVATE", gitlab.PrivateVisibility, true},
		{"private", gitlab.PublicVisibility, false},

		{"internal", gitlab.InternalVisibility, true},
		{"INTERNAL", gitlab.InternalVisibility, true},
		{"internal", gitlab.PublicVisibility, false},

		{"any", gitlab.PublicVisibility, true},
		{"ANY", gitlab.PrivateVisibility, true},
		{"any", gitlab.InternalVisibility, true},

		{"", gitlab.PublicVisibility, true},
		{"", gitlab.PrivateVisibility, true},
		{"", gitlab.InternalVisibility, true},
	}

	for _, tc := range visibilities {
		t.Run(fmt.Sprintf("Config_%s_Project_%s", tc.configVis, tc.projVis), func(t *testing.T) {
			f, err := discovery.NewProjectFilter(&config.ProjectSelector{
				Visibility: tc.configVis,
			})
			require.NoError(t, err)

			p := &gitlab.Project{ID: 1, Visibility: tc.projVis}
			assert.Equal(t, tc.shouldMatch, f.Matches(p))
		})
	}
}

// ============================================================================
// PART 2: High-Scale Concurrency & Stress Testing for DiscoverFleet
// (1,000+ simulated projects across 50 groups with shared/overlapping projects)
// ============================================================================

func TestDiscoverFleet_HighScale1000Projects50Groups(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	const (
		totalGroups          = 50
		uniqueProjectsCount  = 1000
		projectsPerGroup     = 30 // 50 * 30 = 1500 assignments with heavy overlap
		concurrencyWorkers   = 20
	)

	// 1. Seed 50 groups
	groupIDs := make([]int, totalGroups)
	for i := 0; i < totalGroups; i++ {
		gID := i + 1
		groupIDs[i] = gID
		srv.State().AddGroup(&gitlab.Group{
			ID:       gID,
			Name:     fmt.Sprintf("Group-%03d", gID),
			Path:     fmt.Sprintf("group-%03d", gID),
			FullPath: fmt.Sprintf("enterprise/group-%03d", gID),
		})
	}

	// 2. Seed 1,000 unique projects
	for pID := 1; pID <= uniqueProjectsCount; pID++ {
		vis := gitlab.PrivateVisibility
		if pID%3 == 0 {
			vis = gitlab.InternalVisibility
		} else if pID%5 == 0 {
			vis = gitlab.PublicVisibility
		}

		topics := []string{"tier-1"}
		if pID%2 == 0 {
			topics = append(topics, "golang")
		}
		if pID%10 == 0 {
			topics = append(topics, "deprecated")
		}

		assignedGroup := ((pID - 1) % totalGroups) + 1

		srv.State().AddProject(&gitlab.Project{
			ID:                pID,
			Name:              fmt.Sprintf("service-%04d", pID),
			Path:              fmt.Sprintf("service-%04d", pID),
			PathWithNamespace: fmt.Sprintf("enterprise/group-%03d/service-%04d", assignedGroup, pID),
			Visibility:        vis,
			Archived:          pID%20 == 0, // 5% archived
			Topics:            topics,
			Namespace: &gitlab.ProjectNamespace{
				FullPath: fmt.Sprintf("enterprise/group-%03d", assignedGroup),
			},
		})
	}

	// 3. Populate group memberships with deliberate overlaps
	// Each group gets 30 projects: 20 assigned + 10 shared projects from other groups
	for gIdx, gID := range groupIDs {
		// Assigned primary projects
		for offset := 0; offset < 20; offset++ {
			pID := ((gIdx*20 + offset) % uniqueProjectsCount) + 1
			srv.State().AddGroupProject(gID, pID)
		}
		// Shared/overlapping projects (from a shared pool 900..1000)
		for sharedIdx := 0; sharedIdx < 10; sharedIdx++ {
			sharedPID := 901 + ((gIdx*3 + sharedIdx) % 100)
			srv.State().AddGroupProject(gID, sharedPID)
		}
	}

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("FullFleetDiscoveryWithExactDeduplication", func(t *testing.T) {
		recursiveFalse := false
		targets := config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: groupIDs,
				Recursive:       &recursiveFalse,
			},
		}

		start := time.Now()
		fleet, err := discovery.DiscoverFleet(
			ctx,
			client,
			targets,
			discovery.WithConcurrency(concurrencyWorkers),
		)
		elapsed := time.Since(start)

		require.NoError(t, err)
		require.NotNil(t, fleet)

		t.Logf("High-Scale Discovery Completed: %d groups, %d unique projects in %v (Scanned %d projects)",
			fleet.MatchedGroupsCount, fleet.MatchedProjectsCount, elapsed, fleet.ScannedProjectsCount)

		// Verification 1: Exactly 50 groups discovered
		assert.Equal(t, totalGroups, fleet.MatchedGroupsCount)
		assert.Len(t, fleet.Groups, totalGroups)

		// Verification 2: All 1,000 unique projects discovered with zero duplicates
		assert.Equal(t, uniqueProjectsCount, fleet.MatchedProjectsCount)
		assert.Len(t, fleet.Projects, uniqueProjectsCount)

		// Verification 3: Scanned count equals total group project mappings (50 * 30 = 1,500)
		assert.Equal(t, totalGroups*projectsPerGroup, fleet.ScannedProjectsCount)

		// Verification 4: TargetFleet helpers produce strictly deduplicated and sorted lists
		projList := fleet.ProjectList()
		assert.Len(t, projList, uniqueProjectsCount)
		projIDs := fleet.ProjectIDs()
		assert.Len(t, projIDs, uniqueProjectsCount)
		for i := 1; i < len(projIDs); i++ {
			assert.Greater(t, projIDs[i], projIDs[i-1], "ProjectIDs must be strictly ascending with no duplicates")
		}

		groupList := fleet.GroupList()
		assert.Len(t, groupList, totalGroups)
		gIDs := fleet.GroupIDs()
		assert.Len(t, gIDs, totalGroups)
		for i := 1; i < len(gIDs); i++ {
			assert.Greater(t, gIDs[i], gIDs[i-1], "GroupIDs must be strictly ascending with no duplicates")
		}
	})

	t.Run("FilteredFleetDiscoveryUnderHighScale", func(t *testing.T) {
		recursiveFalse := false
		archivedFalse := false
		targets := config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: groupIDs,
				Recursive:       &recursiveFalse,
			},
			ProjectSelector: &config.ProjectSelector{
				Archived:                &archivedFalse, // Active only (excludes pID % 20 == 0)
				TopicsInclude:           []string{"golang"}, // Only golang (pID % 2 == 0)
				TopicsExclude:           []string{"deprecated"}, // Excludes pID % 10 == 0
				ProjectNameRegexInclude: `^service-0[0-4][0-9]{2}$`, // 0001 to 0499
			},
		}

		fleet, err := discovery.DiscoverFleet(
			ctx,
			client,
			targets,
			discovery.WithConcurrency(concurrencyWorkers),
		)
		require.NoError(t, err)
		require.NotNil(t, fleet)

		// Calculate expected matched projects independently:
		// ID in [1..499], active (ID % 20 != 0), even (ID % 2 == 0), not deprecated (ID % 10 != 0)
		var expectedIDs []int
		for id := 1; id <= 499; id++ {
			if id%2 == 0 && id%10 != 0 && id%20 != 0 {
				expectedIDs = append(expectedIDs, id)
			}
		}

		assert.Len(t, fleet.Projects, len(expectedIDs))
		assert.Equal(t, len(expectedIDs), fleet.MatchedProjectsCount)
		assert.ElementsMatch(t, expectedIDs, fleet.ProjectIDs())

		// Scanned count must still reflect total project items processed across all groups
		assert.Equal(t, totalGroups*projectsPerGroup, fleet.ScannedProjectsCount)
	})
}

func TestDiscoverFleet_AdversarialMassiveConcurrentRaceFreedom(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.State().Reset()

	// Seed 30 groups and 150 projects
	for gID := 1; gID <= 30; gID++ {
		srv.State().AddGroup(&gitlab.Group{
			ID:       gID,
			Name:     fmt.Sprintf("G-%d", gID),
			FullPath: fmt.Sprintf("org/g-%d", gID),
		})
		for pID := 1; pID <= 5; pID++ {
			globalPID := gID*100 + pID
			srv.State().AddProject(&gitlab.Project{
				ID:                globalPID,
				Name:              fmt.Sprintf("p-%d", globalPID),
				PathWithNamespace: fmt.Sprintf("org/g-%d/p-%d", gID, globalPID),
				Visibility:        gitlab.PrivateVisibility,
			})
			srv.State().AddGroupProject(gID, globalPID)
		}
	}

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	const concurrentGoroutines = 25
	var wg sync.WaitGroup
	errChan := make(chan error, concurrentGoroutines)

	for i := 0; i < concurrentGoroutines; i++ {
		wg.Add(1)
		go func(routineIdx int) {
			defer wg.Done()

			recursiveFalse := false
			targets := config.TargetSelectors{
				GroupSelector: &config.GroupSelector{
					GroupPathsInclude: []string{
						fmt.Sprintf("org/g-%d", (routineIdx%30)+1),
						fmt.Sprintf("org/g-%d", ((routineIdx+1)%30)+1),
					},
					Recursive: &recursiveFalse,
				},
			}

			fleet, err := discovery.DiscoverFleet(
				context.Background(),
				client,
				targets,
				discovery.WithConcurrency(8),
			)
			if err != nil {
				errChan <- fmt.Errorf("goroutine %d failed: %w", routineIdx, err)
				return
			}
			if len(fleet.Groups) != 2 {
				errChan <- fmt.Errorf("goroutine %d expected 2 groups, got %d", routineIdx, len(fleet.Groups))
				return
			}
			if len(fleet.Projects) != 10 {
				errChan <- fmt.Errorf("goroutine %d expected 10 projects, got %d", routineIdx, len(fleet.Projects))
				return
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		require.NoError(t, err, "No race condition or failure allowed during concurrent execution")
	}
}

func TestDiscoverFleet_MultiTierCircularSubgroupHierarchy(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()

	// 5-group circular ring: 1000 -> 1001 -> 1002 -> 1003 -> 1004 -> 1000
	for i := 0; i < 5; i++ {
		gID := 1000 + i
		srv.State().AddGroup(&gitlab.Group{
			ID:       gID,
			Name:     fmt.Sprintf("Ring-%d", gID),
			FullPath: fmt.Sprintf("ring/g-%d", gID),
		})
	}
	for i := 0; i < 5; i++ {
		curr := 1000 + i
		next := 1000 + ((i + 1) % 5)
		srv.State().AddSubgroup(curr, next)
	}

	// Attach 10 projects to each ring node (total 50 projects)
	for i := 0; i < 5; i++ {
		gID := 1000 + i
		for p := 1; p <= 10; p++ {
			pID := gID*10 + p
			srv.State().AddProject(&gitlab.Project{
				ID:                pID,
				Name:              fmt.Sprintf("ring-proj-%d", pID),
				PathWithNamespace: fmt.Sprintf("ring/g-%d/ring-proj-%d", gID, pID),
			})
			srv.State().AddGroupProject(gID, pID)
		}
	}

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	recursiveTrue := true
	targets := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupIDsInclude: []int{1000},
			Recursive:       &recursiveTrue,
		},
	}

	fleet, err := discovery.DiscoverFleet(ctx, client, targets, discovery.WithConcurrency(10))
	require.NoError(t, err)
	require.NotNil(t, fleet)

	// Must discover all 5 ring groups and all 50 projects with zero duplicates
	assert.Equal(t, 5, fleet.MatchedGroupsCount)
	assert.Len(t, fleet.Groups, 5)
	assert.ElementsMatch(t, []int{1000, 1001, 1002, 1003, 1004}, fleet.GroupIDs())

	assert.Equal(t, 50, fleet.MatchedProjectsCount)
	assert.Len(t, fleet.Projects, 50)
	assert.Len(t, fleet.ProjectIDs(), 50)
}
