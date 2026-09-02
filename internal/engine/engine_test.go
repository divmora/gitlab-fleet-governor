package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

func setupTestEngine(t *testing.T, dryRun bool) (*engine.GovernanceEngine, *mockserver.MockGitLabServer) {
	t.Helper()
	server := mockserver.NewMockGitLabServer()
	server.Seed()

	client, err := server.GovernorClient()
	if err != nil {
		t.Fatalf("failed to create gitlab client: %v", err)
	}

	dryRunPtr := dryRun
	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			DryRun:      &dryRunPtr,
			Concurrency: 4,
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{10},
				Recursive:       gogitlab.Ptr(false),
			},
		},
		Policies: config.PoliciesConfig{
			PushRules: &config.PushRulesConfig{
				PreventSecrets: gogitlab.Ptr(true),
			},
		},
	}

	eng, err := engine.NewGovernanceEngine(client, cfg)
	if err != nil {
		t.Fatalf("failed to create governance engine: %v", err)
	}

	return eng, server
}

func TestGovernanceEngine_DryRun_PlanMode(t *testing.T) {
	eng, server := setupTestEngine(t, true)
	defer server.Close()

	ctx := context.Background()
	result, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected engine run error: %v", err)
	}

	if !result.DryRun {
		t.Errorf("expected DryRun=true, got false")
	}
	if result.Mode != "plan" {
		t.Errorf("expected Mode='plan', got '%s'", result.Mode)
	}
	if !result.Success {
		t.Errorf("expected Success=true, got false")
	}
	if result.Metrics == nil {
		t.Fatalf("expected non-nil metrics")
	}
	if eng.Concurrency() != 4 {
		t.Errorf("expected Concurrency=4, got %d", eng.Concurrency())
	}
	if !eng.IsDryRun() {
		t.Errorf("expected IsDryRun=true")
	}
}

func TestGovernanceEngine_ApplyMode(t *testing.T) {
	eng, server := setupTestEngine(t, false)
	defer server.Close()

	ctx := context.Background()
	result, err := eng.Apply(ctx)
	if err != nil {
		t.Fatalf("unexpected engine apply error: %v", err)
	}

	if result.DryRun {
		t.Errorf("expected DryRun=false in Apply mode, got true")
	}
	if result.Mode != "apply" {
		t.Errorf("expected Mode='apply', got '%s'", result.Mode)
	}
	if !result.Success {
		t.Errorf("expected Success=true, got false")
	}
}

func TestGovernanceEngine_PlanExplicit(t *testing.T) {
	eng, server := setupTestEngine(t, false)
	defer server.Close()

	ctx := context.Background()
	result, err := eng.Plan(ctx)
	if err != nil {
		t.Fatalf("unexpected engine plan error: %v", err)
	}

	if !result.DryRun {
		t.Errorf("expected DryRun=true in Plan mode, got false")
	}
	if result.Mode != "plan" {
		t.Errorf("expected Mode='plan', got '%s'", result.Mode)
	}
}

func TestGovernanceEngine_ExecuteMethod(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()
	server.Seed()

	client, err := server.GovernorClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	eng := engine.NewEngine(client, engine.WithConcurrency(2), engine.WithDryRun(true))

	dryRunVal := true
	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			DryRun:      &dryRunVal,
			Concurrency: 2,
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{10},
				Recursive:       gogitlab.Ptr(false),
			},
		},
	}

	result, err := eng.Execute(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error in Execute: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}

	// Test Execute with nil cfg
	_, err = eng.Execute(context.Background(), nil)
	if !errors.Is(err, engine.ErrNilConfig) {
		t.Fatalf("expected ErrNilConfig, got %v", err)
	}
}

func TestGovernanceEngine_EmptyFleet(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()

	client, err := server.GovernorClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	server.State().AddGroup(&gogitlab.Group{
		ID:       10,
		Name:     "Empty Group",
		Path:     "empty-group",
		FullPath: "empty-group",
	})

	cfg := &config.PolicyConfig{
		Settings: config.SettingsConfig{Concurrency: 2},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupPathsExclude: []string{"empty-group"},
			},
		},
	}

	eng, err := engine.NewGovernanceEngine(client, cfg)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	result, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error on empty fleet: %v", err)
	}
	if len(result.TargetResults) != 0 {
		t.Errorf("expected 0 target results, got %d", len(result.TargetResults))
	}
	if !result.Success {
		t.Errorf("expected Success=true for empty fleet")
	}
}

func TestGovernanceEngine_NilParameters(t *testing.T) {
	_, err := engine.NewGovernanceEngine(nil, &config.PolicyConfig{})
	if !errors.Is(err, engine.ErrNilClient) {
		t.Errorf("expected ErrNilClient, got %v", err)
	}

	mockClient := &mockGitLabClientStub{}
	_, err = engine.NewGovernanceEngine(mockClient, nil)
	if !errors.Is(err, engine.ErrNilConfig) {
		t.Errorf("expected ErrNilConfig, got %v", err)
	}
}

func TestGovernanceEngine_TargetResultHelpers(t *testing.T) {
	tr := &engine.TargetResult{
		TargetID:     101,
		TargetPath:   "platform/fleet-governor",
		TargetName:   "fleet-governor",
		ResourceType: governance.ResourceTypeProject,
		DryRun:       true,
		Success:      true,
		HasChanges:   true,
		Operations: []*engine.OperationResult{
			{
				OperationName: "push_rules",
				Action:        governance.ActionUpdate,
				HasChanges:    true,
				Success:       true,
				Diffs: []governance.Diff{
					{
						Resource: "push_rule",
						Action:   governance.ActionUpdate,
						Fields: []governance.FieldDiff{
							{Field: "prevent_secrets", OldValue: false, NewValue: true, Action: governance.ActionUpdate},
						},
					},
				},
			},
			{
				OperationName: "protected_branches",
				Action:        governance.ActionNoop,
				HasChanges:    false,
				Success:       true,
			},
			{
				OperationName: "approval_rules",
				Action:        governance.ActionUpdate,
				HasChanges:    false,
				Success:       false,
				Error:         errors.New("permission denied"),
			},
		},
	}

	changedOps := tr.ChangedOperations()
	if len(changedOps) != 1 || changedOps[0].OperationName != "push_rules" {
		t.Errorf("unexpected changed ops: %v", changedOps)
	}

	failedOps := tr.FailedOperations()
	if len(failedOps) != 1 || failedOps[0].OperationName != "approval_rules" {
		t.Errorf("unexpected failed ops: %v", failedOps)
	}

	diffs := tr.Diffs()
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff, got %d", len(diffs))
	}

	execRes := &engine.ExecutionResult{
		TargetResults: []*engine.TargetResult{tr},
		Metrics: &engine.SummaryMetricsSnapshot{
			TotalChanged: 1,
			TotalFailed:  0,
		},
		Success: true,
	}

	if !execRes.HasChanges() {
		t.Errorf("expected HasChanges=true")
	}
	if execRes.HasErrors() {
		t.Errorf("expected HasErrors=false")
	}
	if len(execRes.ChangedTargets()) != 1 {
		t.Errorf("expected 1 changed target")
	}
	if len(execRes.UnchangedTargets()) != 0 {
		t.Errorf("expected 0 unchanged targets")
	}
	if len(execRes.FailedTargets()) != 0 {
		t.Errorf("expected 0 failed targets")
	}
	if execRes.TotalDiffs() != 1 {
		t.Errorf("expected TotalDiffs=1, got %d", execRes.TotalDiffs())
	}
}

func TestGovernanceEngine_ConcurrentReentrantExecution(t *testing.T) {
	eng, server := setupTestEngine(t, true)
	defer server.Close()

	ctx := context.Background()
	done := make(chan bool, 4)

	go func() {
		res, err := eng.Plan(ctx)
		if err != nil || !res.DryRun {
			t.Errorf("plan error: %v", err)
		}
		done <- true
	}()

	go func() {
		res, err := eng.Apply(ctx)
		if err != nil || res.DryRun {
			t.Errorf("apply error: %v", err)
		}
		done <- true
	}()

	go func() {
		res, err := eng.Run(ctx)
		if err != nil || !res.DryRun {
			t.Errorf("run error: %v", err)
		}
		done <- true
	}()

	go func() {
		res, err := eng.Plan(ctx)
		if err != nil || !res.DryRun {
			t.Errorf("plan error: %v", err)
		}
		done <- true
	}()

	for i := 0; i < 4; i++ {
		<-done
	}
}

type mockGitLabClientStub struct {
	gitlab.GitLabClient
}
