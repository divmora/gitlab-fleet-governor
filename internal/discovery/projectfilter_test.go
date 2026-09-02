package discovery_test

import (
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestProjectFilter_NewProjectFilter(t *testing.T) {
	t.Run("NilSelector_MatchesAll", func(t *testing.T) {
		f, err := discovery.NewProjectFilter(nil)
		require.NoError(t, err)
		assert.True(t, f.Matches(&gitlab.Project{ID: 1, Name: "test"}))
	})

	t.Run("InvalidRegex_ReturnsError", func(t *testing.T) {
		_, err := discovery.NewProjectFilter(&config.ProjectSelector{
			ProjectNameRegexInclude: "[invalid",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid project_name_regex_include")

		_, err = discovery.NewProjectFilter(&config.ProjectSelector{
			ProjectNameRegexExclude: "(?P<invalid",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid project_name_regex_exclude")
	})
}

func TestProjectFilter_NamespaceMatching(t *testing.T) {
	p := &gitlab.Project{
		ID:                10,
		Name:              "my-service",
		PathWithNamespace: "platform/infra/my-service",
		Namespace: &gitlab.ProjectNamespace{
			FullPath: "platform/infra",
		},
	}

	t.Run("IncludeExactAndPrefix", func(t *testing.T) {
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			NamespacesInclude: []string{"platform/infra"},
		})
		require.NoError(t, err)
		assert.True(t, f.Matches(p))

		fParent, err := discovery.NewProjectFilter(&config.ProjectSelector{
			NamespacesInclude: []string{"platform"},
		})
		require.NoError(t, err)
		assert.True(t, fParent.Matches(p))
	})

	t.Run("PrefixFalsePositiveBoundary", func(t *testing.T) {
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			NamespacesInclude: []string{"plat"},
		})
		require.NoError(t, err)
		assert.False(t, f.Matches(p), "'plat' must not match 'platform/infra'")
	})

	t.Run("ExcludePrecedenceOverInclude", func(t *testing.T) {
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			NamespacesInclude: []string{"platform"},
			NamespacesExclude: []string{"platform/infra"},
		})
		require.NoError(t, err)
		assert.False(t, f.Matches(p))

		res := f.Evaluate(p)
		assert.Equal(t, discovery.DecisionExcludedNamespace, res.Decision)
	})

	t.Run("FallbackToPathWithNamespaceWhenNamespaceNil", func(t *testing.T) {
		pNoNS := &gitlab.Project{
			ID:                12,
			Name:              "my-service",
			PathWithNamespace: "platform/infra/sub/my-service",
		}
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			NamespacesInclude: []string{"platform/infra"},
		})
		require.NoError(t, err)
		assert.True(t, f.Matches(pNoNS))
	})
}

func TestProjectFilter_RegexMatching(t *testing.T) {
	p1 := &gitlab.Project{ID: 1, Name: "payment-gateway", Path: "payment-gateway"}
	p2 := &gitlab.Project{ID: 2, Name: "payment-gateway-deprecated", Path: "payment-gateway-deprecated"}
	p3 := &gitlab.Project{ID: 3, Name: "user-service", Path: "user-service"}

	f, err := discovery.NewProjectFilter(&config.ProjectSelector{
		ProjectNameRegexInclude: `^payment-.*`,
		ProjectNameRegexExclude: `.*-deprecated$`,
	})
	require.NoError(t, err)

	assert.True(t, f.Matches(p1))
	assert.False(t, f.Matches(p2), "deprecated project must be excluded")
	assert.False(t, f.Matches(p3), "user service does not match include regex")

	res := f.Evaluate(p2)
	assert.Equal(t, discovery.DecisionExcludedRegex, res.Decision)

	res3 := f.Evaluate(p3)
	assert.Equal(t, discovery.DecisionMissingRegex, res3.Decision)
}

func TestProjectFilter_TopicsMatching(t *testing.T) {
	p := &gitlab.Project{
		ID:     1,
		Name:   "billing-engine",
		Topics: []string{"Golang", "PCI-DSS", "Tier-1"},
	}

	t.Run("CaseInsensitiveInclude", func(t *testing.T) {
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			TopicsInclude: []string{"pci-dss"},
		})
		require.NoError(t, err)
		assert.True(t, f.Matches(p))
	})

	t.Run("TagListFallback", func(t *testing.T) {
		pTag := &gitlab.Project{
			ID:      2,
			Name:    "legacy-tag",
			TagList: []string{"production"},
		}
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			TopicsInclude: []string{"production"},
		})
		require.NoError(t, err)
		assert.True(t, f.Matches(pTag))
	})

	t.Run("ExcludePrecedence", func(t *testing.T) {
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			TopicsInclude: []string{"golang"},
			TopicsExclude: []string{"tier-1"},
		})
		require.NoError(t, err)
		assert.False(t, f.Matches(p))

		res := f.Evaluate(p)
		assert.Equal(t, discovery.DecisionExcludedTopic, res.Decision)
	})

	t.Run("MissingTopic", func(t *testing.T) {
		f, err := discovery.NewProjectFilter(&config.ProjectSelector{
			TopicsInclude: []string{"nonexistent-topic"},
		})
		require.NoError(t, err)
		assert.False(t, f.Matches(p))

		res := f.Evaluate(p)
		assert.Equal(t, discovery.DecisionMissingTopic, res.Decision)
	})
}

func TestProjectFilter_VisibilityAndArchived(t *testing.T) {
	pPubActive := &gitlab.Project{ID: 1, Visibility: gitlab.PublicVisibility, Archived: false}
	pPubArchived := &gitlab.Project{ID: 2, Visibility: gitlab.PublicVisibility, Archived: true}
	pPrivActive := &gitlab.Project{ID: 3, Visibility: gitlab.PrivateVisibility, Archived: false}

	// Active only
	archivedFalse := false
	fActive, err := discovery.NewProjectFilter(&config.ProjectSelector{
		Archived: &archivedFalse,
	})
	require.NoError(t, err)
	assert.True(t, fActive.Matches(pPubActive))
	assert.False(t, fActive.Matches(pPubArchived))

	resArch := fActive.Evaluate(pPubArchived)
	assert.Equal(t, discovery.DecisionMismatchArchived, resArch.Decision)

	// Visibility private only
	fPriv, err := discovery.NewProjectFilter(&config.ProjectSelector{
		Visibility: "private",
	})
	require.NoError(t, err)
	assert.False(t, fPriv.Matches(pPubActive))
	assert.True(t, fPriv.Matches(pPrivActive))

	resVis := fPriv.Evaluate(pPubActive)
	assert.Equal(t, discovery.DecisionMismatchVisibility, resVis.Decision)

	// Visibility "any" matches everything
	fAny, err := discovery.NewProjectFilter(&config.ProjectSelector{
		Visibility: "any",
	})
	require.NoError(t, err)
	assert.True(t, fAny.Matches(pPubActive))
	assert.True(t, fAny.Matches(pPrivActive))
}

func TestProjectFilter_IDRange(t *testing.T) {
	f, err := discovery.NewProjectFilter(&config.ProjectSelector{
		IDRange: &config.IDRange{Min: 100, Max: 200},
	})
	require.NoError(t, err)

	assert.False(t, f.Matches(&gitlab.Project{ID: 99}))
	assert.True(t, f.Matches(&gitlab.Project{ID: 100}))
	assert.True(t, f.Matches(&gitlab.Project{ID: 150}))
	assert.True(t, f.Matches(&gitlab.Project{ID: 200}))
	assert.False(t, f.Matches(&gitlab.Project{ID: 201}))

	resMin := f.Evaluate(&gitlab.Project{ID: 99})
	assert.Equal(t, discovery.DecisionMismatchIDRange, resMin.Decision)

	resMax := f.Evaluate(&gitlab.Project{ID: 201})
	assert.Equal(t, discovery.DecisionMismatchIDRange, resMax.Decision)
}

func TestProjectFilter_NilProject(t *testing.T) {
	f, err := discovery.NewProjectFilter(nil)
	require.NoError(t, err)

	res := f.Evaluate(nil)
	assert.False(t, res.Matched)
	assert.Equal(t, discovery.DecisionNilProject, res.Decision)
}

func TestProjectFilter_CombinedMultiCriteriaMatrix(t *testing.T) {
	archivedFalse := false
	sel := &config.ProjectSelector{
		NamespacesInclude:       []string{"enterprise/platform"},
		NamespacesExclude:       []string{"enterprise/platform/sandbox"},
		ProjectNameRegexInclude: `^srv-.*`,
		ProjectNameRegexExclude: `.*-test$`,
		TopicsInclude:           []string{"prod"},
		TopicsExclude:           []string{"deprecated"},
		Visibility:              "internal",
		Archived:                &archivedFalse,
		IDRange:                 &config.IDRange{Min: 50, Max: 150},
	}
	f, err := discovery.NewProjectFilter(sel)
	require.NoError(t, err)

	// Perfect match
	perfect := &gitlab.Project{
		ID:                100,
		Name:              "srv-auth",
		Path:              "srv-auth",
		PathWithNamespace: "enterprise/platform/core/srv-auth",
		Namespace:         &gitlab.ProjectNamespace{FullPath: "enterprise/platform/core"},
		Topics:            []string{"prod", "security"},
		Visibility:        gitlab.InternalVisibility,
		Archived:          false,
	}
	assert.True(t, f.Matches(perfect))

	// Rejection checks on each single dimension
	t.Run("RejectsExcludedNamespace", func(t *testing.T) {
		p := *perfect
		p.Namespace = &gitlab.ProjectNamespace{FullPath: "enterprise/platform/sandbox"}
		assert.False(t, f.Matches(&p))
	})
	t.Run("RejectsExcludedRegex", func(t *testing.T) {
		p := *perfect
		p.Name = "srv-auth-test"
		assert.False(t, f.Matches(&p))
	})
	t.Run("RejectsExcludedTopic", func(t *testing.T) {
		p := *perfect
		p.Topics = []string{"prod", "deprecated"}
		assert.False(t, f.Matches(&p))
	})
	t.Run("RejectsArchived", func(t *testing.T) {
		p := *perfect
		p.Archived = true
		assert.False(t, f.Matches(&p))
	})
	t.Run("RejectsMismatchVisibility", func(t *testing.T) {
		p := *perfect
		p.Visibility = gitlab.PublicVisibility
		assert.False(t, f.Matches(&p))
	})
	t.Run("RejectsOutOfRangeID", func(t *testing.T) {
		p := *perfect
		p.ID = 250
		assert.False(t, f.Matches(&p))
	})
}
