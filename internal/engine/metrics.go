package engine

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
)

// SummaryMetrics is a thread-safe accumulator tracking governance execution metrics,
// drift breakdown, operation outcomes, and fleet totals.
type SummaryMetrics struct {
	mu                sync.RWMutex
	DryRun            bool
	StartTime         time.Time
	EndTime           time.Time
	ExecutionDuration time.Duration

	// Fleet discovery counts
	ScannedProjects  int
	ScannedGroups    int
	TotalScanned     int
	TargetedProjects int
	TargetedGroups   int
	TotalTargeted    int

	// Execution outcome totals
	TotalChanged   int
	TotalUnchanged int
	TotalFailed    int
	TotalSkipped   int

	// Operation status totals
	TotalApplied int
	TotalNoop    int

	// Per-operation metrics breakdown
	OperationCounts map[string]*OperationSummary

	// Drift details breakdown
	DriftSummary DriftSummary
}

// OperationSummary aggregates metrics for a specific governance reconciler.
type OperationSummary struct {
	OperationName string        `json:"operation_name"`
	Total         int           `json:"total"`
	Created       int           `json:"created"`
	Updated       int           `json:"updated"`
	Deleted       int           `json:"deleted"`
	Noop          int           `json:"noop"`
	Audit         int           `json:"audit"`
	Skipped       int           `json:"skipped"`
	Failed        int           `json:"failed"`
	Duration      time.Duration `json:"duration"`
}

// DriftSummary encapsulates structured drift counts across actions, operations, and resources.
type DriftSummary struct {
	TotalDriftFields int                           `json:"total_drift_fields"`
	DriftByAction    map[governance.ActionType]int `json:"drift_by_action"`
	DriftByOperation map[string]int                `json:"drift_by_operation"`
	DriftByResource  map[string]int                `json:"drift_by_resource"`
	ChangedResources []string                      `json:"changed_resources"`
	FailedResources  []string                      `json:"failed_resources"`
}

// SummaryMetricsSnapshot is an immutable, JSON-serializable snapshot of SummaryMetrics.
type SummaryMetricsSnapshot struct {
	DryRun                 bool                         `json:"dry_run"`
	StartTime              time.Time                    `json:"start_time"`
	EndTime                time.Time                    `json:"end_time"`
	ExecutionDuration      time.Duration                `json:"execution_duration"`
	DurationMs             int64                        `json:"duration_ms"`
	ScannedProjects        int                          `json:"scanned_projects"`
	ScannedGroups          int                          `json:"scanned_groups"`
	TotalScanned           int                          `json:"total_scanned"`
	TargetedProjects       int                          `json:"targeted_projects"`
	TargetedGroups         int                          `json:"targeted_groups"`
	TotalTargeted          int                          `json:"total_targeted"`
	TotalChanged           int                          `json:"total_changed"`
	TotalUnchanged         int                          `json:"total_unchanged"`
	TotalFailed            int                          `json:"total_failed"`
	TotalSkipped           int                          `json:"total_skipped"`
	TotalScannedGroups     int                          `json:"total_scanned_groups"`
	TotalMatchedGroups     int                          `json:"total_matched_groups"`
	TotalScannedProjects   int                          `json:"total_scanned_projects"`
	TotalMatchedProjects   int                          `json:"total_matched_projects"`
	TotalAppliedOperations int                          `json:"total_applied_operations"`
	TotalSkippedOperations int                          `json:"total_skipped_operations"`
	TotalFailedOperations  int                          `json:"total_failed_operations"`
	OperationCounts        map[string]*OperationSummary `json:"operation_counts"`
	DriftSummary           DriftSummary                 `json:"drift_summary"`
}

// NewSummaryMetrics creates an initialized SummaryMetrics accumulator.
func NewSummaryMetrics(dryRun bool) *SummaryMetrics {
	return &SummaryMetrics{
		DryRun:          dryRun,
		StartTime:       time.Now(),
		OperationCounts: make(map[string]*OperationSummary),
		DriftSummary: DriftSummary{
			DriftByAction:    make(map[governance.ActionType]int),
			DriftByOperation: make(map[string]int),
			DriftByResource:  make(map[string]int),
			ChangedResources: make([]string, 0),
			FailedResources:  make([]string, 0),
		},
	}
}

// RecordDiscovery records fleet discovery counts into the metrics accumulator.
func (m *SummaryMetrics) RecordDiscovery(fleet *discovery.TargetFleet) {
	if fleet == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ScannedProjects = fleet.ScannedProjectsCount
	m.ScannedGroups = fleet.ScannedGroupsCount
	m.TotalScanned = fleet.ScannedProjectsCount + fleet.ScannedGroupsCount
	m.TargetedProjects = fleet.MatchedProjectsCount
	m.TargetedGroups = fleet.MatchedGroupsCount
	m.TotalTargeted = fleet.MatchedProjectsCount + fleet.MatchedGroupsCount
}

// RecordTargetResult processes a completed TargetResult and updates aggregate counters.
func (m *SummaryMetrics) RecordTargetResult(tr *TargetResult) {
	if tr == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if !tr.Success || tr.Error != nil {
		m.TotalFailed++
		m.DriftSummary.FailedResources = append(m.DriftSummary.FailedResources, tr.TargetPath)
	} else if tr.HasChanges {
		m.TotalChanged++
		m.DriftSummary.ChangedResources = append(m.DriftSummary.ChangedResources, tr.TargetPath)
	} else {
		m.TotalUnchanged++
	}

	for _, op := range tr.Operations {
		m.recordOperationLocked(op)
	}
}

// RecordOperationResult records an individual OperationResult into the metrics accumulator.
func (m *SummaryMetrics) RecordOperationResult(op *OperationResult) {
	if op == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordOperationLocked(op)
}

// recordOperationLocked updates per-operation counters and drift details (caller holds lock).
func (m *SummaryMetrics) recordOperationLocked(op *OperationResult) {
	summary, exists := m.OperationCounts[op.OperationName]
	if !exists {
		summary = &OperationSummary{
			OperationName: op.OperationName,
		}
		m.OperationCounts[op.OperationName] = summary
	}

	summary.Total++
	summary.Duration += op.Duration

	if !op.Success || op.Error != nil || op.Status == governance.StatusFailed {
		summary.Failed++
	}

	switch op.Action {
	case governance.ActionCreate:
		summary.Created++
		m.TotalApplied++
		m.DriftSummary.DriftByAction[governance.ActionCreate]++
	case governance.ActionUpdate:
		summary.Updated++
		m.TotalApplied++
		m.DriftSummary.DriftByAction[governance.ActionUpdate]++
	case governance.ActionDelete:
		summary.Deleted++
		m.TotalApplied++
		m.DriftSummary.DriftByAction[governance.ActionDelete]++
	case governance.ActionAudit:
		summary.Audit++
		m.DriftSummary.DriftByAction[governance.ActionAudit]++
	case governance.ActionSkipped:
		summary.Skipped++
		m.TotalSkipped++
	case governance.ActionNoop:
		summary.Noop++
		m.TotalNoop++
	}

	if op.HasChanges {
		m.DriftSummary.DriftByOperation[op.OperationName]++
		m.DriftSummary.DriftByResource[op.ResourcePath]++
		for _, diff := range op.Diffs {
			m.DriftSummary.TotalDriftFields += len(diff.Fields)
			if len(diff.Fields) == 0 && diff.HasChanges() {
				m.DriftSummary.TotalDriftFields++
			}
		}
	}
}

// Finalize records completion timestamp and execution duration.
func (m *SummaryMetrics) Finalize(duration ...time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.EndTime = time.Now()
	if len(duration) > 0 && duration[0] > 0 {
		m.ExecutionDuration = duration[0]
	} else if !m.StartTime.IsZero() {
		m.ExecutionDuration = m.EndTime.Sub(m.StartTime)
	}
}

// Snapshot returns an immutable, deep-copied SummaryMetricsSnapshot.
func (m *SummaryMetrics) Snapshot() *SummaryMetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	opCountsCopy := make(map[string]*OperationSummary, len(m.OperationCounts))
	appliedOps := 0
	skippedOps := 0
	failedOps := 0

	for k, v := range m.OperationCounts {
		opCountsCopy[k] = &OperationSummary{
			OperationName: v.OperationName,
			Total:         v.Total,
			Created:       v.Created,
			Updated:       v.Updated,
			Deleted:       v.Deleted,
			Noop:          v.Noop,
			Audit:         v.Audit,
			Skipped:       v.Skipped,
			Failed:        v.Failed,
			Duration:      v.Duration,
		}
		appliedOps += v.Created + v.Updated + v.Deleted
		skippedOps += v.Skipped
		failedOps += v.Failed
	}

	driftActionCopy := make(map[governance.ActionType]int, len(m.DriftSummary.DriftByAction))
	for k, v := range m.DriftSummary.DriftByAction {
		driftActionCopy[k] = v
	}

	driftOpCopy := make(map[string]int, len(m.DriftSummary.DriftByOperation))
	for k, v := range m.DriftSummary.DriftByOperation {
		driftOpCopy[k] = v
	}

	driftResCopy := make(map[string]int, len(m.DriftSummary.DriftByResource))
	for k, v := range m.DriftSummary.DriftByResource {
		driftResCopy[k] = v
	}

	changedResCopy := make([]string, len(m.DriftSummary.ChangedResources))
	copy(changedResCopy, m.DriftSummary.ChangedResources)
	sort.Strings(changedResCopy)

	failedResCopy := make([]string, len(m.DriftSummary.FailedResources))
	copy(failedResCopy, m.DriftSummary.FailedResources)
	sort.Strings(failedResCopy)

	dur := m.ExecutionDuration
	if dur == 0 && !m.StartTime.IsZero() {
		dur = time.Since(m.StartTime)
	}

	return &SummaryMetricsSnapshot{
		DryRun:                 m.DryRun,
		StartTime:              m.StartTime,
		EndTime:                m.EndTime,
		ExecutionDuration:      dur,
		DurationMs:             dur.Milliseconds(),
		ScannedProjects:        m.ScannedProjects,
		ScannedGroups:          m.ScannedGroups,
		TotalScanned:           m.TotalScanned,
		TargetedProjects:       m.TargetedProjects,
		TargetedGroups:         m.TargetedGroups,
		TotalTargeted:          m.TotalTargeted,
		TotalChanged:           m.TotalChanged,
		TotalUnchanged:         m.TotalUnchanged,
		TotalFailed:            m.TotalFailed,
		TotalSkipped:           m.TotalSkipped,
		TotalScannedGroups:     m.ScannedGroups,
		TotalMatchedGroups:     m.TargetedGroups,
		TotalScannedProjects:   m.ScannedProjects,
		TotalMatchedProjects:   m.TargetedProjects,
		TotalAppliedOperations: appliedOps,
		TotalSkippedOperations: skippedOps,
		TotalFailedOperations:  failedOps,
		OperationCounts:        opCountsCopy,
		DriftSummary: DriftSummary{
			TotalDriftFields: m.DriftSummary.TotalDriftFields,
			DriftByAction:    driftActionCopy,
			DriftByOperation: driftOpCopy,
			DriftByResource:  driftResCopy,
			ChangedResources: changedResCopy,
			FailedResources:  failedResCopy,
		},
	}
}

// String renders a concise one-line summary string.
func (m *SummaryMetrics) String() string {
	snap := m.Snapshot()
	mode := "APPLY"
	if snap.DryRun {
		mode = "DRY-RUN"
	}
	return fmt.Sprintf("[%s] Scanned: %d | Targeted: %d | Changed: %d | Unchanged: %d | Failed: %d | Duration: %s",
		mode, snap.TotalScanned, snap.TotalTargeted, snap.TotalChanged, snap.TotalUnchanged, snap.TotalFailed, snap.ExecutionDuration.Round(time.Millisecond))
}
