package audit_test

import (
	"context"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/audit"
	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func setupMockGitLab(t *testing.T) (*mockserver.MockGitLabServer, gitlab.GitLabClient, *discovery.TargetProject) {
	t.Helper()
	server := mockserver.NewMockGitLabServer()
	t.Cleanup(func() {
		server.Close()
	})

	// Add test project
	proj := &gogitlab.Project{
		ID:                101,
		Name:              "payments-api",
		Path:              "payments-api",
		PathWithNamespace: "fintech/payments-api",
		DefaultBranch:     "main",
		WebURL:            server.URL() + "/fintech/payments-api",
	}
	server.State().AddProject(proj)

	client, err := server.GovernorClient()
	require.NoError(t, err)

	targetProj := &discovery.TargetProject{
		ID:                proj.ID,
		Name:              proj.Name,
		Path:              proj.Path,
		PathWithNamespace: proj.PathWithNamespace,
		DefaultBranch:     proj.DefaultBranch,
		Raw:               proj,
	}

	return server, client, targetProj
}

func TestUserAccessAuditor(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Add members:
	// 1. Owner (Level 50) without expiration -> Compliant (Owners can be indefinite)
	// 2. Maintainer (Level 40) without expiration -> CRITICAL violation
	// 3. Developer (Level 30) without expiration -> HIGH violation
	// 4. Developer (Level 30) with expiration -> Compliant
	server.State().AddProjectMember(targetProj.ID, &gogitlab.ProjectMember{
		ID:          1,
		Username:    "alice_owner",
		Name:        "Alice Owner",
		AccessLevel: gogitlab.OwnerPermissions,
		ExpiresAt:   nil,
	})
	server.State().AddProjectMember(targetProj.ID, &gogitlab.ProjectMember{
		ID:          2,
		Username:    "bob_maint",
		Name:        "Bob Maintainer",
		AccessLevel: gogitlab.MaintainerPermissions,
		ExpiresAt:   nil,
	})
	isoTime := gogitlab.ISOTime(gogitlab.ISOTime{})
	_ = isoTime.UnmarshalJSON([]byte(`"2026-12-31"`))
	server.State().AddProjectMember(targetProj.ID, &gogitlab.ProjectMember{
		ID:          3,
		Username:    "carol_dev_exp",
		Name:        "Carol Developer",
		AccessLevel: gogitlab.DeveloperPermissions,
		ExpiresAt:   &isoTime,
	})
	server.State().AddProjectMember(targetProj.ID, &gogitlab.ProjectMember{
		ID:          4,
		Username:    "dan_dev_noexp",
		Name:        "Dan Developer",
		AccessLevel: gogitlab.DeveloperPermissions,
		ExpiresAt:   nil,
	})

	auditor := audit.NewUserAccessAuditor()
	assert.Equal(t, "user_access", auditor.Name())

	findings, err := auditor.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 4)

	findingMap := make(map[string]audit.UserAccessFinding)
	for _, f := range findings {
		findingMap[f.Username] = f
	}

	// Alice (Owner)
	alice := findingMap["alice_owner"]
	assert.Equal(t, audit.SeverityPass, alice.Severity)
	assert.False(t, alice.HasExpiration)

	// Bob (Maintainer indefinite)
	bob := findingMap["bob_maint"]
	assert.Equal(t, audit.SeverityCritical, bob.Severity)
	assert.Equal(t, "INDEFINITE_MAINTAINER_ACCESS", bob.ViolationType)

	// Carol (Developer with expiration)
	carol := findingMap["carol_dev_exp"]
	assert.Equal(t, audit.SeverityPass, carol.Severity)
	assert.True(t, carol.HasExpiration)

	// Dan (Developer indefinite)
	dan := findingMap["dan_dev_noexp"]
	assert.Equal(t, audit.SeverityHigh, dan.Severity)
	assert.Equal(t, "INDEFINITE_NON_OWNER_ACCESS", dan.ViolationType)
}

func TestProtectedBranchesAuditor(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// 1. Unprotected default branch test
	auditor := audit.NewProtectedBranchesAuditor()
	assert.Equal(t, "protected_branches", auditor.Name())

	findings, err := auditor.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "main", findings[0].BranchName)
	assert.Equal(t, audit.SeverityCritical, findings[0].Severity)
	assert.False(t, findings[0].IsProtected)

	// 2. Protected branch with force push and missing code owners
	server.State().ProtectBranch(targetProj.ID, &gogitlab.ProtectedBranch{
		ID:                        1,
		Name:                      "main",
		AllowForcePush:            true,
		CodeOwnerApprovalRequired: false,
		PushAccessLevels: []*gogitlab.BranchAccessDescription{
			{UserID: 99, AccessLevelDescription: "User ID 99"},
			{AccessLevel: gogitlab.DeveloperPermissions},
		},
		MergeAccessLevels: []*gogitlab.BranchAccessDescription{
			{AccessLevel: gogitlab.MaintainerPermissions},
		},
	})

	findings, err = auditor.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 1)

	mainFinding := findings[0]
	assert.Equal(t, "main", mainFinding.BranchName)
	assert.True(t, mainFinding.IsProtected)
	assert.True(t, mainFinding.AllowForcePush)
	assert.False(t, mainFinding.CodeOwnerApprovalRequired)
	assert.Equal(t, audit.SeverityCritical, mainFinding.Severity)
	assert.Contains(t, mainFinding.DirectUserPushGrants, "User ID 99")
	assert.Len(t, mainFinding.Violations, 4) // force push, direct user, code owner, unrestricted push
}

func TestProtectedEnvironmentsAuditor(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	auditor := audit.NewProtectedEnvironmentsAuditor()
	assert.Equal(t, "protected_environments", auditor.Name())

	// 1. Add non-compliant production environment (0 approvals, direct user deploy)
	server.State().ProtectEnvironment(targetProj.ID, &gogitlab.ProtectedEnvironment{
		Name:                  "production",
		RequiredApprovalCount: 0,
		DeployAccessLevels: []*gogitlab.EnvironmentAccessDescription{
			{UserID: 42, AccessLevelDescription: "User ID 42"},
			{AccessLevel: gogitlab.DeveloperPermissions},
		},
	})

	// 2. Add compliant staging environment
	server.State().ProtectEnvironment(targetProj.ID, &gogitlab.ProtectedEnvironment{
		Name:                  "staging",
		RequiredApprovalCount: 1,
		DeployAccessLevels: []*gogitlab.EnvironmentAccessDescription{
			{AccessLevel: gogitlab.MaintainerPermissions},
		},
	})

	findings, err := auditor.AuditProject(context.Background(), client, targetProj)
	require.NoError(t, err)
	require.Len(t, findings, 2)

	findingMap := make(map[string]audit.ProtectedEnvironmentFinding)
	for _, f := range findings {
		findingMap[f.EnvironmentName] = f
	}

	prod := findingMap["production"]
	assert.True(t, prod.IsProduction)
	assert.Equal(t, 0, prod.RequiredApprovalCount)
	assert.Equal(t, audit.SeverityCritical, prod.Severity)
	assert.Contains(t, prod.DirectUserDeployGrants, "User ID 42")

	staging := findingMap["staging"]
	assert.False(t, staging.IsProduction)
	assert.Equal(t, 1, staging.RequiredApprovalCount)
	assert.Equal(t, audit.SeverityPass, staging.Severity)
	assert.Empty(t, staging.Violations)
}

func TestAuditorCoordinator(t *testing.T) {
	server, client, targetProj := setupMockGitLab(t)

	// Populate full environment for targetProj
	server.State().AddProjectMember(targetProj.ID, &gogitlab.ProjectMember{
		ID:          10,
		Username:    "developer_one",
		AccessLevel: gogitlab.DeveloperPermissions,
	})
	server.State().ProtectBranch(targetProj.ID, &gogitlab.ProtectedBranch{
		ID:                        1,
		Name:                      "main",
		AllowForcePush:            false,
		CodeOwnerApprovalRequired: true,
		PushAccessLevels: []*gogitlab.BranchAccessDescription{
			{AccessLevel: gogitlab.MaintainerPermissions},
		},
	})
	server.State().ProtectEnvironment(targetProj.ID, &gogitlab.ProtectedEnvironment{
		Name:                  "production",
		RequiredApprovalCount: 2,
		DeployAccessLevels: []*gogitlab.EnvironmentAccessDescription{
			{AccessLevel: gogitlab.MaintainerPermissions},
		},
	})

	auditor, err := audit.NewAuditor(client,
		audit.WithAuditorConcurrency(5),
		audit.WithAuditorTargets(config.TargetSelectors{
			ProjectSelector: &config.ProjectSelector{
				NamespacesInclude: []string{"fintech"},
			},
		}),
	)
	require.NoError(t, err)

	report, err := auditor.Execute(context.Background())
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, 1, report.Summary.TotalProjectsScanned)
	assert.NotEmpty(t, report.ActiveModules)
	assert.Len(t, report.UserAccessFindings, 1)
	assert.Len(t, report.ProtectedBranchFindings, 1)
	assert.Len(t, report.ProtectedEnvFindings, 1)

	// User has no expiration -> 1 violation in summary
	assert.Equal(t, 1, report.Summary.TotalViolations)
	assert.Equal(t, 1, report.Summary.HighSeverityCount)
	assert.Equal(t, 0, report.Summary.CriticalSeverityCount)
}

func TestBotClassifier(t *testing.T) {
	classifier := audit.NewBotClassifier(
		[]string{"custom-agent", "ops-bot@custom.org", "999"},
		[]string{"*automation*", "*-ci@*"},
	)

	// 1. Explicit email match
	assert.True(t, classifier.IsBot(10, "bob", "Bob", "ops-bot@custom.org"))

	// 2. Explicit username match
	assert.True(t, classifier.IsBot(11, "custom-agent", "Agent Smith", "agent@custom.org"))

	// 3. Explicit numeric user ID match
	assert.True(t, classifier.IsBot(999, "unknown_user", "Random", "random@domain.com"))

	// 4. Custom pattern matches (wildcards in username/email)
	assert.True(t, classifier.IsBot(12, "my-automation-runner", "Runner", "runner@company.com"))
	assert.True(t, classifier.IsBot(13, "deployer", "Deployer", "deploy-ci@company.com"))

	// 5. Universal GitLab token and built-in bot conventions
	assert.True(t, classifier.IsBot(14, "project_101_bot_12345", "Token", ""))
	assert.True(t, classifier.IsBot(15, "group_202_bot_67890", "Group Token", ""))
	assert.True(t, classifier.IsBot(16, "renovate-bot", "Renovate", ""))
	assert.True(t, classifier.IsBot(17, "gitlab-bot", "GitLab Bot", ""))
	assert.True(t, classifier.IsBot(18, "service_account_984f1a", "Service Account", "service_account_984f1a@noreply.gitlab.example.com"))
	assert.True(t, classifier.IsBot(19, "ci_builder", "CI Builder", "service_account_customhash@noreply.mycompany.org"))

	// 6. Regular humans (must NOT match)
	assert.False(t, classifier.IsBot(1, "alice", "Alice Admin", "alice@company.com"))
	assert.False(t, classifier.IsBot(2, "john.doe", "John Doe", "john.doe@company.com"))
}
