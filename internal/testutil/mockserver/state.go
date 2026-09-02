package mockserver

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// State is a thread-safe in-memory database of GitLab resources.
type State struct {
	mu sync.RWMutex

	// Projects: key is project ID
	projects          map[int]*gitlab.Project
	projectByPath     map[string]int // path_with_namespace -> ID
	pipelineRetention map[int]int    // projectID -> ci_delete_pipelines_in_seconds
	nextProjectID     int

	// Groups: key is group ID
	groups      map[int]*gitlab.Group
	groupByPath map[string]int // full_path -> ID
	nextGroupID int

	// Group-Project relationships
	groupProjects map[int][]int // groupID -> list of projectIDs
	subgroups     map[int][]int // groupID -> list of child groupIDs

	// Push Rules
	projectPushRules map[int]*gitlab.ProjectPushRules
	groupPushRules   map[int]*gitlab.GroupPushRules

	// Protected Branches: projectID -> branchName -> *gitlab.ProtectedBranch
	protectedBranches map[int]map[string]*gitlab.ProtectedBranch

	// Merge Request Approvals
	projectApprovals     map[int]*gitlab.ProjectApprovals
	projectApprovalRules map[int]map[int]*gitlab.ProjectApprovalRule // projectID -> ruleID -> rule
	nextApprovalRuleID   int

	// CI/CD Variables: projectID -> varKey/envScope -> variable
	projectVariables map[int]map[string]*gitlab.ProjectVariable
	groupVariables   map[int]map[string]*gitlab.GroupVariable

	// Runners
	runners       map[int]*gitlab.Runner
	runnerDetails map[int]*gitlab.RunnerDetails
	nextRunnerID  int

	// Compliance Frameworks: projectID -> frameworks
	complianceFrameworks map[int][]MockComplianceFramework

	// Webhooks: projectID -> hookID -> hook
	projectHooks map[int]map[int]*gitlab.ProjectHook
	nextHookID   int

	// Members
	projectMembers map[int]map[int]*gitlab.ProjectMember // projectID -> userID -> member
	groupMembers   map[int]map[int]*gitlab.GroupMember   // groupID -> userID -> member

	// Users: userID -> User
	users          map[int]*gitlab.User
	userByUsername map[string]int
	nextUserID     int
}

// MockComplianceFramework mirrors GraphQL compliance framework model.
type MockComplianceFramework struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// NewState creates a new, empty in-memory state store.
func NewState() *State {
	s := &State{}
	s.Reset()
	return s
}

// Reset clears all in-memory resources.
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.projects = make(map[int]*gitlab.Project)
	s.projectByPath = make(map[string]int)
	s.pipelineRetention = make(map[int]int)
	s.nextProjectID = 1

	s.groups = make(map[int]*gitlab.Group)
	s.groupByPath = make(map[string]int)
	s.nextGroupID = 1

	s.groupProjects = make(map[int][]int)
	s.subgroups = make(map[int][]int)

	s.projectPushRules = make(map[int]*gitlab.ProjectPushRules)
	s.groupPushRules = make(map[int]*gitlab.GroupPushRules)

	s.protectedBranches = make(map[int]map[string]*gitlab.ProtectedBranch)
	s.projectApprovals = make(map[int]*gitlab.ProjectApprovals)
	s.projectApprovalRules = make(map[int]map[int]*gitlab.ProjectApprovalRule)
	s.nextApprovalRuleID = 1

	s.projectVariables = make(map[int]map[string]*gitlab.ProjectVariable)
	s.groupVariables = make(map[int]map[string]*gitlab.GroupVariable)

	s.runners = make(map[int]*gitlab.Runner)
	s.runnerDetails = make(map[int]*gitlab.RunnerDetails)
	s.nextRunnerID = 1

	s.complianceFrameworks = make(map[int][]MockComplianceFramework)

	s.projectHooks = make(map[int]map[int]*gitlab.ProjectHook)
	s.nextHookID = 1

	s.projectMembers = make(map[int]map[int]*gitlab.ProjectMember)
	s.groupMembers = make(map[int]map[int]*gitlab.GroupMember)

	s.users = make(map[int]*gitlab.User)
	s.userByUsername = make(map[string]int)
	s.nextUserID = 1
}

// ----------------------------------------------------------------------------
// Deep Cloning Helpers (Prevent Data Races during Concurrent JSON Encoding)
// ----------------------------------------------------------------------------

func cloneProject(p *gitlab.Project) *gitlab.Project {
	if p == nil {
		return nil
	}
	cp := *p
	if p.TagList != nil {
		cp.TagList = append([]string(nil), p.TagList...)
	}
	if p.Topics != nil {
		cp.Topics = append([]string(nil), p.Topics...)
	}
	if p.Namespace != nil {
		nsCopy := *p.Namespace
		cp.Namespace = &nsCopy
	}
	if p.Owner != nil {
		ownerCopy := *p.Owner
		cp.Owner = &ownerCopy
	}
	if p.Permissions != nil {
		permCopy := *p.Permissions
		cp.Permissions = &permCopy
	}
	return &cp
}

func cloneGroup(g *gitlab.Group) *gitlab.Group {
	if g == nil {
		return nil
	}
	cp := *g
	return &cp
}

func cloneProjectPushRules(pr *gitlab.ProjectPushRules) *gitlab.ProjectPushRules {
	if pr == nil {
		return nil
	}
	cp := *pr
	return &cp
}

func cloneGroupPushRules(pr *gitlab.GroupPushRules) *gitlab.GroupPushRules {
	if pr == nil {
		return nil
	}
	cp := *pr
	return &cp
}

func cloneProtectedBranch(pb *gitlab.ProtectedBranch) *gitlab.ProtectedBranch {
	if pb == nil {
		return nil
	}
	cp := *pb
	if pb.PushAccessLevels != nil {
		cp.PushAccessLevels = append([]*gitlab.BranchAccessDescription(nil), pb.PushAccessLevels...)
	}
	if pb.MergeAccessLevels != nil {
		cp.MergeAccessLevels = append([]*gitlab.BranchAccessDescription(nil), pb.MergeAccessLevels...)
	}
	if pb.UnprotectAccessLevels != nil {
		cp.UnprotectAccessLevels = append([]*gitlab.BranchAccessDescription(nil), pb.UnprotectAccessLevels...)
	}
	return &cp
}

func cloneProjectApprovals(pa *gitlab.ProjectApprovals) *gitlab.ProjectApprovals {
	if pa == nil {
		return nil
	}
	cp := *pa
	return &cp
}

func cloneProjectApprovalRule(ar *gitlab.ProjectApprovalRule) *gitlab.ProjectApprovalRule {
	if ar == nil {
		return nil
	}
	cp := *ar
	if ar.EligibleApprovers != nil {
		cp.EligibleApprovers = append([]*gitlab.BasicUser(nil), ar.EligibleApprovers...)
	}
	if ar.Users != nil {
		cp.Users = append([]*gitlab.BasicUser(nil), ar.Users...)
	}
	if ar.Groups != nil {
		cp.Groups = append([]*gitlab.Group(nil), ar.Groups...)
	}
	return &cp
}

func cloneProjectVariable(v *gitlab.ProjectVariable) *gitlab.ProjectVariable {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func cloneGroupVariable(v *gitlab.GroupVariable) *gitlab.GroupVariable {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func cloneProjectHook(h *gitlab.ProjectHook) *gitlab.ProjectHook {
	if h == nil {
		return nil
	}
	cp := *h
	return &cp
}

func cloneProjectMember(m *gitlab.ProjectMember) *gitlab.ProjectMember {
	if m == nil {
		return nil
	}
	cp := *m
	return &cp
}

func cloneGroupMember(m *gitlab.GroupMember) *gitlab.GroupMember {
	if m == nil {
		return nil
	}
	cp := *m
	return &cp
}

func cloneUser(u *gitlab.User) *gitlab.User {
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

func cloneRunner(r *gitlab.Runner) *gitlab.Runner {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

func cloneRunnerDetails(d *gitlab.RunnerDetails) *gitlab.RunnerDetails {
	if d == nil {
		return nil
	}
	cp := *d
	if d.TagList != nil {
		cp.TagList = make([]string, len(d.TagList))
		copy(cp.TagList, d.TagList)
	}
	return &cp
}

// ----------------------------------------------------------------------------
// Project Operations
// ----------------------------------------------------------------------------

func (s *State) AddProject(p *gitlab.Project) *gitlab.Project {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.ID == 0 {
		p.ID = s.nextProjectID
		s.nextProjectID++
	} else if p.ID >= s.nextProjectID {
		s.nextProjectID = p.ID + 1
	}
	if p.CreatedAt == nil {
		now := time.Now()
		p.CreatedAt = &now
	}
	s.projects[p.ID] = p
	if p.PathWithNamespace != "" {
		s.projectByPath[strings.ToLower(p.PathWithNamespace)] = p.ID
	}
	return cloneProject(p)
}

func (s *State) GetProject(idOrPath any) (*gitlab.Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	p, found := s.projects[id]
	if !found {
		return nil, false
	}
	return cloneProject(p), true
}

func (s *State) UpdateProject(idOrPath any, updater func(*gitlab.Project)) (*gitlab.Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	p, found := s.projects[id]
	if !found {
		return nil, false
	}
	updater(p)
	return cloneProject(p), true
}

func (s *State) ListProjects() []*gitlab.Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]*gitlab.Project, 0, len(s.projects))
	for _, p := range s.projects {
		res = append(res, cloneProject(p))
	}
	return res
}

func (s *State) SetPipelineRetention(projectID int, seconds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelineRetention[projectID] = seconds
}

func (s *State) GetPipelineRetention(projectID int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pipelineRetention[projectID]
}

func (s *State) GetProjectPipelineRetention(projectID int) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sec, ok := s.pipelineRetention[projectID]
	return sec, ok
}

func (s *State) resolveProjectIDLocked(idOrPath any) (int, bool) {
	switch v := idOrPath.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		if id, err := strconv.Atoi(v); err == nil {
			return id, true
		}
		if unescaped, err := url.PathUnescape(v); err == nil {
			v = unescaped
		}
		cleanPath := strings.Trim(strings.ToLower(v), "/")
		id, ok := s.projectByPath[cleanPath]
		return id, ok
	default:
		return 0, false
	}
}

// ----------------------------------------------------------------------------
// Group Operations
// ----------------------------------------------------------------------------

func (s *State) AddGroup(g *gitlab.Group) *gitlab.Group {
	s.mu.Lock()
	defer s.mu.Unlock()

	if g.ID == 0 {
		g.ID = s.nextGroupID
		s.nextGroupID++
	} else if g.ID >= s.nextGroupID {
		s.nextGroupID = g.ID + 1
	}
	s.groups[g.ID] = g
	if g.FullPath != "" {
		s.groupByPath[strings.ToLower(g.FullPath)] = g.ID
	}
	if g.ParentID != 0 {
		s.subgroups[g.ParentID] = append(s.subgroups[g.ParentID], g.ID)
	}
	return cloneGroup(g)
}

func (s *State) AddSubgroup(parentGroupID, childGroupID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subgroups[parentGroupID] = append(s.subgroups[parentGroupID], childGroupID)
}

func (s *State) GetGroup(idOrPath any) (*gitlab.Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	g, found := s.groups[id]
	if !found {
		return nil, false
	}
	return cloneGroup(g), true
}

func (s *State) ListGroups() []*gitlab.Group {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]*gitlab.Group, 0, len(s.groups))
	for _, g := range s.groups {
		res = append(res, cloneGroup(g))
	}
	return res
}

func (s *State) ListSubgroups(parentIDOrPath any) []*gitlab.Group {
	s.mu.RLock()
	defer s.mu.RUnlock()

	parentID, ok := s.resolveGroupIDLocked(parentIDOrPath)
	if !ok {
		return nil
	}
	childIDs := s.subgroups[parentID]
	res := make([]*gitlab.Group, 0, len(childIDs))
	for _, cid := range childIDs {
		if g, found := s.groups[cid]; found {
			res = append(res, cloneGroup(g))
		}
	}
	return res
}

func (s *State) AddGroupProject(groupID, projectID int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.groupProjects[groupID] = append(s.groupProjects[groupID], projectID)
}

func (s *State) ListGroupProjects(groupIDOrPath any) []*gitlab.Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	groupID, ok := s.resolveGroupIDLocked(groupIDOrPath)
	if !ok {
		return nil
	}
	pIDs := s.groupProjects[groupID]
	res := make([]*gitlab.Project, 0, len(pIDs))
	for _, pid := range pIDs {
		if p, found := s.projects[pid]; found {
			res = append(res, cloneProject(p))
		}
	}
	return res
}

func (s *State) resolveGroupIDLocked(idOrPath any) (int, bool) {
	switch v := idOrPath.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		if id, err := strconv.Atoi(v); err == nil {
			return id, true
		}
		if unescaped, err := url.PathUnescape(v); err == nil {
			v = unescaped
		}
		cleanPath := strings.Trim(strings.ToLower(v), "/")
		id, ok := s.groupByPath[cleanPath]
		return id, ok
	default:
		return 0, false
	}
}

// ----------------------------------------------------------------------------
// Push Rules Operations
// ----------------------------------------------------------------------------

func (s *State) GetProjectPushRule(idOrPath any) (*gitlab.ProjectPushRules, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	r, found := s.projectPushRules[id]
	if !found {
		return nil, false
	}
	return cloneProjectPushRules(r), true
}

func (s *State) SetProjectPushRule(idOrPath any, r *gitlab.ProjectPushRules) (*gitlab.ProjectPushRules, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	r.ID = id
	s.projectPushRules[id] = r
	return cloneProjectPushRules(r), true
}

func (s *State) GetGroupPushRule(idOrPath any) (*gitlab.GroupPushRules, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	r, found := s.groupPushRules[id]
	if !found {
		return nil, false
	}
	return cloneGroupPushRules(r), true
}

func (s *State) SetGroupPushRule(idOrPath any, r *gitlab.GroupPushRules) (*gitlab.GroupPushRules, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	r.ID = id
	s.groupPushRules[id] = r
	return cloneGroupPushRules(r), true
}

// ----------------------------------------------------------------------------
// Protected Branches Operations
// ----------------------------------------------------------------------------

func (s *State) ListProtectedBranches(idOrPath any) []*gitlab.ProtectedBranch {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil
	}
	branches := s.protectedBranches[id]
	res := make([]*gitlab.ProtectedBranch, 0, len(branches))
	for _, b := range branches {
		res = append(res, cloneProtectedBranch(b))
	}
	return res
}

func (s *State) ProtectBranch(idOrPath any, b *gitlab.ProtectedBranch) (*gitlab.ProtectedBranch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	if s.protectedBranches[id] == nil {
		s.protectedBranches[id] = make(map[string]*gitlab.ProtectedBranch)
	}
	s.protectedBranches[id][b.Name] = b
	return cloneProtectedBranch(b), true
}

func (s *State) UnprotectBranch(idOrPath any, branch string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok || s.protectedBranches[id] == nil {
		return false
	}
	_, found := s.protectedBranches[id][branch]
	if found {
		delete(s.protectedBranches[id], branch)
	}
	return found
}

func (s *State) GetProtectedBranch(idOrPath any, branch string) (*gitlab.ProtectedBranch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok || s.protectedBranches[id] == nil {
		return nil, false
	}
	b, found := s.protectedBranches[id][branch]
	if !found {
		return nil, false
	}
	return cloneProtectedBranch(b), true
}

// ----------------------------------------------------------------------------
// Merge Request Approvals Operations
// ----------------------------------------------------------------------------

func (s *State) GetProjectApprovals(idOrPath any) *gitlab.ProjectApprovals {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil
	}
	app, found := s.projectApprovals[id]
	if !found {
		return &gitlab.ProjectApprovals{}
	}
	return cloneProjectApprovals(app)
}

func (s *State) SetProjectApprovals(idOrPath any, a *gitlab.ProjectApprovals) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return false
	}
	s.projectApprovals[id] = a
	return true
}

func (s *State) ListApprovalRules(idOrPath any) []*gitlab.ProjectApprovalRule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil
	}
	rules := s.projectApprovalRules[id]
	res := make([]*gitlab.ProjectApprovalRule, 0, len(rules))
	for _, r := range rules {
		res = append(res, cloneProjectApprovalRule(r))
	}
	return res
}

func (s *State) AddApprovalRule(idOrPath any, r *gitlab.ProjectApprovalRule) (*gitlab.ProjectApprovalRule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	if s.projectApprovalRules[id] == nil {
		s.projectApprovalRules[id] = make(map[int]*gitlab.ProjectApprovalRule)
	}
	if r.ID == 0 {
		r.ID = s.nextApprovalRuleID
		s.nextApprovalRuleID++
	} else if r.ID >= s.nextApprovalRuleID {
		s.nextApprovalRuleID = r.ID + 1
	}
	s.projectApprovalRules[id][r.ID] = r
	return cloneProjectApprovalRule(r), true
}

func (s *State) UpdateApprovalRule(idOrPath any, ruleID int, updater func(*gitlab.ProjectApprovalRule)) (*gitlab.ProjectApprovalRule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok || s.projectApprovalRules[id] == nil {
		return nil, false
	}
	rule, found := s.projectApprovalRules[id][ruleID]
	if !found {
		return nil, false
	}
	updater(rule)
	return cloneProjectApprovalRule(rule), true
}

func (s *State) DeleteApprovalRule(idOrPath any, ruleID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok || s.projectApprovalRules[id] == nil {
		return false
	}
	_, found := s.projectApprovalRules[id][ruleID]
	if found {
		delete(s.projectApprovalRules[id], ruleID)
	}
	return found
}

// ----------------------------------------------------------------------------
// CI/CD Variables Operations
// ----------------------------------------------------------------------------

func varCompositeKey(key, envScope string) string {
	if envScope == "" {
		envScope = "*"
	}
	return fmt.Sprintf("%s::%s", key, envScope)
}

func (s *State) ListProjectVariables(idOrPath any) []*gitlab.ProjectVariable {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil
	}
	vars := s.projectVariables[id]
	res := make([]*gitlab.ProjectVariable, 0, len(vars))
	for _, v := range vars {
		res = append(res, cloneProjectVariable(v))
	}
	return res
}

func (s *State) SetProjectVariable(idOrPath any, v *gitlab.ProjectVariable) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return false
	}
	if s.projectVariables[id] == nil {
		s.projectVariables[id] = make(map[string]*gitlab.ProjectVariable)
	}
	s.projectVariables[id][varCompositeKey(v.Key, v.EnvironmentScope)] = v
	return true
}

func (s *State) RemoveProjectVariable(idOrPath any, key, envScope string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok || s.projectVariables[id] == nil {
		return false
	}
	cKey := varCompositeKey(key, envScope)
	_, found := s.projectVariables[id][cKey]
	if found {
		delete(s.projectVariables[id], cKey)
		return true
	}
	for k, v := range s.projectVariables[id] {
		if v.Key == key && (envScope == "" || v.EnvironmentScope == envScope) {
			delete(s.projectVariables[id], k)
			return true
		}
	}
	return false
}

func (s *State) ListGroupVariables(idOrPath any) []*gitlab.GroupVariable {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok {
		return nil
	}
	vars := s.groupVariables[id]
	res := make([]*gitlab.GroupVariable, 0, len(vars))
	for _, v := range vars {
		res = append(res, cloneGroupVariable(v))
	}
	return res
}

func (s *State) SetGroupVariable(idOrPath any, v *gitlab.GroupVariable) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok {
		return false
	}
	if s.groupVariables[id] == nil {
		s.groupVariables[id] = make(map[string]*gitlab.GroupVariable)
	}
	s.groupVariables[id][varCompositeKey(v.Key, v.EnvironmentScope)] = v
	return true
}

func (s *State) RemoveGroupVariable(idOrPath any, key, envScope string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok || s.groupVariables[id] == nil {
		return false
	}
	cKey := varCompositeKey(key, envScope)
	_, found := s.groupVariables[id][cKey]
	if found {
		delete(s.groupVariables[id], cKey)
		return true
	}
	for k, v := range s.groupVariables[id] {
		if v.Key == key && (envScope == "" || v.EnvironmentScope == envScope) {
			delete(s.groupVariables[id], k)
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------------------
// Runners Operations
// ----------------------------------------------------------------------------

func (s *State) AddRunner(r *gitlab.Runner, details *gitlab.RunnerDetails) *gitlab.Runner {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.ID == 0 {
		r.ID = s.nextRunnerID
		s.nextRunnerID++
	} else if r.ID >= s.nextRunnerID {
		s.nextRunnerID = r.ID + 1
	}
	s.runners[r.ID] = r
	if details != nil {
		details.ID = r.ID
		s.runnerDetails[r.ID] = details
	}
	return cloneRunner(r)
}

func (s *State) ListRunners() []*gitlab.Runner {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]*gitlab.Runner, 0, len(s.runners))
	for _, r := range s.runners {
		res = append(res, cloneRunner(r))
	}
	return res
}

func (s *State) GetRunnerDetails(id int) (*gitlab.RunnerDetails, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, found := s.runnerDetails[id]
	if !found {
		return nil, false
	}
	return cloneRunnerDetails(d), true
}

func (s *State) UpdateRunnerDetails(id int, updater func(*gitlab.RunnerDetails)) (*gitlab.RunnerDetails, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, found := s.runnerDetails[id]
	if !found {
		return nil, false
	}
	updater(d)
	return cloneRunnerDetails(d), true
}

// ----------------------------------------------------------------------------
// Compliance Operations
// ----------------------------------------------------------------------------

func (s *State) GetComplianceFrameworks(projectID int) []MockComplianceFramework {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]MockComplianceFramework, len(s.complianceFrameworks[projectID]))
	copy(res, s.complianceFrameworks[projectID])
	return res
}

func (s *State) SetComplianceFramework(projectID int, f MockComplianceFramework) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.complianceFrameworks[projectID] = []MockComplianceFramework{f}
}

func (s *State) RemoveComplianceFramework(projectID int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.complianceFrameworks, projectID)
}

// ----------------------------------------------------------------------------
// Webhooks Operations
// ----------------------------------------------------------------------------

func (s *State) ListProjectHooks(idOrPath any) []*gitlab.ProjectHook {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil
	}
	hooks := s.projectHooks[id]
	res := make([]*gitlab.ProjectHook, 0, len(hooks))
	for _, h := range hooks {
		res = append(res, cloneProjectHook(h))
	}
	return res
}

func (s *State) AddProjectHook(idOrPath any, h *gitlab.ProjectHook) (*gitlab.ProjectHook, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil, false
	}
	if s.projectHooks[id] == nil {
		s.projectHooks[id] = make(map[int]*gitlab.ProjectHook)
	}
	if h.ID == 0 {
		h.ID = s.nextHookID
		s.nextHookID++
	} else if h.ID >= s.nextHookID {
		s.nextHookID = h.ID + 1
	}
	s.projectHooks[id][h.ID] = h
	return cloneProjectHook(h), true
}

func (s *State) EditProjectHook(idOrPath any, hookID int, updater func(*gitlab.ProjectHook)) (*gitlab.ProjectHook, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok || s.projectHooks[id] == nil {
		return nil, false
	}
	hook, found := s.projectHooks[id][hookID]
	if !found {
		return nil, false
	}
	updater(hook)
	return cloneProjectHook(hook), true
}

func (s *State) DeleteProjectHook(idOrPath any, hookID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok || s.projectHooks[id] == nil {
		return false
	}
	_, found := s.projectHooks[id][hookID]
	if found {
		delete(s.projectHooks[id], hookID)
	}
	return found
}

// ----------------------------------------------------------------------------
// Members Operations
// ----------------------------------------------------------------------------

func (s *State) ListProjectMembers(idOrPath any) []*gitlab.ProjectMember {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return nil
	}
	members := s.projectMembers[id]
	res := make([]*gitlab.ProjectMember, 0, len(members))
	for _, m := range members {
		res = append(res, cloneProjectMember(m))
	}
	return res
}

func (s *State) AddProjectMember(idOrPath any, m *gitlab.ProjectMember) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveProjectIDLocked(idOrPath)
	if !ok {
		return false
	}
	if s.projectMembers[id] == nil {
		s.projectMembers[id] = make(map[int]*gitlab.ProjectMember)
	}
	s.projectMembers[id][m.ID] = m
	return true
}

func (s *State) ListGroupMembers(idOrPath any) []*gitlab.GroupMember {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok {
		return nil
	}
	members := s.groupMembers[id]
	res := make([]*gitlab.GroupMember, 0, len(members))
	for _, m := range members {
		res = append(res, cloneGroupMember(m))
	}
	return res
}

func (s *State) AddGroupMember(idOrPath any, m *gitlab.GroupMember) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.resolveGroupIDLocked(idOrPath)
	if !ok {
		return false
	}
	if s.groupMembers[id] == nil {
		s.groupMembers[id] = make(map[int]*gitlab.GroupMember)
	}
	s.groupMembers[id][m.ID] = m
	return true
}

// ----------------------------------------------------------------------------
// Users Operations
// ----------------------------------------------------------------------------

func (s *State) AddUser(u *gitlab.User) *gitlab.User {
	s.mu.Lock()
	defer s.mu.Unlock()

	if u.ID == 0 {
		u.ID = s.nextUserID
		s.nextUserID++
	} else if u.ID >= s.nextUserID {
		s.nextUserID = u.ID + 1
	}
	s.users[u.ID] = u
	if u.Username != "" {
		s.userByUsername[strings.ToLower(u.Username)] = u.ID
	}
	return cloneUser(u)
}

func (s *State) GetUser(id int) (*gitlab.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, found := s.users[id]
	if !found {
		return nil, false
	}
	return cloneUser(u), true
}

func (s *State) GetUserByUsername(username string) (*gitlab.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, found := s.userByUsername[strings.ToLower(username)]
	if !found {
		return nil, false
	}
	return cloneUser(s.users[id]), true
}

func (s *State) ListUsers() []*gitlab.User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]*gitlab.User, 0, len(s.users))
	for _, u := range s.users {
		res = append(res, cloneUser(u))
	}
	return res
}
