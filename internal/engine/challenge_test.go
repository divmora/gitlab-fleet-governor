package engine_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// TestChallenge_WorkerPool_HighLoadConcurrency tests bounded worker pool under high task throughput.
func TestChallenge_WorkerPool_HighLoadConcurrency(t *testing.T) {
	const numTasks = 1000
	const concurrency = 20

	pool := engine.NewWorkerPool(context.Background(), concurrency)
	var processedCount sync.WaitGroup
	processedCount.Add(numTasks)

	for i := 0; i < numTasks; i++ {
		err := pool.Submit(func(ctx context.Context) error {
			time.Sleep(100 * time.Microsecond)
			processedCount.Done()
			return nil
		})
		require.NoError(t, err)
	}

	processedCount.Wait()
	errs := pool.Wait()
	assert.Empty(t, errs)
}

// TestChallenge_WorkerPool_PanicRecoveryInWorker tests task panic isolation.
func TestChallenge_WorkerPool_PanicRecoveryInWorker(t *testing.T) {
	pool := engine.NewWorkerPool(context.Background(), 5)

	// Submit 3 normal tasks and 2 panicking tasks
	for i := 0; i < 5; i++ {
		taskID := i
		err := pool.Submit(func(ctx context.Context) error {
			if taskID%2 == 1 {
				panic(fmt.Sprintf("deliberate panic in worker task %d", taskID))
			}
			return nil
		})
		require.NoError(t, err)
	}

	errs := pool.Wait()
	assert.Equal(t, 2, len(errs), "Pool should capture exactly 2 panic errors")
	for _, err := range errs {
		assert.Contains(t, err.Error(), "worker panic recovered")
	}
}

// TestChallenge_ParallelMap_FiftyWorkerSaturationCascadingPanics tests 50 workers under cascading panics.
func TestChallenge_ParallelMap_FiftyWorkerSaturationCascadingPanics(t *testing.T) {
	const numItems = 200
	const concurrency = 50
	items := make([]int, numItems)
	for i := 0; i < numItems; i++ {
		items[i] = i
	}

	results, errs := engine.ParallelMap(context.Background(), concurrency, items, func(ctx context.Context, item int) (int, error) {
		// Even items divisible by 4 (50 items total) trigger a runtime panic
		if item%4 == 0 {
			panic(fmt.Sprintf("cascading worker panic at item index %d", item))
		}
		// Odd items divisible by 5 return standard errors
		if item%5 == 0 {
			return 0, fmt.Errorf("task error at item %d", item)
		}
		return item * 2, nil
	})

	require.NotEmpty(t, errs, "ParallelMap must return non-nil and non-empty error slice when tasks panic")
	assert.Equal(t, numItems, len(results))

	panicCount := 0
	standardErrCount := 0
	for _, err := range errs {
		if err == nil {
			continue
		}
		errStr := err.Error()
		if strings.Contains(errStr, "worker panic recovered:") {
			panicCount++
			assert.Contains(t, errStr, "stack:", "Panic error must contain stack trace")
		} else if strings.Contains(errStr, "task error at item") {
			standardErrCount++
		}
	}

	assert.Greater(t, panicCount, 0, "Must capture panic errors")
	assert.Greater(t, standardErrCount, 0, "Must capture standard errors")

	// Verify non-error/non-panicking tasks computed valid outputs
	for i := 0; i < numItems; i++ {
		if i%4 != 0 && i%5 != 0 {
			assert.Equal(t, i*2, results[i], "Non-panicking and non-failing item %d must produce valid result", i)
		}
	}
}

// TestChallenge_GovernanceEngine_ReentrantConcurrencyMultiGoroutine verifies race-free reentrancy.
func TestChallenge_GovernanceEngine_ReentrantConcurrencyMultiGoroutine(t *testing.T) {
	server := mockserver.NewMockGitLabServer()
	defer server.Close()
	server.Seed()

	client, err := server.GovernorClient()
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			Concurrency: 4,
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{10},
				Recursive:       gogitlab.Ptr(false),
			},
		},
	}

	eng, err := engine.NewGovernanceEngine(client, cfg)
	require.NoError(t, err)

	const numGoroutines = 24
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		mode := i % 4
		go func(m int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			switch m {
			case 0:
				res, err := eng.Plan(ctx)
				assert.NoError(t, err)
				assert.True(t, res.DryRun)
				assert.Equal(t, "plan", res.Mode)
			case 1:
				res, err := eng.Apply(ctx)
				assert.NoError(t, err)
				assert.False(t, res.DryRun)
				assert.Equal(t, "apply", res.Mode)
			case 2:
				res, err := eng.Run(ctx)
				assert.NoError(t, err)
				assert.NotNil(t, res)
			case 3:
				res, err := eng.Execute(ctx, cfg)
				assert.NoError(t, err)
				assert.NotNil(t, res)
			}
		}(mode)
	}

	wg.Wait()
}

// TestChallenge_SummaryMetrics_InvariantUnderMixedFailures tests TotalTargeted == TotalChanged + TotalUnchanged + TotalFailed.
func TestChallenge_SummaryMetrics_InvariantUnderMixedFailures(t *testing.T) {
	metrics := engine.NewSummaryMetrics(false)

	const numTargets = 100
	metrics.RecordDiscovery(&discovery.TargetFleet{
		ScannedProjectsCount: numTargets,
		MatchedProjectsCount: numTargets,
	})

	expectedChanged := 0
	expectedUnchanged := 0
	expectedFailed := 0
	expectedTotalFailedOps := 0

	for i := 0; i < numTargets; i++ {
		targetID := 1000 + i
		targetPath := fmt.Sprintf("fleet/proj-%d", targetID)

		switch {
		case i < 30:
			// 30 Changed targets (successful with drift)
			expectedChanged++
			metrics.RecordTargetResult(&engine.TargetResult{
				TargetID:     targetID,
				TargetPath:   targetPath,
				ResourceType: governance.ResourceTypeProject,
				Success:      true,
				HasChanges:   true,
				Operations: []*engine.OperationResult{
					{
						OperationName: "push_rules",
						Action:        governance.ActionUpdate,
						Status:        governance.StatusSuccess,
						HasChanges:    true,
						Success:       true,
						Diffs: []governance.Diff{
							{Resource: "push_rule", Action: governance.ActionUpdate, Fields: []governance.FieldDiff{{Field: "deny_delete_tag", OldValue: false, NewValue: true}}},
						},
					},
				},
			})

		case i < 70:
			// 40 Unchanged targets (successful without drift)
			expectedUnchanged++
			metrics.RecordTargetResult(&engine.TargetResult{
				TargetID:     targetID,
				TargetPath:   targetPath,
				ResourceType: governance.ResourceTypeProject,
				Success:      true,
				HasChanges:   false,
				Operations: []*engine.OperationResult{
					{
						OperationName: "push_rules",
						Action:        governance.ActionNoop,
						Status:        governance.StatusNoop,
						HasChanges:    false,
						Success:       true,
					},
				},
			})

		default:
			// 30 Failed targets with multiple failing operations per target
			expectedFailed++
			numFailedOps := (i % 3) + 1 // 1, 2, or 3 failed operations
			expectedTotalFailedOps += numFailedOps

			ops := make([]*engine.OperationResult, 0, numFailedOps)
			for opIdx := 0; opIdx < numFailedOps; opIdx++ {
				opName := fmt.Sprintf("operation_%d", opIdx)
				ops = append(ops, &engine.OperationResult{
					OperationName: opName,
					Action:        governance.ActionUpdate,
					Status:        governance.StatusFailed,
					Success:       false,
					Error:         fmt.Errorf("error in %s on target %d", opName, targetID),
				})
			}

			metrics.RecordTargetResult(&engine.TargetResult{
				TargetID:     targetID,
				TargetPath:   targetPath,
				ResourceType: governance.ResourceTypeProject,
				Success:      false,
				HasChanges:   false,
				Error:        fmt.Errorf("target execution failure on %d", targetID),
				Operations:   ops,
			})
		}
	}

	snap := metrics.Snapshot()

	// Invariant verification
	assert.Equal(t, numTargets, snap.TotalTargeted, "TotalTargeted must match scanned count")
	assert.Equal(t, expectedChanged, snap.TotalChanged, "TotalChanged must match")
	assert.Equal(t, expectedUnchanged, snap.TotalUnchanged, "TotalUnchanged must match")
	assert.Equal(t, expectedFailed, snap.TotalFailed, "TotalFailed must match failed targets count exactly")
	assert.Equal(t, expectedTotalFailedOps, snap.TotalFailedOperations, "TotalFailedOperations must match sum of failed ops")

	// The fundamental invariant
	assert.Equal(t, snap.TotalTargeted, snap.TotalChanged+snap.TotalUnchanged+snap.TotalFailed,
		"Invariant Violation: TotalTargeted (%d) != TotalChanged (%d) + TotalUnchanged (%d) + TotalFailed (%d)",
		snap.TotalTargeted, snap.TotalChanged, snap.TotalUnchanged, snap.TotalFailed)
}
