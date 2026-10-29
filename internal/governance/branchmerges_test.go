package governance_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

func TestBranchMergesReconciler(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	reconciler := governance.NewBranchMergesReconciler()
	ctx := context.Background()

	proj := &gogitlab.Project{ID: 101, PathWithNamespace: "platform/fleet-governor"}

	t.Run("NameAndOrder", func(t *testing.T) {
		assert.Equal(t, "branch_merges", reconciler.Name())
		assert.Equal(t, 24, reconciler.Order())
	})

	t.Run("NilOrEmptyPolicy_YieldsNoop", func(t *testing.T) {
		planRes, err := reconciler.Plan(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planRes.Action)
		assert.False(t, planRes.HasChanges)

		applyRes, err := reconciler.Apply(ctx, client, proj, nil)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
		assert.True(t, applyRes.Success)

		emptyCfg := &config.PolicyConfig{Policies: config.PoliciesConfig{BranchMerges: []config.BranchMergeRuleConfig{}}}
		planRes2, err := reconciler.Plan(ctx, client, proj, emptyCfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planRes2.Action)
	})

	t.Run("Group_Skipped", func(t *testing.T) {
		group := &gogitlab.Group{ID: 10, FullPath: "platform"}
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				BranchMerges: []config.BranchMergeRuleConfig{
					{SourceBranch: "feature", TargetBranch: "main"},
				},
			},
		}

		planRes, err := reconciler.PlanGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, planRes.Action)

		applyRes, err := reconciler.ApplyGroup(ctx, client, group, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionSkipped, applyRes.Action)
	})

	t.Run("MissingBranch_ReturnsError", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				BranchMerges: []config.BranchMergeRuleConfig{
					{SourceBranch: "nonexistent-branch", TargetBranch: "main"},
				},
			},
		}

		_, err := reconciler.Plan(ctx, client, proj, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source branch 'nonexistent-branch' not found")

		_, err = reconciler.Apply(ctx, client, proj, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "source branch 'nonexistent-branch' not found")
	})

	t.Run("IdenticalBranches_YieldsNoop", func(t *testing.T) {
		// Create two branches pointing to the same commit sha
		srv.State().CreateBranch(proj.ID, "sync-target", "")
		tgtBranch, _ := srv.State().GetBranch(proj.ID, "sync-target")
		require.NotNil(t, tgtBranch)

		srv.State().CreateBranch(proj.ID, "sync-source", "")
		// Set same commit sha
		srcBranch, _ := srv.State().GetBranch(proj.ID, "sync-source")
		require.NotNil(t, srcBranch)
		srcBranch.Commit.ID = tgtBranch.Commit.ID

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				BranchMerges: []config.BranchMergeRuleConfig{
					{SourceBranch: "sync-source", TargetBranch: "sync-target"},
				},
			},
		}

		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, planRes.Action)

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionNoop, applyRes.Action)
	})

	t.Run("CleanBranches_CreatedAndAutoMerged", func(t *testing.T) {
		srv.State().CreateBranch(proj.ID, "feature-clean", "")
		srv.State().CreateBranch(proj.ID, "main-clean", "")

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				BranchMerges: []config.BranchMergeRuleConfig{
					{
						SourceBranch: "feature-clean",
						TargetBranch: "main-clean",
						Title:        "Merge feature-clean into main-clean",
						AutoMerge:    gogitlab.Ptr(true),
					},
				},
			},
		}

		// Plan verifies ActionCreate
		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, planRes.Action)
		assert.True(t, planRes.HasChanges)

		// Apply creates MR and merges it
		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)
		assert.Equal(t, governance.ActionUpdate, applyRes.Action)

		// Verify on server state that MR is now merged
		mrs := srv.State().ListMergeRequests(proj.ID, "feature-clean", "main-clean", "merged")
		require.Len(t, mrs, 1)
		assert.Equal(t, "merged", mrs[0].State)
	})

	t.Run("ConflictingBranches_CreatedAndLeftOpen", func(t *testing.T) {
		// Mock server treats branches containing "conflict" as having conflicts
		srv.State().CreateBranch(proj.ID, "feature-conflict", "")
		srv.State().CreateBranch(proj.ID, "main-conflict", "")

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				BranchMerges: []config.BranchMergeRuleConfig{
					{
						SourceBranch: "feature-conflict",
						TargetBranch: "main-conflict",
						Title:        "Merge feature-conflict into main-conflict",
						AutoMerge:    gogitlab.Ptr(true),
					},
				},
			},
		}

		// Plan identifies ActionCreate
		planRes, err := reconciler.Plan(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.Equal(t, governance.ActionCreate, planRes.Action)

		// Apply creates the MR, detects conflicts, and LEAVES OPEN without calling AcceptMergeRequest
		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// Verify on server state that MR is STILL in state "opened" (not merged!)
		mrs := srv.State().ListMergeRequests(proj.ID, "feature-conflict", "main-conflict", "opened")
		require.Len(t, mrs, 1)
		assert.Equal(t, "opened", mrs[0].State)
		assert.True(t, mrs[0].HasConflicts)

		// A second Apply invocation is idempotent: inspects existing open MR, sees conflict, leaves it open
		applyRes2, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes2.Success)

		// Still exactly 1 opened MR (no duplicate created!)
		mrsAfter := srv.State().ListMergeRequests(proj.ID, "feature-conflict", "main-conflict", "opened")
		require.Len(t, mrsAfter, 1)
	})

	t.Run("AutoMergeDisabled_LeavesCleanMROpen", func(t *testing.T) {
		srv.State().CreateBranch(proj.ID, "feature-manual", "")
		srv.State().CreateBranch(proj.ID, "main-manual", "")

		cfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				BranchMerges: []config.BranchMergeRuleConfig{
					{
						SourceBranch: "feature-manual",
						TargetBranch: "main-manual",
						AutoMerge:    gogitlab.Ptr(false),
					},
				},
			},
		}

		applyRes, err := reconciler.Apply(ctx, client, proj, cfg)
		require.NoError(t, err)
		assert.True(t, applyRes.Success)

		// MR is created and left in state "opened"
		mrs := srv.State().ListMergeRequests(proj.ID, "feature-manual", "main-manual", "opened")
		require.Len(t, mrs, 1)
		assert.Equal(t, "opened", mrs[0].State)
	})
}
