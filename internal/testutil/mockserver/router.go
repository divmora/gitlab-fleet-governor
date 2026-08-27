package mockserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// Router handles incoming HTTP requests for MockGitLabServer.
type Router struct {
	state  *State
	faults *FaultEngine
}

// NewRouter creates a new Router with given State and FaultEngine.
func NewRouter(state *State, faults *FaultEngine) *Router {
	return &Router{
		state:  state,
		faults: faults,
	}
}

// ServeHTTP satisfies http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Fault Engine evaluation
	if rt.faults != nil {
		injected, code, headers, body := rt.faults.MatchAndApply(r)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		if injected {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write(body)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")

	rawPath := r.URL.EscapedPath()
	if rawPath == "" {
		rawPath = r.URL.Path
	}

	// Route GraphQL requests
	if rawPath == "/graphql" || rawPath == "/api/v4/graphql" {
		rt.handleGraphQL(w, r)
		return
	}

	// Must start with /api/v4
	if !strings.HasPrefix(rawPath, "/api/v4") {
		http.NotFound(w, r)
		return
	}

	subPath := strings.TrimPrefix(rawPath, "/api/v4")

	switch {
	case strings.HasPrefix(subPath, "/projects"):
		rt.routeProjects(w, r, strings.TrimPrefix(subPath, "/projects"))
	case strings.HasPrefix(subPath, "/groups"):
		rt.routeGroups(w, r, strings.TrimPrefix(subPath, "/groups"))
	case strings.HasPrefix(subPath, "/runners"):
		rt.routeRunners(w, r, strings.TrimPrefix(subPath, "/runners"))
	case strings.HasPrefix(subPath, "/users"):
		rt.routeUsers(w, r, strings.TrimPrefix(subPath, "/users"))
	default:
		http.NotFound(w, r)
	}
}

// ----------------------------------------------------------------------------
// Projects Routing
// ----------------------------------------------------------------------------

func (rt *Router) routeProjects(w http.ResponseWriter, r *http.Request, sub string) {
	if sub == "" || sub == "/" {
		if r.Method == http.MethodGet {
			rt.handleListProjects(w, r)
			return
		}
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Parse /:id/...
	parts := strings.Split(strings.TrimPrefix(sub, "/"), "/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}

	rawID := parts[0]
	unescapedID, err := url.PathUnescape(rawID)
	if err != nil {
		unescapedID = rawID
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			rt.handleGetProject(w, r, unescapedID)
		case http.MethodPut:
			rt.handleEditProject(w, r, unescapedID)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	action := parts[1]
	switch action {
	case "push_rule":
		rt.handleProjectPushRule(w, r, unescapedID)
	case "protected_branches":
		branchName := ""
		if len(parts) > 2 {
			branchName, _ = url.PathUnescape(strings.Join(parts[2:], "/"))
		}
		rt.handleProjectProtectedBranches(w, r, unescapedID, branchName)
	case "approvals":
		rt.handleProjectApprovals(w, r, unescapedID)
	case "approval_rules":
		ruleIDStr := ""
		if len(parts) > 2 {
			ruleIDStr = parts[2]
		}
		rt.handleProjectApprovalRules(w, r, unescapedID, ruleIDStr)
	case "variables":
		varKey := ""
		if len(parts) > 2 {
			varKey, _ = url.PathUnescape(parts[2])
		}
		rt.handleProjectVariables(w, r, unescapedID, varKey)
	case "runners":
		rt.handleProjectRunners(w, r, unescapedID)
	case "hooks":
		hookIDStr := ""
		if len(parts) > 2 {
			hookIDStr = parts[2]
		}
		rt.handleProjectHooks(w, r, unescapedID, hookIDStr)
	case "members":
		rt.handleProjectMembers(w, r, unescapedID)
	default:
		http.NotFound(w, r)
	}
}

func (rt *Router) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects := rt.state.ListProjects()

	// Apply optional search filter
	if search := r.URL.Query().Get("search"); search != "" {
		sLower := strings.ToLower(search)
		filtered := make([]*gitlab.Project, 0)
		for _, p := range projects {
			if strings.Contains(strings.ToLower(p.Name), sLower) || strings.Contains(strings.ToLower(p.PathWithNamespace), sLower) {
				filtered = append(filtered, p)
			}
		}
		projects = filtered
	}

	// Apply visibility filter
	if vis := r.URL.Query().Get("visibility"); vis != "" {
		filtered := make([]*gitlab.Project, 0)
		for _, p := range projects {
			if string(p.Visibility) == vis {
				filtered = append(filtered, p)
			}
		}
		projects = filtered
	}

	// Apply archived filter
	if archStr := r.URL.Query().Get("archived"); archStr != "" {
		if arch, err := strconv.ParseBool(archStr); err == nil {
			filtered := make([]*gitlab.Project, 0)
			for _, p := range projects {
				if p.Archived == arch {
					filtered = append(filtered, p)
				}
			}
			projects = filtered
		}
	}

	// Sort by ID
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].ID < projects[j].ID
	})

	rt.paginate(w, r, len(projects), func(i int) any { return projects[i] })
}

func (rt *Router) handleGetProject(w http.ResponseWriter, r *http.Request, idOrPath string) {
	p, found := rt.state.GetProject(idOrPath)
	if !found {
		http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
		return
	}
	respMap := make(map[string]any)
	data, _ := json.Marshal(p)
	_ = json.Unmarshal(data, &respMap)
	respMap["ci_delete_pipelines_in_seconds"] = rt.state.GetPipelineRetention(p.ID)
	_ = json.NewEncoder(w).Encode(respMap)
}

func (rt *Router) handleEditProject(w http.ResponseWriter, r *http.Request, idOrPath string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	p, found := rt.state.UpdateProject(idOrPath, func(proj *gitlab.Project) {
		if v, ok := body["default_branch"].(string); ok {
			proj.DefaultBranch = v
		}
		if v, ok := body["squash_option"].(string); ok {
			proj.SquashOption = gitlab.SquashOptionValue(v)
		}
		if v, ok := body["merge_method"].(string); ok {
			proj.MergeMethod = gitlab.MergeMethodValue(v)
		}
		if v, ok := body["only_allow_merge_if_pipeline_succeeds"].(bool); ok {
			proj.OnlyAllowMergeIfPipelineSucceeds = v
		}
		if v, ok := body["allow_merge_on_skipped_pipeline"].(bool); ok {
			proj.AllowMergeOnSkippedPipeline = v
		}
		if v, ok := body["only_allow_merge_if_all_discussions_are_resolved"].(bool); ok {
			proj.OnlyAllowMergeIfAllDiscussionsAreResolved = v
		}
		if v, ok := body["remove_source_branch_after_merge"].(bool); ok {
			proj.RemoveSourceBranchAfterMerge = v
		}
		if v, ok := body["auto_cancel_pending_pipelines"].(string); ok {
			proj.AutoCancelPendingPipelines = v
		}
		if v, ok := body["auto_devops_enabled"].(bool); ok {
			proj.AutoDevopsEnabled = v
		}
		if v, ok := body["keep_latest_artifact"].(bool); ok {
			proj.KeepLatestArtifact = v
		}
		if v, ok := body["printing_merge_request_link_enabled"].(bool); ok {
			proj.PrintingMergeRequestLinkEnabled = v
		}
		if v, ok := body["shared_runners_enabled"].(bool); ok {
			proj.SharedRunnersEnabled = v
		}
		if v, ok := body["group_runners_enabled"].(bool); ok {
			proj.GroupRunnersEnabled = v
		}
	})

	if !found {
		http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
		return
	}
	if v, ok := body["ci_delete_pipelines_in_seconds"].(float64); ok {
		rt.state.SetPipelineRetention(p.ID, int(v))
	}
	respMap := make(map[string]any)
	data, _ := json.Marshal(p)
	_ = json.Unmarshal(data, &respMap)
	respMap["ci_delete_pipelines_in_seconds"] = rt.state.GetPipelineRetention(p.ID)
	_ = json.NewEncoder(w).Encode(respMap)
}

func (rt *Router) handleProjectPushRule(w http.ResponseWriter, r *http.Request, idOrPath string) {
	switch r.Method {
	case http.MethodGet:
		pr, found := rt.state.GetProjectPushRule(idOrPath)
		if !found {
			http.Error(w, `{"message":"404 Push Rule Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(pr)
	case http.MethodPost:
		var pr gitlab.ProjectPushRules
		if err := json.NewDecoder(r.Body).Decode(&pr); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		saved, ok := rt.state.SetProjectPushRule(idOrPath, &pr)
		if !ok {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(saved)
	case http.MethodPut:
		var pr gitlab.ProjectPushRules
		if err := json.NewDecoder(r.Body).Decode(&pr); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		saved, ok := rt.state.SetProjectPushRule(idOrPath, &pr)
		if !ok {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(saved)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleProjectProtectedBranches(w http.ResponseWriter, r *http.Request, idOrPath, branch string) {
	if branch == "" {
		switch r.Method {
		case http.MethodGet:
			branches := rt.state.ListProtectedBranches(idOrPath)
			rt.paginate(w, r, len(branches), func(i int) any { return branches[i] })
		case http.MethodPost:
			var body struct {
				Name                      string                            `json:"name"`
				PushAccessLevel           *int                              `json:"push_access_level"`
				MergeAccessLevel          *int                              `json:"merge_access_level"`
				UnprotectAccessLevel      *int                              `json:"unprotect_access_level"`
				AllowedToPush             []*gitlab.BranchAccessDescription `json:"allowed_to_push"`
				AllowedToMerge            []*gitlab.BranchAccessDescription `json:"allowed_to_merge"`
				AllowedToUnprotect        []*gitlab.BranchAccessDescription `json:"allowed_to_unprotect"`
				PushAccessLevels          []*gitlab.BranchAccessDescription `json:"push_access_levels"`
				MergeAccessLevels         []*gitlab.BranchAccessDescription `json:"merge_access_levels"`
				UnprotectAccessLevels     []*gitlab.BranchAccessDescription `json:"unprotect_access_levels"`
				AllowForcePush            *bool                             `json:"allow_force_push"`
				CodeOwnerApprovalRequired *bool                             `json:"code_owner_approval_required"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			pb := gitlab.ProtectedBranch{
				Name: body.Name,
			}
			if body.AllowForcePush != nil {
				pb.AllowForcePush = *body.AllowForcePush
			}
			if body.CodeOwnerApprovalRequired != nil {
				pb.CodeOwnerApprovalRequired = *body.CodeOwnerApprovalRequired
			}
			if len(body.PushAccessLevels) > 0 {
				pb.PushAccessLevels = body.PushAccessLevels
			} else if len(body.AllowedToPush) > 0 {
				pb.PushAccessLevels = body.AllowedToPush
			} else if body.PushAccessLevel != nil {
				pb.PushAccessLevels = []*gitlab.BranchAccessDescription{
					{AccessLevel: gitlab.AccessLevelValue(*body.PushAccessLevel)},
				}
			}
			if len(body.MergeAccessLevels) > 0 {
				pb.MergeAccessLevels = body.MergeAccessLevels
			} else if len(body.AllowedToMerge) > 0 {
				pb.MergeAccessLevels = body.AllowedToMerge
			} else if body.MergeAccessLevel != nil {
				pb.MergeAccessLevels = []*gitlab.BranchAccessDescription{
					{AccessLevel: gitlab.AccessLevelValue(*body.MergeAccessLevel)},
				}
			}
			if len(body.UnprotectAccessLevels) > 0 {
				pb.UnprotectAccessLevels = body.UnprotectAccessLevels
			} else if len(body.AllowedToUnprotect) > 0 {
				pb.UnprotectAccessLevels = body.AllowedToUnprotect
			} else if body.UnprotectAccessLevel != nil {
				pb.UnprotectAccessLevels = []*gitlab.BranchAccessDescription{
					{AccessLevel: gitlab.AccessLevelValue(*body.UnprotectAccessLevel)},
				}
			}
			saved, ok := rt.state.ProtectBranch(idOrPath, &pb)
			if !ok {
				http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(saved)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		b, found := rt.state.GetProtectedBranch(idOrPath, branch)
		if !found {
			http.Error(w, `{"message":"404 Protected Branch Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(b)
	case http.MethodPatch:
		var body struct {
			CodeOwnerApprovalRequired bool `json:"code_owner_approval_required"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b, found := rt.state.GetProtectedBranch(idOrPath, branch)
		if !found {
			b = &gitlab.ProtectedBranch{Name: branch}
		}
		b.CodeOwnerApprovalRequired = body.CodeOwnerApprovalRequired
		rt.state.ProtectBranch(idOrPath, b)
		_ = json.NewEncoder(w).Encode(b)
	case http.MethodDelete:
		if ok := rt.state.UnprotectBranch(idOrPath, branch); ok {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, `{"message":"404 Protected Branch Not Found"}`, http.StatusNotFound)
		}
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleProjectApprovals(w http.ResponseWriter, r *http.Request, idOrPath string) {
	switch r.Method {
	case http.MethodGet:
		app := rt.state.GetProjectApprovals(idOrPath)
		if app == nil {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(app)
	case http.MethodPost:
		var app gitlab.ProjectApprovals
		if err := json.NewDecoder(r.Body).Decode(&app); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		if ok := rt.state.SetProjectApprovals(idOrPath, &app); !ok {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&app)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleProjectApprovalRules(w http.ResponseWriter, r *http.Request, idOrPath, ruleIDStr string) {
	if ruleIDStr == "" {
		switch r.Method {
		case http.MethodGet:
			rules := rt.state.ListApprovalRules(idOrPath)
			rt.paginate(w, r, len(rules), func(i int) any { return rules[i] })
		case http.MethodPost:
			var body struct {
				Name              string `json:"name"`
				ApprovalsRequired int    `json:"approvals_required"`
				RuleType          string `json:"rule_type"`
				UserIDs           []int  `json:"user_ids"`
				GroupIDs          []int  `json:"group_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			rule := gitlab.ProjectApprovalRule{
				Name:              body.Name,
				ApprovalsRequired: body.ApprovalsRequired,
				RuleType:          body.RuleType,
			}
			if rule.RuleType == "" {
				rule.RuleType = "regular"
			}
			for _, uid := range body.UserIDs {
				if u, found := rt.state.GetUser(uid); found {
					rule.Users = append(rule.Users, &gitlab.BasicUser{
						ID:       u.ID,
						Username: u.Username,
						Name:     u.Name,
					})
				}
			}
			for _, gid := range body.GroupIDs {
				if g, found := rt.state.GetGroup(gid); found {
					rule.Groups = append(rule.Groups, g)
				}
			}
			saved, ok := rt.state.AddApprovalRule(idOrPath, &rule)
			if !ok {
				http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(saved)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	ruleID, err := strconv.Atoi(ruleIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid rule ID"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		var updatedUsers []*gitlab.BasicUser
		hasUsers := false
		if uids, ok := body["user_ids"].([]any); ok {
			hasUsers = true
			for _, uidRaw := range uids {
				if uidFloat, ok := uidRaw.(float64); ok {
					if u, found := rt.state.GetUser(int(uidFloat)); found {
						updatedUsers = append(updatedUsers, &gitlab.BasicUser{
							ID:       u.ID,
							Username: u.Username,
							Name:     u.Name,
						})
					}
				}
			}
		}
		var updatedGroups []*gitlab.Group
		hasGroups := false
		if gids, ok := body["group_ids"].([]any); ok {
			hasGroups = true
			for _, gidRaw := range gids {
				if gidFloat, ok := gidRaw.(float64); ok {
					if g, found := rt.state.GetGroup(int(gidFloat)); found {
						updatedGroups = append(updatedGroups, g)
					}
				}
			}
		}

		rule, ok := rt.state.UpdateApprovalRule(idOrPath, ruleID, func(r *gitlab.ProjectApprovalRule) {
			if v, ok := body["name"].(string); ok {
				r.Name = v
			}
			if v, ok := body["approvals_required"].(float64); ok {
				r.ApprovalsRequired = int(v)
			}
			if hasUsers {
				r.Users = updatedUsers
			}
			if hasGroups {
				r.Groups = updatedGroups
			}
		})
		if !ok {
			http.Error(w, `{"message":"404 Rule Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(rule)
	case http.MethodDelete:
		if ok := rt.state.DeleteApprovalRule(idOrPath, ruleID); ok {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, `{"message":"404 Rule Not Found"}`, http.StatusNotFound)
		}
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleProjectVariables(w http.ResponseWriter, r *http.Request, idOrPath, varKey string) {
	if varKey == "" {
		switch r.Method {
		case http.MethodGet:
			vars := rt.state.ListProjectVariables(idOrPath)
			rt.paginate(w, r, len(vars), func(i int) any { return vars[i] })
		case http.MethodPost:
			var pv gitlab.ProjectVariable
			if err := json.NewDecoder(r.Body).Decode(&pv); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			if ok := rt.state.SetProjectVariable(idOrPath, &pv); !ok {
				http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(&pv)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	envScope := r.URL.Query().Get("filter[environment_scope]")
	switch r.Method {
	case http.MethodPut:
		var pv gitlab.ProjectVariable
		if err := json.NewDecoder(r.Body).Decode(&pv); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		pv.Key = varKey
		if pv.EnvironmentScope == "" && envScope != "" {
			pv.EnvironmentScope = envScope
		}
		if ok := rt.state.SetProjectVariable(idOrPath, &pv); !ok {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&pv)
	case http.MethodDelete:
		if ok := rt.state.RemoveProjectVariable(idOrPath, varKey, envScope); ok {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, `{"message":"404 Variable Not Found"}`, http.StatusNotFound)
		}
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleProjectRunners(w http.ResponseWriter, r *http.Request, idOrPath string) {
	if r.Method == http.MethodGet {
		runners := rt.state.ListRunners()
		rt.paginate(w, r, len(runners), func(i int) any { return runners[i] })
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func (rt *Router) handleProjectHooks(w http.ResponseWriter, r *http.Request, idOrPath, hookIDStr string) {
	if hookIDStr == "" {
		switch r.Method {
		case http.MethodGet:
			hooks := rt.state.ListProjectHooks(idOrPath)
			rt.paginate(w, r, len(hooks), func(i int) any { return hooks[i] })
		case http.MethodPost:
			var hook gitlab.ProjectHook
			if err := json.NewDecoder(r.Body).Decode(&hook); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			saved, ok := rt.state.AddProjectHook(idOrPath, &hook)
			if !ok {
				http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(saved)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	hookID, err := strconv.Atoi(hookIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid hook ID"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		hook, ok := rt.state.EditProjectHook(idOrPath, hookID, func(h *gitlab.ProjectHook) {
			if v, ok := body["url"].(string); ok {
				h.URL = v
			}
			if v, ok := body["push_events"].(bool); ok {
				h.PushEvents = v
			}
			if v, ok := body["tag_push_events"].(bool); ok {
				h.TagPushEvents = v
			}
			if v, ok := body["merge_requests_events"].(bool); ok {
				h.MergeRequestsEvents = v
			}
			if v, ok := body["issues_events"].(bool); ok {
				h.IssuesEvents = v
			}
			if v, ok := body["note_events"].(bool); ok {
				h.NoteEvents = v
			}
			if v, ok := body["job_events"].(bool); ok {
				h.JobEvents = v
			}
			if v, ok := body["pipeline_events"].(bool); ok {
				h.PipelineEvents = v
			}
			if v, ok := body["wiki_page_events"].(bool); ok {
				h.WikiPageEvents = v
			}
			if v, ok := body["deployment_events"].(bool); ok {
				h.DeploymentEvents = v
			}
			if v, ok := body["releases_events"].(bool); ok {
				h.ReleasesEvents = v
			}
			if v, ok := body["enable_ssl_verification"].(bool); ok {
				h.EnableSSLVerification = v
			}
			if v, ok := body["push_events_branch_filter"].(string); ok {
				h.PushEventsBranchFilter = v
			}
		})
		if !ok {
			http.Error(w, `{"message":"404 Hook Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(hook)
	case http.MethodDelete:
		if ok := rt.state.DeleteProjectHook(idOrPath, hookID); ok {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, `{"message":"404 Hook Not Found"}`, http.StatusNotFound)
		}
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleProjectMembers(w http.ResponseWriter, r *http.Request, idOrPath string) {
	if r.Method == http.MethodGet {
		members := rt.state.ListProjectMembers(idOrPath)
		rt.paginate(w, r, len(members), func(i int) any { return members[i] })
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ----------------------------------------------------------------------------
// Groups Routing
// ----------------------------------------------------------------------------

func (rt *Router) routeGroups(w http.ResponseWriter, r *http.Request, sub string) {
	if sub == "" || sub == "/" {
		if r.Method == http.MethodGet {
			rt.handleListGroups(w, r)
			return
		}
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.Trim(sub, "/"), "/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}

	// Check for trailing sub-resources
	lastPart := parts[len(parts)-1]
	secondLastPart := ""
	if len(parts) >= 2 {
		secondLastPart = parts[len(parts)-2]
	}

	if secondLastPart == "variables" {
		groupID := strings.Join(parts[:len(parts)-2], "/")
		rt.handleGroupVariables(w, r, groupID, lastPart)
		return
	}
	if secondLastPart == "members" && lastPart == "all" {
		groupID := strings.Join(parts[:len(parts)-2], "/")
		rt.handleGroupMembers(w, r, groupID)
		return
	}

	switch lastPart {
	case "subgroups":
		groupID := strings.Join(parts[:len(parts)-1], "/")
		rt.handleListSubgroups(w, r, groupID)
		return
	case "projects":
		groupID := strings.Join(parts[:len(parts)-1], "/")
		rt.handleListGroupProjects(w, r, groupID)
		return
	case "push_rule":
		groupID := strings.Join(parts[:len(parts)-1], "/")
		rt.handleGroupPushRule(w, r, groupID)
		return
	case "variables":
		groupID := strings.Join(parts[:len(parts)-1], "/")
		rt.handleGroupVariables(w, r, groupID, "")
		return
	case "runners":
		groupID := strings.Join(parts[:len(parts)-1], "/")
		rt.handleGroupRunners(w, r, groupID)
		return
	case "members":
		groupID := strings.Join(parts[:len(parts)-1], "/")
		rt.handleGroupMembers(w, r, groupID)
		return
	default:
		// Entire path is the group ID or path
		fullID := strings.Trim(sub, "/")
		unescapedID, err := url.PathUnescape(fullID)
		if err != nil {
			unescapedID = fullID
		}
		if r.Method == http.MethodGet {
			rt.handleGetGroup(w, r, unescapedID)
			return
		}
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
}

func (rt *Router) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups := rt.state.ListGroups()

	if topLevel := r.URL.Query().Get("top_level_only"); topLevel == "true" || topLevel == "1" {
		topGroups := make([]*gitlab.Group, 0)
		for _, g := range groups {
			if g.ParentID == 0 {
				topGroups = append(topGroups, g)
			}
		}
		groups = topGroups
	}

	if search := r.URL.Query().Get("search"); search != "" {
		sLower := strings.ToLower(search)
		filtered := make([]*gitlab.Group, 0)
		for _, g := range groups {
			if strings.Contains(strings.ToLower(g.Name), sLower) || strings.Contains(strings.ToLower(g.FullPath), sLower) {
				filtered = append(filtered, g)
			}
		}
		groups = filtered
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].ID < groups[j].ID
	})

	rt.paginate(w, r, len(groups), func(i int) any { return groups[i] })
}

func (rt *Router) handleGetGroup(w http.ResponseWriter, r *http.Request, idOrPath string) {
	g, found := rt.state.GetGroup(idOrPath)
	if !found {
		http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(g)
}

func (rt *Router) handleListSubgroups(w http.ResponseWriter, r *http.Request, idOrPath string) {
	subgroups := rt.state.ListSubgroups(idOrPath)
	sort.Slice(subgroups, func(i, j int) bool {
		return subgroups[i].ID < subgroups[j].ID
	})
	rt.paginate(w, r, len(subgroups), func(i int) any { return subgroups[i] })
}

func (rt *Router) handleListGroupProjects(w http.ResponseWriter, r *http.Request, idOrPath string) {
	projects := rt.state.ListGroupProjects(idOrPath)
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].ID < projects[j].ID
	})
	rt.paginate(w, r, len(projects), func(i int) any { return projects[i] })
}

func (rt *Router) handleGroupPushRule(w http.ResponseWriter, r *http.Request, idOrPath string) {
	switch r.Method {
	case http.MethodGet:
		pr, found := rt.state.GetGroupPushRule(idOrPath)
		if !found {
			http.Error(w, `{"message":"404 Group Push Rule Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(pr)
	case http.MethodPost, http.MethodPut:
		var pr gitlab.GroupPushRules
		if err := json.NewDecoder(r.Body).Decode(&pr); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		saved, ok := rt.state.SetGroupPushRule(idOrPath, &pr)
		if !ok {
			http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(saved)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleGroupVariables(w http.ResponseWriter, r *http.Request, idOrPath, varKey string) {
	if varKey == "" {
		switch r.Method {
		case http.MethodGet:
			vars := rt.state.ListGroupVariables(idOrPath)
			rt.paginate(w, r, len(vars), func(i int) any { return vars[i] })
		case http.MethodPost:
			var gv gitlab.GroupVariable
			if err := json.NewDecoder(r.Body).Decode(&gv); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			if ok := rt.state.SetGroupVariable(idOrPath, &gv); !ok {
				http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(&gv)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	envScope := r.URL.Query().Get("filter[environment_scope]")
	switch r.Method {
	case http.MethodPut:
		var gv gitlab.GroupVariable
		if err := json.NewDecoder(r.Body).Decode(&gv); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		gv.Key = varKey
		if gv.EnvironmentScope == "" && envScope != "" {
			gv.EnvironmentScope = envScope
		}
		if ok := rt.state.SetGroupVariable(idOrPath, &gv); !ok {
			http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(&gv)
	case http.MethodDelete:
		if ok := rt.state.RemoveGroupVariable(idOrPath, varKey, envScope); ok {
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, `{"message":"404 Variable Not Found"}`, http.StatusNotFound)
		}
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleGroupRunners(w http.ResponseWriter, r *http.Request, idOrPath string) {
	if r.Method == http.MethodGet {
		runners := rt.state.ListRunners()
		rt.paginate(w, r, len(runners), func(i int) any { return runners[i] })
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func (rt *Router) handleGroupMembers(w http.ResponseWriter, r *http.Request, idOrPath string) {
	if r.Method == http.MethodGet {
		members := rt.state.ListGroupMembers(idOrPath)
		rt.paginate(w, r, len(members), func(i int) any { return members[i] })
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ----------------------------------------------------------------------------
// Runners Routing
// ----------------------------------------------------------------------------

func (rt *Router) routeRunners(w http.ResponseWriter, r *http.Request, sub string) {
	parts := strings.Split(strings.TrimPrefix(sub, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	runnerID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, `{"error":"invalid runner ID"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		d, found := rt.state.GetRunnerDetails(runnerID)
		if !found {
			http.Error(w, `{"message":"404 Runner Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	case http.MethodPut:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		d, ok := rt.state.UpdateRunnerDetails(runnerID, func(rd *gitlab.RunnerDetails) {
			if v, ok := body["description"].(string); ok {
				rd.Description = v
			}
			if v, ok := body["paused"].(bool); ok {
				rd.Paused = v
			}
			if v, ok := body["locked"].(bool); ok {
				rd.Locked = v
			}
			if v, ok := body["access_level"].(string); ok {
				rd.AccessLevel = v
			}
			if tags, ok := body["tag_list"].([]any); ok {
				tagList := make([]string, 0, len(tags))
				for _, t := range tags {
					if s, ok := t.(string); ok {
						tagList = append(tagList, s)
					}
				}
				rd.TagList = tagList
			}
		})
		if !ok {
			http.Error(w, `{"message":"404 Runner Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ----------------------------------------------------------------------------
// Users Routing
// ----------------------------------------------------------------------------

func (rt *Router) routeUsers(w http.ResponseWriter, r *http.Request, sub string) {
	if sub == "" || sub == "/" {
		if r.Method == http.MethodGet {
			if username := r.URL.Query().Get("username"); username != "" {
				u, found := rt.state.GetUserByUsername(username)
				if found {
					_ = json.NewEncoder(w).Encode([]*gitlab.User{u})
				} else {
					_ = json.NewEncoder(w).Encode([]*gitlab.User{})
				}
				return
			}
			users := rt.state.ListUsers()
			rt.paginate(w, r, len(users), func(i int) any { return users[i] })
			return
		}
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(sub, "/"), "/")
	userID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, `{"error":"invalid user ID"}`, http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodGet {
		u, found := rt.state.GetUser(userID)
		if !found {
			http.Error(w, `{"message":"404 User Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(u)
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ----------------------------------------------------------------------------
// GraphQL Routing
// ----------------------------------------------------------------------------

func (rt *Router) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"read error"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal(bodyBytes, &req)

	// Check if mutation or query
	if strings.Contains(req.Query, "projectSetComplianceFramework") {
		// e.g. projectId: "gid://gitlab/Project/123", complianceFrameworkId: "gid://.../1"
		projectID := extractNumericID(req.Query, "Project/")
		frameworkID := extractStringBetween(req.Query, `complianceFrameworkId: "`, `"`)

		if strings.Contains(req.Query, "complianceFrameworkId: null") || frameworkID == "" {
			rt.state.RemoveComplianceFramework(projectID)
		} else {
			name := frameworkID
			if strings.HasPrefix(frameworkID, "gid://") {
				parts := strings.Split(frameworkID, "/")
				name = "Framework-" + parts[len(parts)-1]
			}
			rt.state.SetComplianceFramework(projectID, MockComplianceFramework{
				ID:   frameworkID,
				Name: name,
			})
		}
		_, _ = w.Write([]byte(`{"data":{"projectSetComplianceFramework":{"clientMutationId":null,"errors":[]}}}`))
		return
	}

	// Compliance Frameworks Query
	if strings.Contains(req.Query, "complianceFrameworks") {
		projectID := extractNumericID(req.Query, `fullPath: "`)
		frameworks := rt.state.GetComplianceFrameworks(projectID)

		res := map[string]any{
			"data": map[string]any{
				"project": map[string]any{
					"complianceFrameworks": map[string]any{
						"nodes": frameworks,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(res)
		return
	}

	// Generic success for other GraphQL queries
	_, _ = w.Write([]byte(`{"data":{}}`))
}

func extractNumericID(s, prefix string) int {
	idx := strings.Index(s, prefix)
	if idx == -1 {
		return 0
	}
	rest := s[idx+len(prefix):]
	numStr := ""
	for _, c := range rest {
		if c >= '0' && c <= '9' {
			numStr += string(c)
		} else if len(numStr) > 0 {
			break
		}
	}
	id, _ := strconv.Atoi(numStr)
	return id
}

func extractStringBetween(s, start, end string) string {
	idx1 := strings.Index(s, start)
	if idx1 == -1 {
		return ""
	}
	rest := s[idx1+len(start):]
	idx2 := strings.Index(rest, end)
	if idx2 == -1 {
		return ""
	}
	return rest[:idx2]
}

// ----------------------------------------------------------------------------
// Keyset and Page Pagination Response Helpers
// ----------------------------------------------------------------------------

func (rt *Router) paginate(w http.ResponseWriter, r *http.Request, totalCount int, itemGetter func(int) any) {
	q := r.URL.Query()

	perPage := 20
	if ppStr := q.Get("per_page"); ppStr != "" {
		if pp, err := strconv.Atoi(ppStr); err == nil && pp > 0 {
			perPage = pp
		}
	}

	isKeyset := q.Get("pagination") == "keyset"

	if isKeyset {
		idAfter := 0
		if idStr := q.Get("id_after"); idStr != "" {
			idAfter, _ = strconv.Atoi(idStr)
		} else if idStr := q.Get("page_token"); idStr != "" {
			idAfter, _ = strconv.Atoi(idStr)
		}

		// Find start index (first item with ID > idAfter)
		startIdx := 0
		for startIdx < totalCount {
			item := itemGetter(startIdx)
			itemID := getItemID(item)
			if itemID > idAfter {
				break
			}
			startIdx++
		}

		endIdx := startIdx + perPage
		if endIdx > totalCount {
			endIdx = totalCount
		}

		slice := make([]any, 0, endIdx-startIdx)
		for i := startIdx; i < endIdx; i++ {
			slice = append(slice, itemGetter(i))
		}

		// Keyset link header if more items exist
		if endIdx < totalCount && len(slice) > 0 {
			lastItem := slice[len(slice)-1]
			lastID := getItemID(lastItem)

			nextURL := fmt.Sprintf("%s%s?id_after=%d&page_token=%d&pagination=keyset&per_page=%d", r.Host, r.URL.Path, lastID, lastID, perPage)
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				nextURL = "https://" + nextURL
			} else {
				nextURL = "http://" + nextURL
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
		}

		_ = json.NewEncoder(w).Encode(slice)
		return
	}

	// Standard Page-based pagination
	page := 1
	if pStr := q.Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}

	totalPages := (totalCount + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}

	startIdx := (page - 1) * perPage
	endIdx := startIdx + perPage
	if startIdx > totalCount {
		startIdx = totalCount
	}
	if endIdx > totalCount {
		endIdx = totalCount
	}

	slice := make([]any, 0, endIdx-startIdx)
	for i := startIdx; i < endIdx; i++ {
		slice = append(slice, itemGetter(i))
	}

	w.Header().Set("X-Page", fmt.Sprintf("%d", page))
	w.Header().Set("X-Per-Page", fmt.Sprintf("%d", perPage))
	w.Header().Set("X-Total", fmt.Sprintf("%d", totalCount))
	w.Header().Set("X-Total-Pages", fmt.Sprintf("%d", totalPages))
	if page < totalPages {
		w.Header().Set("X-Next-Page", fmt.Sprintf("%d", page+1))
	}
	if page > 1 {
		w.Header().Set("X-Prev-Page", fmt.Sprintf("%d", page-1))
	}

	_ = json.NewEncoder(w).Encode(slice)
}

func getItemID(item any) int {
	switch v := item.(type) {
	case *gitlab.Project:
		return v.ID
	case *gitlab.Group:
		return v.ID
	case *gitlab.Runner:
		return v.ID
	case *gitlab.User:
		return v.ID
	case *gitlab.ProjectApprovalRule:
		return v.ID
	case *gitlab.ProjectHook:
		return v.ID
	case *gitlab.ProjectMember:
		return v.ID
	case *gitlab.GroupMember:
		return v.ID
	default:
		return 0
	}
}
