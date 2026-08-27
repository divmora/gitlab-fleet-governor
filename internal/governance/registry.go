package governance

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
)

// OperationsRegistry manages the registration, ordering, and execution of all governance operations.
type OperationsRegistry struct {
	mu         sync.RWMutex
	operations map[string]GovernanceOperation
	client     gitlab.GitLabClient
}

// NewOperationsRegistry creates an empty OperationsRegistry.
func NewOperationsRegistry(client gitlab.GitLabClient) *OperationsRegistry {
	return &OperationsRegistry{
		operations: make(map[string]GovernanceOperation),
		client:     client,
	}
}

// NewDefaultRegistry instantiates an OperationsRegistry pre-loaded with all 10 governance reconcilers.
func NewDefaultRegistry(client gitlab.GitLabClient) *OperationsRegistry {
	reg := NewOperationsRegistry(client)
	reg.Register(NewPushRulesReconciler())
	reg.Register(NewProtectedBranchesReconciler())
	reg.Register(NewApprovalRulesReconciler())
	reg.Register(NewProjectSettingsReconciler())
	reg.Register(NewPipelineRetentionReconciler())
	reg.Register(NewVariablesReconciler())
	reg.Register(NewRunnersReconciler())
	reg.Register(NewComplianceReconciler())
	reg.Register(NewWebhooksReconciler())
	reg.Register(NewMembersReconciler())
	return reg
}

// Register registers a governance operation.
func (r *OperationsRegistry) Register(op GovernanceOperation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations[op.Name()] = op
}

// Get retrieves an operation by name.
func (r *OperationsRegistry) Get(name string) (GovernanceOperation, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	op, found := r.operations[name]
	return op, found
}

// OrderedOperations returns all registered operations sorted in ascending dependency order.
func (r *OperationsRegistry) OrderedOperations() []GovernanceOperation {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]GovernanceOperation, 0, len(r.operations))
	for _, op := range r.operations {
		list = append(list, op)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].Order() == list[j].Order() {
			return list[i].Name() < list[j].Name()
		}
		return list[i].Order() < list[j].Order()
	})

	return list
}

// PlanProject executes Plan across all operations in dependency order on a project.
func (r *OperationsRegistry) PlanProject(ctx context.Context, project *gogitlab.Project, cfg *config.PolicyConfig) ([]PlanResult, error) {
	ops := r.OrderedOperations()
	results := make([]PlanResult, 0, len(ops))

	for _, op := range ops {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		planRes, err := op.Plan(ctx, r.client, project, cfg)
		if err != nil {
			if planRes == nil {
				planRes = &PlanResult{
					OperationName: op.Name(),
					ResourceType:  ResourceTypeProject,
					ResourceID:    project.ID,
					ResourcePath:  project.PathWithNamespace,
					Error:         err,
				}
			}
			results = append(results, *planRes)
			return results, fmt.Errorf("operation %s failed planning on project %d: %w", op.Name(), project.ID, err)
		}

		if planRes != nil {
			results = append(results, *planRes)
		}
	}

	return results, nil
}

// ApplyProject executes Apply across all operations in dependency order on a project.
func (r *OperationsRegistry) ApplyProject(ctx context.Context, project *gogitlab.Project, cfg *config.PolicyConfig) ([]ApplyResult, error) {
	ops := r.OrderedOperations()
	results := make([]ApplyResult, 0, len(ops))

	for _, op := range ops {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		applyRes, err := op.Apply(ctx, r.client, project, cfg)
		if err != nil {
			if applyRes == nil {
				applyRes = &ApplyResult{
					OperationName: op.Name(),
					ResourceType:  ResourceTypeProject,
					ResourceID:    project.ID,
					ResourcePath:  project.PathWithNamespace,
					Status:        StatusFailed,
					Success:       false,
					Error:         err,
				}
			}
			results = append(results, *applyRes)
			return results, fmt.Errorf("operation %s failed applying on project %d: %w", op.Name(), project.ID, err)
		}

		if applyRes != nil {
			results = append(results, *applyRes)
		}
	}

	return results, nil
}

// PlanGroup executes PlanGroup across all operations in dependency order on a group.
func (r *OperationsRegistry) PlanGroup(ctx context.Context, group *gogitlab.Group, cfg *config.PolicyConfig) ([]PlanResult, error) {
	ops := r.OrderedOperations()
	results := make([]PlanResult, 0, len(ops))

	for _, op := range ops {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		planRes, err := op.PlanGroup(ctx, r.client, group, cfg)
		if err != nil {
			if planRes == nil {
				planRes = &PlanResult{
					OperationName: op.Name(),
					ResourceType:  ResourceTypeGroup,
					ResourceID:    group.ID,
					ResourcePath:  group.FullPath,
					Error:         err,
				}
			}
			results = append(results, *planRes)
			return results, fmt.Errorf("operation %s failed planning on group %d: %w", op.Name(), group.ID, err)
		}

		if planRes != nil {
			results = append(results, *planRes)
		}
	}

	return results, nil
}

// ApplyGroup executes ApplyGroup across all operations in dependency order on a group.
func (r *OperationsRegistry) ApplyGroup(ctx context.Context, group *gogitlab.Group, cfg *config.PolicyConfig) ([]ApplyResult, error) {
	ops := r.OrderedOperations()
	results := make([]ApplyResult, 0, len(ops))

	for _, op := range ops {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		applyRes, err := op.ApplyGroup(ctx, r.client, group, cfg)
		if err != nil {
			if applyRes == nil {
				applyRes = &ApplyResult{
					OperationName: op.Name(),
					ResourceType:  ResourceTypeGroup,
					ResourceID:    group.ID,
					ResourcePath:  group.FullPath,
					Status:        StatusFailed,
					Success:       false,
					Error:         err,
				}
			}
			results = append(results, *applyRes)
			return results, fmt.Errorf("operation %s failed applying on group %d: %w", op.Name(), group.ID, err)
		}

		if applyRes != nil {
			results = append(results, *applyRes)
		}
	}

	return results, nil
}

// PlanTargetProject is a helper adapter accepting discovery.TargetProject.
func (r *OperationsRegistry) PlanTargetProject(ctx context.Context, target *discovery.TargetProject, cfg *config.PolicyConfig) ([]PlanResult, error) {
	raw := target.Raw
	if raw == nil {
		raw = &gogitlab.Project{
			ID:                target.ID,
			Name:              target.Name,
			Path:              target.Path,
			PathWithNamespace: target.PathWithNamespace,
			DefaultBranch:     target.DefaultBranch,
			Archived:          target.Archived,
		}
	}
	return r.PlanProject(ctx, raw, cfg)
}

// ApplyTargetProject is a helper adapter accepting discovery.TargetProject.
func (r *OperationsRegistry) ApplyTargetProject(ctx context.Context, target *discovery.TargetProject, cfg *config.PolicyConfig) ([]ApplyResult, error) {
	raw := target.Raw
	if raw == nil {
		raw = &gogitlab.Project{
			ID:                target.ID,
			Name:              target.Name,
			Path:              target.Path,
			PathWithNamespace: target.PathWithNamespace,
			DefaultBranch:     target.DefaultBranch,
			Archived:          target.Archived,
		}
	}
	return r.ApplyProject(ctx, raw, cfg)
}

// PlanTargetGroup is a helper adapter accepting discovery.TargetGroup.
func (r *OperationsRegistry) PlanTargetGroup(ctx context.Context, target *discovery.TargetGroup, cfg *config.PolicyConfig) ([]PlanResult, error) {
	raw := target.Raw
	if raw == nil {
		raw = &gogitlab.Group{
			ID:       target.ID,
			Name:     target.Name,
			Path:     target.Path,
			FullPath: target.FullPath,
			ParentID: target.ParentID,
		}
	}
	return r.PlanGroup(ctx, raw, cfg)
}

// ApplyTargetGroup is a helper adapter accepting discovery.TargetGroup.
func (r *OperationsRegistry) ApplyTargetGroup(ctx context.Context, target *discovery.TargetGroup, cfg *config.PolicyConfig) ([]ApplyResult, error) {
	raw := target.Raw
	if raw == nil {
		raw = &gogitlab.Group{
			ID:       target.ID,
			Name:     target.Name,
			Path:     target.Path,
			FullPath: target.FullPath,
			ParentID: target.ParentID,
		}
	}
	return r.ApplyGroup(ctx, raw, cfg)
}
