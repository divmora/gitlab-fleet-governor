package mockserver

import (
	"net/http"
	"net/http/httptest"
	"time"

	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// MockGitLabServer is an in-memory HTTP test server simulating GitLab v4 REST API.
type MockGitLabServer struct {
	server *httptest.Server
	state  *State
	faults *FaultEngine
	router *Router
}

// NewMockGitLabServer creates and starts a new MockGitLabServer.
func NewMockGitLabServer() *MockGitLabServer {
	state := NewState()
	faults := NewFaultEngine()
	router := NewRouter(state, faults)

	server := httptest.NewServer(router)

	return &MockGitLabServer{
		server: server,
		state:  state,
		faults: faults,
		router: router,
	}
}

// URL returns the base HTTP URL of the server (e.g. "http://127.0.0.1:12345").
func (s *MockGitLabServer) URL() string {
	return s.server.URL
}

// BaseURL returns the GitLab API v4 base URL (e.g. "http://127.0.0.1:12345/api/v4").
func (s *MockGitLabServer) BaseURL() string {
	return s.server.URL + "/api/v4"
}

// HTTPClient returns the underlying *http.Client configured for this test server.
func (s *MockGitLabServer) HTTPClient() *http.Client {
	return s.server.Client()
}

// Close shuts down the underlying HTTP test server.
func (s *MockGitLabServer) Close() {
	s.server.Close()
}

// State returns the in-memory state store.
func (s *MockGitLabServer) State() *State {
	return s.state
}

// Faults returns the fault injection engine.
func (s *MockGitLabServer) Faults() *FaultEngine {
	return s.faults
}

// Reset clears all in-memory state and active fault rules.
func (s *MockGitLabServer) Reset() {
	s.state.Reset()
	s.faults.Clear()
}

// Client creates an official gitlab.Client configured to point to this mock server.
func (s *MockGitLabServer) Client(opts ...gitlab.ClientOptionFunc) (*gitlab.Client, error) {
	allOpts := append([]gitlab.ClientOptionFunc{
		gitlab.WithBaseURL(s.BaseURL()),
	}, opts...)

	return gitlab.NewClient("mock-token", allOpts...)
}

// GovernorClient creates a high-level Client configured to point to this mock server.
func (s *MockGitLabServer) GovernorClient(opts ...gl.ClientOption) (*gl.Client, error) {
	auth := &gl.ResolvedAuth{
		BaseURL:   s.BaseURL(),
		Token:     "mock-token",
		TokenType: gl.TokenTypePrivate,
	}
	return gl.NewClient(auth, opts...)
}

// Seed populates the server with a rich set of standard test fixtures.
func (s *MockGitLabServer) Seed() {
	now := time.Now()

	// 1. Users
	u1 := s.state.AddUser(&gitlab.User{
		ID:       1,
		Username: "alice",
		Name:     "Alice Smith",
		Email:    "alice@example.com",
		State:    "active",
	})
	u2 := s.state.AddUser(&gitlab.User{
		ID:       2,
		Username: "bob",
		Name:     "Bob Jones",
		Email:    "bob@example.com",
		State:    "active",
	})
	u3 := s.state.AddUser(&gitlab.User{
		ID:       3,
		Username: "carol",
		Name:     "Carol Danvers",
		Email:    "carol@example.com",
		State:    "active",
	})
	_ = s.state.AddUser(&gitlab.User{
		ID:       4,
		Username: "admin",
		Name:     "Administrator",
		Email:    "admin@example.com",
		State:    "active",
	})

	// 2. Groups
	g1 := s.state.AddGroup(&gitlab.Group{
		ID:       10,
		Name:     "Platform Engineering",
		Path:     "platform",
		FullPath: "platform",
	})
	g2 := s.state.AddGroup(&gitlab.Group{
		ID:       20,
		Name:     "Security",
		Path:     "security",
		FullPath: "security",
	})
	// Subgroup
	g1Sub := s.state.AddGroup(&gitlab.Group{
		ID:       11,
		Name:     "Core Infrastructure",
		Path:     "core",
		FullPath: "platform/core",
		ParentID: 10,
	})

	// 3. Projects
	p1 := s.state.AddProject(&gitlab.Project{
		ID:                101,
		Name:              "fleet-governor",
		Path:              "fleet-governor",
		PathWithNamespace: "platform/fleet-governor",
		DefaultBranch:     "main",
		Visibility:        gitlab.PrivateVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	s.state.AddGroupProject(g1.ID, p1.ID)

	p2 := s.state.AddProject(&gitlab.Project{
		ID:                102,
		Name:              "cloud-infra",
		Path:              "cloud-infra",
		PathWithNamespace: "platform/core/cloud-infra",
		DefaultBranch:     "main",
		Visibility:        gitlab.InternalVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	s.state.AddGroupProject(g1Sub.ID, p2.ID)

	p3 := s.state.AddProject(&gitlab.Project{
		ID:                103,
		Name:              "security-tools",
		Path:              "security-tools",
		PathWithNamespace: "security/security-tools",
		DefaultBranch:     "master",
		Visibility:        gitlab.PublicVisibility,
		Archived:          false,
		CreatedAt:         &now,
	})
	s.state.AddGroupProject(g2.ID, p3.ID)

	// 4. Push Rules
	s.state.SetProjectPushRule(p1.ID, &gitlab.ProjectPushRules{
		ID:                 p1.ID,
		AuthorEmailRegex:   `@example\.com$`,
		BranchNameRegex:    `^(main|release/.*|feat/.*)$`,
		CommitMessageRegex: `^\[(FEAT|FIX|CHORE|DOCS)\]`,
		PreventSecrets:     true,
		DenyDeleteTag:      true,
		MaxFileSize:        10,
	})

	s.state.SetGroupPushRule(g1.ID, &gitlab.GroupPushRules{
		ID:                 g1.ID,
		AuthorEmailRegex:   `@example\.com$`,
		PreventSecrets:     true,
		RejectUnsignedCommits: true,
	})

	// 5. Protected Branches
	s.state.ProtectBranch(p1.ID, &gitlab.ProtectedBranch{
		ID:   1,
		Name: "main",
		PushAccessLevels: []*gitlab.BranchAccessDescription{
			{AccessLevel: gitlab.MaintainerPermissions, AccessLevelDescription: "Maintainers"},
		},
		MergeAccessLevels: []*gitlab.BranchAccessDescription{
			{AccessLevel: gitlab.DeveloperPermissions, AccessLevelDescription: "Developers + Maintainers"},
		},
		AllowForcePush:            false,
		CodeOwnerApprovalRequired: true,
	})

	// 6. MR Approvals & Rules
	s.state.SetProjectApprovals(p1.ID, &gitlab.ProjectApprovals{
		ApprovalsBeforeMerge: 2,
		ResetApprovalsOnPush: true,
	})

	s.state.AddApprovalRule(p1.ID, &gitlab.ProjectApprovalRule{
		ID:                1,
		Name:              "Security Review",
		ApprovalsRequired: 1,
		EligibleApprovers: []*gitlab.BasicUser{
			{ID: u2.ID, Username: u2.Username, Name: u2.Name},
		},
		Users: []*gitlab.BasicUser{
			{ID: u2.ID, Username: u2.Username, Name: u2.Name},
		},
	})

	// 7. CI/CD Variables
	s.state.SetProjectVariable(p1.ID, &gitlab.ProjectVariable{
		Key:              "AWS_REGION",
		Value:            "us-east-1",
		VariableType:     "env_var",
		Protected:        true,
		Masked:           false,
		EnvironmentScope: "*",
	})
	s.state.SetGroupVariable(g1.ID, &gitlab.GroupVariable{
		Key:              "ORGANIZATION_NAME",
		Value:            "FleetCorp",
		VariableType:     "env_var",
		EnvironmentScope: "*",
	})

	// 8. Runners
	r1 := s.state.AddRunner(&gitlab.Runner{
		ID:          1,
		Description: "shared-runner-01",
		IsShared:    true,
		Active:      true,
		Online:      true,
	}, &gitlab.RunnerDetails{
		ID:          1,
		Description: "shared-runner-01",
		Active:      true,
		Paused:      false,
		Locked:      false,
		TagList:     []string{"docker", "linux"},
		AccessLevel: "not_protected",
	})
	_ = r1

	// 9. Compliance Framework
	s.state.SetComplianceFramework(p1.ID, MockComplianceFramework{
		ID:          "gid://gitlab/ComplianceManagement::Framework/1",
		Name:        "SOC2",
		Description: "SOC2 Compliance Baseline",
		Color:       "#0052cc",
		Default:     true,
	})

	// 10. Webhooks
	s.state.AddProjectHook(p1.ID, &gitlab.ProjectHook{
		ID:                    1,
		URL:                   "https://webhook.fleetcorp.com/events",
		PushEvents:            true,
		MergeRequestsEvents:   true,
		EnableSSLVerification: true,
	})

	// 11. Members
	s.state.AddProjectMember(p1.ID, &gitlab.ProjectMember{
		ID:          u1.ID,
		Username:    u1.Username,
		Name:        u1.Name,
		AccessLevel: gitlab.MaintainerPermissions,
	})
	s.state.AddGroupMember(g1.ID, &gitlab.GroupMember{
		ID:          u3.ID,
		Username:    u3.Username,
		Name:        u3.Name,
		AccessLevel: gitlab.OwnerPermissions,
	})
}

// SeedCircularSubgroups seeds a circular subgroup dependency to test discovery cycle detection.
// Cycle: Group 500 -> Subgroup 501 -> Subgroup 502 -> Subgroup 500
func (s *MockGitLabServer) SeedCircularSubgroups() {
	s.state.AddGroup(&gitlab.Group{
		ID:       500,
		Name:     "Circular Root",
		Path:     "circ-root",
		FullPath: "circ-root",
	})
	s.state.AddGroup(&gitlab.Group{
		ID:       501,
		Name:     "Circular Child A",
		Path:     "child-a",
		FullPath: "circ-root/child-a",
		ParentID: 500,
	})
	s.state.AddGroup(&gitlab.Group{
		ID:       502,
		Name:     "Circular Child B",
		Path:     "child-b",
		FullPath: "circ-root/child-a/child-b",
		ParentID: 501,
	})

	// Inject artificial cycle in state.subgroups: 502 -> 500
	s.state.mu.Lock()
	s.state.subgroups[502] = append(s.state.subgroups[502], 500)
	s.state.mu.Unlock()
}
