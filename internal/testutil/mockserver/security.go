package mockserver

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// securityState holds job token scope and security settings per project.
type securityState struct {
	inboundEnabled map[int64]bool
	allowProjects  map[int64]map[int64]bool
	allowGroups    map[int64]map[int64]bool
	pushProtection map[int64]bool
}

func (s *State) securityLocked() *securityState {
	if s.security == nil {
		s.security = &securityState{
			inboundEnabled: map[int64]bool{},
			allowProjects:  map[int64]map[int64]bool{},
			allowGroups:    map[int64]map[int64]bool{},
			pushProtection: map[int64]bool{},
		}
	}
	return s.security
}

// JobTokenInboundEnabled reports whether a project limits job token access.
func (s *State) JobTokenInboundEnabled(projectID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.securityLocked().inboundEnabled[projectID]
}

// SetJobTokenInboundEnabled sets "limit access to this project".
func (s *State) SetJobTokenInboundEnabled(projectID int64, enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.securityLocked().inboundEnabled[projectID] = enabled
}

// AllowJobTokenProject adds target to the project's inbound allowlist.
func (s *State) AllowJobTokenProject(projectID, target int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.securityLocked()
	if sec.allowProjects[projectID] == nil {
		sec.allowProjects[projectID] = map[int64]bool{}
	}
	sec.allowProjects[projectID][target] = true
}

// AllowJobTokenGroup adds target to the project's groups allowlist.
func (s *State) AllowJobTokenGroup(projectID, target int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.securityLocked()
	if sec.allowGroups[projectID] == nil {
		sec.allowGroups[projectID] = map[int64]bool{}
	}
	sec.allowGroups[projectID][target] = true
}

// JobTokenAllowlist returns the allowlisted project and group IDs, sorted.
// Like GitLab, the project list always contains the project itself.
func (s *State) JobTokenAllowlist(projectID int64) (projects, groups []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.securityLocked()
	projects = []int64{projectID}
	for id := range sec.allowProjects[projectID] {
		if id != projectID {
			projects = append(projects, id)
		}
	}
	for id := range sec.allowGroups[projectID] {
		groups = append(groups, id)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i] < projects[j] })
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	return projects, groups
}

// SecretPushProtection reports a project's secret push protection setting.
func (s *State) SecretPushProtection(projectID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.securityLocked().pushProtection[projectID]
}

func (rt *Router) handleJobTokenScope(w http.ResponseWriter, r *http.Request, idOrPath string, rest []string) {
	proj, ok := rt.state.GetProject(idOrPath)
	if !ok {
		http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
		return
	}
	pid := proj.ID

	switch {
	case len(rest) == 0:
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(gitlab.JobTokenAccessSettings{InboundEnabled: rt.state.JobTokenInboundEnabled(pid)})
		case http.MethodPatch:
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
				return
			}
			rt.state.SetJobTokenInboundEnabled(pid, body.Enabled)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}

	case rest[0] == "allowlist" || rest[0] == "groups_allowlist":
		groups := rest[0] == "groups_allowlist"
		if len(rest) == 2 && r.Method == http.MethodDelete {
			target, err := strconv.ParseInt(rest[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if !groups && target == pid {
				http.Error(w, `{"message":"cannot remove the project itself"}`, http.StatusBadRequest)
				return
			}
			rt.state.mu.Lock()
			sec := rt.state.securityLocked()
			if groups {
				delete(sec.allowGroups[pid], target)
			} else {
				delete(sec.allowProjects[pid], target)
			}
			rt.state.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch r.Method {
		case http.MethodGet:
			projectIDs, groupIDs := rt.state.JobTokenAllowlist(pid)
			if groups {
				items := make([]*gitlab.Group, 0, len(groupIDs))
				for _, id := range groupIDs {
					if g, ok := rt.state.GetGroup(id); ok {
						items = append(items, g)
					}
				}
				rt.paginate(w, r, len(items), func(i int) any { return items[i] })
				return
			}
			items := make([]*gitlab.Project, 0, len(projectIDs))
			for _, id := range projectIDs {
				if p, ok := rt.state.GetProject(id); ok {
					items = append(items, p)
				}
			}
			rt.paginate(w, r, len(items), func(i int) any { return items[i] })
		case http.MethodPost:
			var body struct {
				TargetProjectID int64 `json:"target_project_id"`
				TargetGroupID   int64 `json:"target_group_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
				return
			}
			if groups {
				if _, ok := rt.state.GetGroup(body.TargetGroupID); !ok {
					http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
					return
				}
				rt.state.AllowJobTokenGroup(pid, body.TargetGroupID)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(gitlab.JobTokenAllowlistItem{SourceProjectID: pid, TargetGroupID: body.TargetGroupID})
				return
			}
			if _, ok := rt.state.GetProject(body.TargetProjectID); !ok {
				http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
				return
			}
			rt.state.AllowJobTokenProject(pid, body.TargetProjectID)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(gitlab.JobTokenInboundAllowItem{SourceProjectID: pid, TargetProjectID: body.TargetProjectID})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}

	default:
		http.NotFound(w, r)
	}
}

func (rt *Router) handleSecuritySettings(w http.ResponseWriter, r *http.Request, idOrPath string) {
	proj, ok := rt.state.GetProject(idOrPath)
	if !ok {
		http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"secret_push_protection_enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
			http.Error(w, `{"error":"secret_push_protection_enabled is required"}`, http.StatusBadRequest)
			return
		}
		rt.state.mu.Lock()
		rt.state.securityLocked().pushProtection[proj.ID] = *body.Enabled
		rt.state.mu.Unlock()
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(gitlab.ProjectSecuritySettings{ProjectID: proj.ID, SecretPushProtectionEnabled: rt.state.SecretPushProtection(proj.ID)})
}
