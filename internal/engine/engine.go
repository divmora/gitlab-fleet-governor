package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
)

// Common engine errors.
var (
	ErrNilClient = errors.New("gitlab client cannot be nil")
	ErrNilConfig = errors.New("policy config cannot be nil")
)

// ----------------------------------------------------------------------------
// Result Models
// ----------------------------------------------------------------------------

// OperationResult encapsulates the unified outcome of a single governance operation.
type OperationResult struct {
	OperationName string                  `json:"operation_name"`
	ResourceType  governance.ResourceType `json:"resource_type"`
	ResourceID    int                     `json:"resource_id"`
	ResourcePath  string                  `json:"resource_path"`
	Action        governance.ActionType   `json:"action"`
	Status        governance.StatusType   `json:"status"`
	HasChanges    bool                    `json:"has_changes"`
	Success       bool                    `json:"success"`
	Diffs         []governance.Diff       `json:"diffs,omitempty"`
	Details       string                  `json:"details,omitempty"`
	Error         error                   `json:"error,omitempty"`
	Duration      time.Duration           `json:"duration"`
}

// OperationResultFromPlan converts a governance.PlanResult into an OperationResult.
func OperationResultFromPlan(pr *governance.PlanResult) *OperationResult {
	if pr == nil {
		return nil
	}
	status := governance.StatusSuccess
	success := true
	if pr.Error != nil {
		status = governance.StatusFailed
		success = false
	} else if pr.Action == governance.ActionNoop {
		status = governance.StatusNoop
	} else if pr.Action == governance.ActionSkipped {
		status = governance.StatusSkipped
	}

	return &OperationResult{
		OperationName: pr.OperationName,
		ResourceType:  pr.ResourceType,
		ResourceID:    pr.ResourceID,
		ResourcePath:  pr.ResourcePath,
		Action:        pr.Action,
		Status:        status,
		HasChanges:    pr.HasChanges,
		Success:       success,
		Diffs:         pr.Diffs,
		Details:       pr.Details,
		Error:         pr.Error,
		Duration:      pr.Duration,
	}
}

// OperationResultFromApply converts a governance.ApplyResult into an OperationResult.
func OperationResultFromApply(ar *governance.ApplyResult) *OperationResult {
	if ar == nil {
		return nil
	}
	hasChanges := ar.Action != governance.ActionNoop && ar.Action != governance.ActionSkipped && len(ar.Diffs) > 0

	return &OperationResult{
		OperationName: ar.OperationName,
		ResourceType:  ar.ResourceType,
		ResourceID:    ar.ResourceID,
		ResourcePath:  ar.ResourcePath,
		Action:        ar.Action,
		Status:        ar.Status,
		HasChanges:    hasChanges,
		Success:       ar.Success,
		Diffs:         ar.Diffs,
		Details:       ar.Details,
		Error:         ar.Error,
		Duration:      ar.Duration,
	}
}

// TargetResult aggregates all operation outcomes for a single project or group.
type TargetResult struct {
	TargetID     int                     `json:"target_id"`
	TargetPath   string                  `json:"target_path"`
	TargetName   string                  `json:"target_name"`
	ResourceType governance.ResourceType `json:"resource_type"`
	DryRun       bool                    `json:"dry_run"`
	Success      bool                    `json:"success"`
	HasChanges   bool                    `json:"has_changes"`
	Operations   []*OperationResult      `json:"operations"`
	Error        error                   `json:"error,omitempty"`
	Duration     time.Duration           `json:"duration"`
}

// ChangedOperations returns all operations that contained drift or mutations.
func (tr *TargetResult) ChangedOperations() []*OperationResult {
	ops := make([]*OperationResult, 0)
	for _, op := range tr.Operations {
		if op.HasChanges {
			ops = append(ops, op)
		}
	}
	return ops
}

// FailedOperations returns all operations that failed with an error.
func (tr *TargetResult) FailedOperations() []*OperationResult {
	ops := make([]*OperationResult, 0)
	for _, op := range tr.Operations {
		if !op.Success || op.Error != nil {
			ops = append(ops, op)
		}
	}
	return ops
}

// Diffs returns all accumulated diffs across all operations on this target.
func (tr *TargetResult) Diffs() []governance.Diff {
	diffs := make([]governance.Diff, 0)
	for _, op := range tr.Operations {
		diffs = append(diffs, op.Diffs...)
	}
	return diffs
}

// ProjectChange records summarized changes on a single project.
type ProjectChange struct {
	ProjectID   int                   `json:"project_id"`
	ProjectPath string                `json:"project_path"`
	Action      governance.ActionType `json:"action"`
	Operations  []string              `json:"operations"`
}

// ExecutionResult represents the complete end-to-end outcome of an engine run.
type ExecutionResult struct {
	Mode           string                  `json:"mode"`
	DryRun         bool                    `json:"dry_run"`
	Success        bool                    `json:"success"`
	Fleet          *discovery.TargetFleet  `json:"fleet,omitempty"`
	GroupResults   []*TargetResult         `json:"group_results,omitempty"`
	ProjectResults []*TargetResult         `json:"project_results,omitempty"`
	TargetResults  []*TargetResult         `json:"target_results,omitempty"`
	Metrics        *SummaryMetricsSnapshot `json:"metrics"`
	SummaryMetrics *SummaryMetricsSnapshot `json:"summary_metrics,omitempty"`
	ProjectChanges []ProjectChange         `json:"project_changes,omitempty"`
	Errors         []error                 `json:"errors,omitempty"`
	StartedAt      time.Time               `json:"started_at"`
	CompletedAt    time.Time               `json:"completed_at"`
	Duration       time.Duration           `json:"duration"`
}

// HasChanges returns true if any target had drift or was mutated.
func (er *ExecutionResult) HasChanges() bool {
	return er.Metrics != nil && er.Metrics.TotalChanged > 0
}

// HasErrors returns true if any target or operation failed.
func (er *ExecutionResult) HasErrors() bool {
	return !er.Success || len(er.Errors) > 0 || (er.Metrics != nil && er.Metrics.TotalFailed > 0)
}

// ChangedTargets returns all targets that had changes.
func (er *ExecutionResult) ChangedTargets() []*TargetResult {
	targets := make([]*TargetResult, 0)
	for _, tr := range er.TargetResults {
		if tr.HasChanges {
			targets = append(targets, tr)
		}
	}
	return targets
}

// UnchangedTargets returns all targets that had zero drift.
func (er *ExecutionResult) UnchangedTargets() []*TargetResult {
	targets := make([]*TargetResult, 0)
	for _, tr := range er.TargetResults {
		if !tr.HasChanges && tr.Success {
			targets = append(targets, tr)
		}
	}
	return targets
}

// FailedTargets returns all targets that encountered errors.
func (er *ExecutionResult) FailedTargets() []*TargetResult {
	targets := make([]*TargetResult, 0)
	for _, tr := range er.TargetResults {
		if !tr.Success || tr.Error != nil {
			targets = append(targets, tr)
		}
	}
	return targets
}

// TotalDiffs returns the total count of diffs across all targets.
func (er *ExecutionResult) TotalDiffs() int {
	total := 0
	for _, tr := range er.TargetResults {
		total += len(tr.Diffs())
	}
	return total
}

// ----------------------------------------------------------------------------
// Governance Engine Implementation
// ----------------------------------------------------------------------------

// EngineOption defines functional options for GovernanceEngine.
type EngineOption func(*GovernanceEngine)

// WithConcurrency overrides the execution concurrency worker count.
func WithConcurrency(c int) EngineOption {
	return func(e *GovernanceEngine) {
		if c > 0 {
			e.concurrency = c
		}
	}
}

// WithDryRun overrides the dry run execution mode flag.
func WithDryRun(dryRun bool) EngineOption {
	return func(e *GovernanceEngine) {
		e.dryRun = dryRun
	}
}

// WithRegistry sets a custom OperationsRegistry instance.
func WithRegistry(r *governance.OperationsRegistry) EngineOption {
	return func(e *GovernanceEngine) {
		e.registry = r
	}
}

// GovernanceEngine orchestrates fleet discovery, worker pool dispatch,
// governance reconciler execution, metrics collection, and summary reporting.
type GovernanceEngine struct {
	client      gitlab.GitLabClient
	config      *config.PolicyConfig
	registry    *governance.OperationsRegistry
	concurrency int
	dryRun      bool
}

// NewGovernanceEngine creates a new GovernanceEngine instance.
func NewGovernanceEngine(client gitlab.GitLabClient, cfg *config.PolicyConfig, opts ...EngineOption) (*GovernanceEngine, error) {
	if client == nil {
		return nil, ErrNilClient
	}
	if cfg == nil {
		return nil, ErrNilConfig
	}

	dryRun := true
	if cfg.Settings.DryRun != nil {
		dryRun = *cfg.Settings.DryRun
	}

	concurrency := cfg.Settings.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	engine := &GovernanceEngine{
		client:      client,
		config:      cfg,
		registry:    governance.NewDefaultRegistry(client),
		concurrency: concurrency,
		dryRun:      dryRun,
	}

	for _, opt := range opts {
		opt(engine)
	}

	return engine, nil
}

// NewEngine creates a new GovernanceEngine instance without requiring an initial config.
func NewEngine(client gitlab.GitLabClient, opts ...EngineOption) *GovernanceEngine {
	engine := &GovernanceEngine{
		client:      client,
		registry:    governance.NewDefaultRegistry(client),
		concurrency: 10,
		dryRun:      true,
	}

	for _, opt := range opts {
		opt(engine)
	}

	return engine
}

// Execute executes governance against the provided config. Implements EngineExecutor.
func (e *GovernanceEngine) Execute(ctx context.Context, cfg *config.PolicyConfig) (*ExecutionResult, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}
	dryRun := e.dryRun
	if cfg.Settings.DryRun != nil {
		dryRun = *cfg.Settings.DryRun
	}
	concurrency := e.concurrency
	if cfg.Settings.Concurrency > 0 {
		concurrency = cfg.Settings.Concurrency
	}
	return e.runWithOptions(ctx, cfg, dryRun, concurrency)
}

// Run executes the governance engine according to configured dry-run mode.
func (e *GovernanceEngine) Run(ctx context.Context) (*ExecutionResult, error) {
	return e.runWithOptions(ctx, e.config, e.dryRun, e.concurrency)
}

// Plan executes the engine in dry-run simulation mode.
func (e *GovernanceEngine) Plan(ctx context.Context) (*ExecutionResult, error) {
	return e.runWithOptions(ctx, e.config, true, e.concurrency)
}

// Apply executes the engine in live mutating apply mode.
func (e *GovernanceEngine) Apply(ctx context.Context) (*ExecutionResult, error) {
	return e.runWithOptions(ctx, e.config, false, e.concurrency)
}

func (e *GovernanceEngine) runWithOptions(ctx context.Context, cfg *config.PolicyConfig, dryRun bool, concurrency int) (*ExecutionResult, error) {
	if e.client == nil {
		return nil, ErrNilClient
	}
	if cfg == nil {
		return nil, ErrNilConfig
	}
	if concurrency <= 0 {
		concurrency = 10
	}
	registry := e.registry
	if registry == nil {
		registry = governance.NewDefaultRegistry(e.client)
	}

	startTime := time.Now()
	metrics := NewSummaryMetrics(dryRun)

	mode := "apply"
	if dryRun {
		mode = "plan"
	}

	// 1. Fleet Discovery Phase
	fleet, err := discovery.DiscoverFleet(ctx, e.client, cfg.Targets, discovery.WithConcurrency(concurrency))
	if err != nil {
		metrics.Finalize(time.Since(startTime))
		snap := metrics.Snapshot()
		return &ExecutionResult{
			Mode:           mode,
			DryRun:         dryRun,
			Success:        false,
			Metrics:        snap,
			SummaryMetrics: snap,
			Errors:         []error{fmt.Errorf("fleet discovery failed: %w", err)},
			StartedAt:      startTime,
			CompletedAt:    time.Now(),
			Duration:       time.Since(startTime),
		}, err
	}

	metrics.RecordDiscovery(fleet)

	if fleet.IsEmpty() {
		metrics.Finalize(time.Since(startTime))
		snap := metrics.Snapshot()
		return &ExecutionResult{
			Mode:           mode,
			DryRun:         dryRun,
			Success:        true,
			Fleet:          fleet,
			GroupResults:   make([]*TargetResult, 0),
			ProjectResults: make([]*TargetResult, 0),
			TargetResults:  make([]*TargetResult, 0),
			Metrics:        snap,
			SummaryMetrics: snap,
			StartedAt:      startTime,
			CompletedAt:    time.Now(),
			Duration:       time.Since(startTime),
		}, nil
	}

	var allErrors []error
	var errMu sync.Mutex
	recordErr := func(err error) {
		if err != nil {
			errMu.Lock()
			allErrors = append(allErrors, err)
			errMu.Unlock()
		}
	}

	// 2. Governance Execution Phase for Groups
	groupResults := make([]*TargetResult, 0, len(fleet.Groups))
	if len(fleet.Groups) > 0 {
		groups := fleet.GroupList()
		res, errs := ParallelMap(ctx, concurrency, groups, func(taskCtx context.Context, g *discovery.TargetGroup) (*TargetResult, error) {
			return e.executeGroup(taskCtx, g, registry, cfg, dryRun)
		})
		for _, err := range errs {
			recordErr(err)
		}
		for _, r := range res {
			if r != nil {
				groupResults = append(groupResults, r)
				metrics.RecordTargetResult(r)
			}
		}
	}

	// 3. Governance Execution Phase for Projects
	projectResults := make([]*TargetResult, 0, len(fleet.Projects))
	projectChanges := make([]ProjectChange, 0)
	if len(fleet.Projects) > 0 {
		projects := fleet.ProjectList()
		res, errs := ParallelMap(ctx, concurrency, projects, func(taskCtx context.Context, p *discovery.TargetProject) (*TargetResult, error) {
			return e.executeProject(taskCtx, p, registry, cfg, dryRun)
		})
		for _, err := range errs {
			recordErr(err)
		}
		for _, r := range res {
			if r != nil {
				projectResults = append(projectResults, r)
				metrics.RecordTargetResult(r)

				if r.HasChanges {
					opNames := make([]string, 0)
					action := governance.ActionUpdate
					for _, op := range r.Operations {
						if op.HasChanges {
							opNames = append(opNames, op.OperationName)
							action = op.Action
						}
					}
					projectChanges = append(projectChanges, ProjectChange{
						ProjectID:   r.TargetID,
						ProjectPath: r.TargetPath,
						Action:      action,
						Operations:  opNames,
					})
				}
			}
		}
	}

	// 4. Combine and Sort Target Results deterministically
	allTargetResults := make([]*TargetResult, 0, len(groupResults)+len(projectResults))
	allTargetResults = append(allTargetResults, groupResults...)
	allTargetResults = append(allTargetResults, projectResults...)

	sort.Slice(allTargetResults, func(i, j int) bool {
		if allTargetResults[i].TargetPath == allTargetResults[j].TargetPath {
			return allTargetResults[i].TargetID < allTargetResults[j].TargetID
		}
		return allTargetResults[i].TargetPath < allTargetResults[j].TargetPath
	})

	completedAt := time.Now()
	duration := completedAt.Sub(startTime)
	metrics.Finalize(duration)
	snap := metrics.Snapshot()

	overallSuccess := len(allErrors) == 0 && snap.TotalFailed == 0

	return &ExecutionResult{
		Mode:           mode,
		DryRun:         dryRun,
		Success:        overallSuccess,
		Fleet:          fleet,
		GroupResults:   groupResults,
		ProjectResults: projectResults,
		TargetResults:  allTargetResults,
		Metrics:        snap,
		SummaryMetrics: snap,
		ProjectChanges: projectChanges,
		Errors:         allErrors,
		StartedAt:      startTime,
		CompletedAt:    completedAt,
		Duration:       duration,
	}, nil
}

// executeProject executes governance reconciliation for a single project target.
func (e *GovernanceEngine) executeProject(ctx context.Context, target *discovery.TargetProject, registry *governance.OperationsRegistry, cfg *config.PolicyConfig, dryRun bool) (*TargetResult, error) {
	start := time.Now()
	res := &TargetResult{
		TargetID:     target.ID,
		TargetPath:   target.PathWithNamespace,
		TargetName:   target.Name,
		ResourceType: governance.ResourceTypeProject,
		DryRun:       dryRun,
		Success:      true,
		Operations:   make([]*OperationResult, 0),
	}

	if dryRun {
		planResults, err := registry.PlanTargetProject(ctx, target, cfg)
		res.Duration = time.Since(start)
		if err != nil {
			res.Success = false
			res.Error = err
		}
		for i := range planResults {
			opRes := OperationResultFromPlan(&planResults[i])
			res.Operations = append(res.Operations, opRes)
			if opRes.HasChanges {
				res.HasChanges = true
			}
			if !opRes.Success {
				res.Success = false
			}
		}
		return res, err
	}

	applyResults, err := registry.ApplyTargetProject(ctx, target, cfg)
	res.Duration = time.Since(start)
	if err != nil {
		res.Success = false
		res.Error = err
	}
	for i := range applyResults {
		opRes := OperationResultFromApply(&applyResults[i])
		res.Operations = append(res.Operations, opRes)
		if opRes.HasChanges {
			res.HasChanges = true
		}
		if !opRes.Success {
			res.Success = false
		}
	}
	return res, err
}

// executeGroup executes governance reconciliation for a single group target.
func (e *GovernanceEngine) executeGroup(ctx context.Context, target *discovery.TargetGroup, registry *governance.OperationsRegistry, cfg *config.PolicyConfig, dryRun bool) (*TargetResult, error) {
	start := time.Now()
	res := &TargetResult{
		TargetID:     target.ID,
		TargetPath:   target.FullPath,
		TargetName:   target.Name,
		ResourceType: governance.ResourceTypeGroup,
		DryRun:       dryRun,
		Success:      true,
		Operations:   make([]*OperationResult, 0),
	}

	if dryRun {
		planResults, err := registry.PlanTargetGroup(ctx, target, cfg)
		res.Duration = time.Since(start)
		if err != nil {
			res.Success = false
			res.Error = err
		}
		for i := range planResults {
			opRes := OperationResultFromPlan(&planResults[i])
			res.Operations = append(res.Operations, opRes)
			if opRes.HasChanges {
				res.HasChanges = true
			}
			if !opRes.Success {
				res.Success = false
			}
		}
		return res, err
	}

	applyResults, err := registry.ApplyTargetGroup(ctx, target, cfg)
	res.Duration = time.Since(start)
	if err != nil {
		res.Success = false
		res.Error = err
	}
	for i := range applyResults {
		opRes := OperationResultFromApply(&applyResults[i])
		res.Operations = append(res.Operations, opRes)
		if opRes.HasChanges {
			res.HasChanges = true
		}
		if !opRes.Success {
			res.Success = false
		}
	}
	return res, err
}

// Concurrency returns the configured execution concurrency.
func (e *GovernanceEngine) Concurrency() int {
	return e.concurrency
}

// IsDryRun returns whether dry run simulation mode is active.
func (e *GovernanceEngine) IsDryRun() bool {
	return e.dryRun
}
