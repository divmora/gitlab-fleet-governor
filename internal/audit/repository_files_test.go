package audit_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func TestRepositoryFilesAuditor(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()
	server.Seed()

	client, err := server.GovernorClient()
	require.NoError(t, err)

	proj, found := server.State().GetProject(101)
	require.True(t, found)

	targetProj := &discovery.TargetProject{
		ID:                proj.ID,
		Name:              proj.Name,
		Path:              proj.Path,
		PathWithNamespace: proj.PathWithNamespace,
		DefaultBranch:     "main",
		Raw:               proj,
	}

	auditor := audit.NewRepositoryFilesAuditor()
	assert.Equal(t, "repository_files", auditor.Name())

	t.Run("NoPolicyConfig_YieldsZeroFindings", func(t *testing.T) {
		findings, err := auditor.AuditProject(context.Background(), client, targetProj, nil)
		require.NoError(t, err)
		assert.Empty(t, findings)
	})

	t.Run("MissingFile_FlaggedAsHighSeverity", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "MISSING_CODEOWNERS",
						Content:      "* @sec",
						TargetBranch: "main",
					},
				},
			},
		}

		findings, err := auditor.AuditProject(context.Background(), client, targetProj, cfg)
		require.NoError(t, err)
		require.Len(t, findings, 1)

		f := findings[0]
		assert.Equal(t, "MISSING_CODEOWNERS", f.FilePath)
		assert.False(t, f.FileExists)
		assert.Equal(t, "MISSING_FILE", f.ViolationType)
		assert.Equal(t, audit.SeverityHigh, f.Severity)
	})

	t.Run("ContentDrift_FlaggedAsMediumSeverity", func(t *testing.T) {
		server.State().SetFile(101, "SECURITY.md", "main", []byte("# Old Policy"))

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path:         "SECURITY.md",
						Content:      "# New Enterprise Security Policy",
						TargetBranch: "main",
						Enforcement:  "direct_commit",
					},
				},
			},
		}

		findings, err := auditor.AuditProject(context.Background(), client, targetProj, cfg)
		require.NoError(t, err)
		require.Len(t, findings, 1)

		f := findings[0]
		assert.Equal(t, "SECURITY.md", f.FilePath)
		assert.True(t, f.FileExists)
		assert.Equal(t, "CONTENT_DRIFT", f.ViolationType)
		assert.Equal(t, audit.SeverityMedium, f.Severity)
	})

	t.Run("MissingEnsureContainsSubstrings_Flagged", func(t *testing.T) {
		server.State().SetFile(101, ".gitlab-ci.yml", "main", []byte("stages:\n  - build"))

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				RepositoryFiles: []config.RepositoryFileConfig{
					{
						Path: ".gitlab-ci.yml",
						EnsureContains: []string{
							"include:",
							"project: 'platform/templates'",
						},
						TargetBranch: "main",
					},
				},
			},
		}

		findings, err := auditor.AuditProject(context.Background(), client, targetProj, cfg)
		require.NoError(t, err)
		require.Len(t, findings, 1)

		f := findings[0]
		assert.Equal(t, ".gitlab-ci.yml", f.FilePath)
		assert.True(t, f.FileExists)
		assert.Equal(t, "MISSING_REQUIRED_STRINGS", f.ViolationType)
		assert.Equal(t, audit.SeverityMedium, f.Severity)
	})
}
