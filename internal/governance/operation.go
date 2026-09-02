package governance

import (
	"context"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// ActionType defines the category of mutation planned or applied by a reconciler.
type ActionType string

const (
	// ActionCreate indicates a new resource will be or was created.
	ActionCreate ActionType = "CREATE"
	// ActionUpdate indicates an existing resource will be or was modified.
	ActionUpdate ActionType = "UPDATE"
	// ActionDelete indicates an existing resource will be or was removed/unprotected.
	ActionDelete ActionType = "DELETE"
	// ActionNoop indicates the live state already matches the desired policy.
	ActionNoop ActionType = "NOOP"
	// ActionAudit indicates a policy audit finding was reported.
	ActionAudit ActionType = "AUDIT"
	// ActionSkipped indicates the operation was not applicable or was skipped.
	ActionSkipped ActionType = "SKIPPED"
)

// ResourceType specifies the scope of the target entity (project or group).
type ResourceType string

const (
	ResourceTypeProject ResourceType = "project"
	ResourceTypeGroup   ResourceType = "group"
)

// StatusType defines the execution outcome status of an applied operation.
type StatusType string

const (
	StatusSuccess StatusType = "SUCCESS"
	StatusFailed  StatusType = "FAILED"
	StatusSkipped StatusType = "SKIPPED"
	StatusNoop    StatusType = "NOOP"
)

// GovernanceOperation is the core interface for all fleet policy reconcilers.
// Each operation encapsulates domain-specific inspection, diff calculation,
// dry-run simulation (Plan), and idempotent enforcement (Apply) for both
// projects and groups.
type GovernanceOperation interface {
	// Name returns the canonical unique identifier for the operation.
	Name() string

	// Order returns the integer execution order for operation sequencing.
	Order() int

	// Plan evaluates the project against the desired policy without mutating state.
	Plan(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*PlanResult, error)

	// Apply enforces the desired policy on the target project, performing mutating API calls.
	Apply(ctx context.Context, client gitlab.GitLabClient, project *gogitlab.Project, cfg *config.PolicyConfig) (*ApplyResult, error)

	// PlanGroup evaluates the group against the desired policy without mutating state.
	PlanGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*PlanResult, error)

	// ApplyGroup enforces the desired policy on the target group, performing mutating API calls.
	ApplyGroup(ctx context.Context, client gitlab.GitLabClient, group *gogitlab.Group, cfg *config.PolicyConfig) (*ApplyResult, error)
}

// PlanResult encapsulates the dry-run simulation outcome of a governance operation.
type PlanResult struct {
	OperationName string        `json:"operation_name"`
	ResourceType  ResourceType  `json:"resource_type"`
	ResourceID    int           `json:"resource_id"`
	ResourcePath  string        `json:"resource_path"`
	Action        ActionType    `json:"action"`
	Diffs         []Diff        `json:"diffs,omitempty"`
	HasChanges    bool          `json:"has_changes"`
	Details       string        `json:"details,omitempty"`
	Error         error         `json:"error,omitempty"`
	Duration      time.Duration `json:"duration,omitempty"`
}

// ApplyResult encapsulates the live mutation outcome of an applied governance operation.
type ApplyResult struct {
	OperationName string        `json:"operation_name"`
	ResourceType  ResourceType  `json:"resource_type"`
	ResourceID    int           `json:"resource_id"`
	ResourcePath  string        `json:"resource_path"`
	Action        ActionType    `json:"action"`
	Diffs         []Diff        `json:"diffs,omitempty"`
	Status        StatusType    `json:"status"`
	Success       bool          `json:"success"`
	Details       string        `json:"details,omitempty"`
	Error         error         `json:"error,omitempty"`
	ExecutedAt    time.Time     `json:"executed_at"`
	Duration      time.Duration `json:"duration"`
}

// NewPlanResult creates a populated PlanResult with changes.
func NewPlanResult(op string, rType ResourceType, id int, path string, action ActionType, diffs []Diff) *PlanResult {
	hasChanges := action != ActionNoop && action != ActionSkipped && len(diffs) > 0
	return &PlanResult{
		OperationName: op,
		ResourceType:  rType,
		ResourceID:    id,
		ResourcePath:  path,
		Action:        action,
		Diffs:         diffs,
		HasChanges:    hasChanges,
	}
}

// NewNoopPlanResult creates a PlanResult representing no drift.
func NewNoopPlanResult(op string, rType ResourceType, id int, path string) *PlanResult {
	return &PlanResult{
		OperationName: op,
		ResourceType:  rType,
		ResourceID:    id,
		ResourcePath:  path,
		Action:        ActionNoop,
		Diffs:         make([]Diff, 0),
		HasChanges:    false,
	}
}

// NewSkippedPlanResult creates a PlanResult representing a skipped operation.
func NewSkippedPlanResult(op string, rType ResourceType, id int, path string, reason string) *PlanResult {
	return &PlanResult{
		OperationName: op,
		ResourceType:  rType,
		ResourceID:    id,
		ResourcePath:  path,
		Action:        ActionSkipped,
		Diffs:         make([]Diff, 0),
		HasChanges:    false,
		Details:       reason,
	}
}

// NewApplyResult creates an ApplyResult for a mutation.
func NewApplyResult(op string, rType ResourceType, id int, path string, action ActionType, status StatusType, diffs []Diff, err error, start time.Time) *ApplyResult {
	success := status == StatusSuccess || status == StatusNoop || status == StatusSkipped
	return &ApplyResult{
		OperationName: op,
		ResourceType:  rType,
		ResourceID:    id,
		ResourcePath:  path,
		Action:        action,
		Diffs:         diffs,
		Status:        status,
		Success:       success,
		Error:         err,
		ExecutedAt:    start,
		Duration:      time.Since(start),
	}
}

// NewNoopApplyResult creates an ApplyResult when no mutation was needed.
func NewNoopApplyResult(op string, rType ResourceType, id int, path string) *ApplyResult {
	return &ApplyResult{
		OperationName: op,
		ResourceType:  rType,
		ResourceID:    id,
		ResourcePath:  path,
		Action:        ActionNoop,
		Status:        StatusNoop,
		Success:       true,
		Diffs:         make([]Diff, 0),
		ExecutedAt:    time.Now(),
	}
}

// NewSkippedApplyResult creates an ApplyResult when operation was skipped.
func NewSkippedApplyResult(op string, rType ResourceType, id int, path string, reason string) *ApplyResult {
	return &ApplyResult{
		OperationName: op,
		ResourceType:  rType,
		ResourceID:    id,
		ResourcePath:  path,
		Action:        ActionSkipped,
		Status:        StatusSkipped,
		Success:       true,
		Diffs:         make([]Diff, 0),
		Details:       reason,
		ExecutedAt:    time.Now(),
	}
}
