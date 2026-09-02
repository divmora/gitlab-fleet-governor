package engine_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
)

func TestSummaryMetrics_BasicAccumulation(t *testing.T) {
	metrics := engine.NewSummaryMetrics(true)

	fleet := &discovery.TargetFleet{
		ScannedProjectsCount: 10,
		ScannedGroupsCount:   2,
		MatchedProjectsCount: 5,
		MatchedGroupsCount:   1,
	}
	metrics.RecordDiscovery(fleet)

	// Target 1: Changed with push_rules diff
	tr1 := TargetResultFixture(1, "group1/proj1", true, true, nil,
		&engine.OperationResult{
			OperationName: "push_rules",
			ResourceType:  governance.ResourceTypeProject,
			ResourceID:    1,
			ResourcePath:  "group1/proj1",
			Action:        governance.ActionCreate,
			Status:        governance.StatusSuccess,
			HasChanges:    true,
			Success:       true,
			Diffs: []governance.Diff{
				{
					Resource: "push_rule",
					Action:   governance.ActionCreate,
					Fields: []governance.FieldDiff{
						{Field: "prevent_secrets", OldValue: nil, NewValue: true, Action: governance.ActionCreate},
					},
				},
			},
		},
	)
	metrics.RecordTargetResult(tr1)

	// Target 2: Unchanged
	tr2 := TargetResultFixture(2, "group1/proj2", false, true, nil,
		&engine.OperationResult{
			OperationName: "push_rules",
			Action:        governance.ActionNoop,
			Status:        governance.StatusNoop,
			HasChanges:    false,
			Success:       true,
		},
	)
	metrics.RecordTargetResult(tr2)

	// Target 3: Failed
	tr3 := TargetResultFixture(3, "group1/proj3", false, false, errors.New("api rate limit exceeded"))
	metrics.RecordTargetResult(tr3)

	metrics.Finalize(50 * time.Millisecond)
	snap := metrics.Snapshot()

	if snap.TotalScanned != 12 {
		t.Errorf("expected TotalScanned=12, got %d", snap.TotalScanned)
	}
	if snap.TotalTargeted != 6 {
		t.Errorf("expected TotalTargeted=6, got %d", snap.TotalTargeted)
	}
	if snap.TotalChanged != 1 {
		t.Errorf("expected TotalChanged=1, got %d", snap.TotalChanged)
	}
	if snap.TotalUnchanged != 1 {
		t.Errorf("expected TotalUnchanged=1, got %d", snap.TotalUnchanged)
	}
	if snap.TotalFailed != 1 {
		t.Errorf("expected TotalFailed=1, got %d", snap.TotalFailed)
	}
	if snap.DriftSummary.TotalDriftFields != 1 {
		t.Errorf("expected TotalDriftFields=1, got %d", snap.DriftSummary.TotalDriftFields)
	}
	if len(snap.DriftSummary.ChangedResources) != 1 || snap.DriftSummary.ChangedResources[0] != "group1/proj1" {
		t.Errorf("unexpected ChangedResources: %v", snap.DriftSummary.ChangedResources)
	}
	if len(snap.DriftSummary.FailedResources) != 1 || snap.DriftSummary.FailedResources[0] != "group1/proj3" {
		t.Errorf("unexpected FailedResources: %v", snap.DriftSummary.FailedResources)
	}
	if metrics.String() == "" {
		t.Errorf("expected non-empty string representation")
	}
}

func TestSummaryMetrics_ConcurrentUpdates(t *testing.T) {
	metrics := engine.NewSummaryMetrics(false)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tr := TargetResultFixture(id, "proj", id%2 == 0, true, nil,
				&engine.OperationResult{
					OperationName: "push_rules",
					Action:        governance.ActionUpdate,
					HasChanges:    id%2 == 0,
					Success:       true,
				},
			)
			metrics.RecordTargetResult(tr)
		}(i)
	}

	wg.Wait()
	snap := metrics.Snapshot()

	if snap.TotalChanged != 50 {
		t.Errorf("expected TotalChanged=50, got %d", snap.TotalChanged)
	}
	if snap.TotalUnchanged != 50 {
		t.Errorf("expected TotalUnchanged=50, got %d", snap.TotalUnchanged)
	}
}

func TestSummaryMetrics_NilSafety(t *testing.T) {
	metrics := engine.NewSummaryMetrics(true)
	metrics.RecordDiscovery(nil)
	metrics.RecordTargetResult(nil)
	metrics.RecordOperationResult(nil)
	snap := metrics.Snapshot()
	if snap == nil {
		t.Fatalf("expected non-nil snapshot")
	}
}

func TestSummaryMetrics_FailedTargetOperationCount(t *testing.T) {
	metrics := engine.NewSummaryMetrics(true)
	tr := TargetResultFixture(1, "group1/proj1", false, false, errors.New("target error"),
		&engine.OperationResult{
			OperationName: "push_rules",
			Action:        governance.ActionUpdate,
			Status:        governance.StatusFailed,
			Success:       false,
			Error:         errors.New("operation 1 failed"),
		},
		&engine.OperationResult{
			OperationName: "protected_branches",
			Action:        governance.ActionUpdate,
			Status:        governance.StatusFailed,
			Success:       false,
			Error:         errors.New("operation 2 failed"),
		},
	)
	metrics.RecordTargetResult(tr)
	snap := metrics.Snapshot()

	if snap.TotalFailed != 1 {
		t.Errorf("expected TotalFailed=1 (target-level failure), got %d", snap.TotalFailed)
	}
	if opCount, ok := snap.OperationCounts["push_rules"]; !ok || opCount.Failed != 1 {
		t.Errorf("expected push_rules failed=1, got %v", opCount)
	}
	if opCount, ok := snap.OperationCounts["protected_branches"]; !ok || opCount.Failed != 1 {
		t.Errorf("expected protected_branches failed=1, got %v", opCount)
	}
}

func TargetResultFixture(id int, path string, changed, success bool, err error, ops ...*engine.OperationResult) *engine.TargetResult {

	return &engine.TargetResult{
		TargetID:     id,
		TargetPath:   path,
		TargetName:   path,
		ResourceType: governance.ResourceTypeProject,
		DryRun:       true,
		Success:      success,
		HasChanges:   changed,
		Error:        err,
		Operations:   ops,
	}
}
