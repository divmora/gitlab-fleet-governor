package mockserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type deployState struct {
	nextID int64
	keys   map[int64][]*gitlab.ProjectDeployKey // by project ID
	tokens map[string][]*gitlab.DeployToken     // by "project:<id>" or "group:<id>"
}

func (s *State) deployLocked() *deployState {
	if s.deploy == nil {
		s.deploy = &deployState{nextID: 1000, keys: map[int64][]*gitlab.ProjectDeployKey{}, tokens: map[string][]*gitlab.DeployToken{}}
	}
	return s.deploy
}

// AddDeployKey seeds a deploy key on a project and returns it.
func (s *State) AddDeployKey(projectID int64, k gitlab.ProjectDeployKey) *gitlab.ProjectDeployKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.deployLocked()
	if k.ID == 0 {
		d.nextID++
		k.ID = d.nextID
	}
	if k.CreatedAt == nil {
		now := time.Now()
		k.CreatedAt = &now
	}
	d.keys[projectID] = append(d.keys[projectID], &k)
	cp := k
	return &cp
}

// DeployKeys returns a copy of a project's deploy keys, sorted by ID.
func (s *State) DeployKeys(projectID int64) []gitlab.ProjectDeployKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gitlab.ProjectDeployKey
	for _, k := range s.deployLocked().keys[projectID] {
		out = append(out, *k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AddDeployToken seeds a deploy token on "project" or "group" ownerID.
func (s *State) AddDeployToken(kind string, ownerID int64, t gitlab.DeployToken) *gitlab.DeployToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.deployLocked()
	if t.ID == 0 {
		d.nextID++
		t.ID = d.nextID
	}
	key := kind + ":" + strconv.FormatInt(ownerID, 10)
	d.tokens[key] = append(d.tokens[key], &t)
	cp := t
	return &cp
}

// DeployTokens returns a copy of the deploy tokens of "project" or "group" ownerID.
func (s *State) DeployTokens(kind string, ownerID int64) []gitlab.DeployToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gitlab.DeployToken
	for _, t := range s.deployLocked().tokens[kind+":"+strconv.FormatInt(ownerID, 10)] {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (rt *Router) handleProjectDeployKeys(w http.ResponseWriter, r *http.Request, idOrPath string, rest []string) {
	proj, ok := rt.state.GetProject(idOrPath)
	if !ok {
		http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
		return
	}
	pid := proj.ID

	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			keys := rt.state.DeployKeys(pid)
			rt.paginate(w, r, len(keys), func(i int) any { return keys[i] })
		case http.MethodPost:
			var body gitlab.AddDeployKeyOptions
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == nil || body.Title == nil {
				http.Error(w, `{"message":"400 key and title are required"}`, http.StatusBadRequest)
				return
			}
			k := gitlab.ProjectDeployKey{Title: *body.Title, Key: *body.Key}
			if body.CanPush != nil {
				k.CanPush = *body.CanPush
			}
			if fp, err := fingerprint(*body.Key); err == nil {
				k.FingerprintSHA256 = fp
			}
			created := rt.state.AddDeployKey(pid, k)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(created)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	keyID, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rt.state.mu.Lock()
	keys := rt.state.deployLocked().keys[pid]
	idx := -1
	for i, k := range keys {
		if k.ID == keyID {
			idx = i
		}
	}
	if idx < 0 {
		rt.state.mu.Unlock()
		http.Error(w, `{"message":"404 Deploy Key Not Found"}`, http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		rt.state.deploy.keys[pid] = append(keys[:idx:idx], keys[idx+1:]...)
		rt.state.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPut:
		var body gitlab.UpdateDeployKeyOptions
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			rt.state.mu.Unlock()
			http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
			return
		}
		if body.CanPush != nil {
			keys[idx].CanPush = *body.CanPush
		}
		if body.Title != nil {
			keys[idx].Title = *body.Title
		}
		cp := *keys[idx]
		rt.state.mu.Unlock()
		_ = json.NewEncoder(w).Encode(cp)
	case http.MethodGet:
		cp := *keys[idx]
		rt.state.mu.Unlock()
		_ = json.NewEncoder(w).Encode(cp)
	default:
		rt.state.mu.Unlock()
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (rt *Router) handleDeployTokens(w http.ResponseWriter, r *http.Request, kind, idOrPath string, rest []string) {
	var ownerID int64
	if kind == "group" {
		g, ok := rt.state.GetGroup(idOrPath)
		if !ok {
			http.Error(w, `{"message":"404 Group Not Found"}`, http.StatusNotFound)
			return
		}
		ownerID = g.ID
	} else {
		p, ok := rt.state.GetProject(idOrPath)
		if !ok {
			http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
			return
		}
		ownerID = p.ID
	}

	if len(rest) == 0 {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		var tokens []gitlab.DeployToken
		for _, t := range rt.state.DeployTokens(kind, ownerID) {
			if !t.Revoked {
				tokens = append(tokens, t)
			}
		}
		rt.paginate(w, r, len(tokens), func(i int) any { return tokens[i] })
		return
	}

	tokenID, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil || r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	rt.state.mu.Lock()
	defer rt.state.mu.Unlock()
	for _, t := range rt.state.deployLocked().tokens[kind+":"+strconv.FormatInt(ownerID, 10)] {
		if t.ID == tokenID && !t.Revoked {
			t.Revoked = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.Error(w, `{"message":"404 Deploy Token Not Found"}`, http.StatusNotFound)
}

// fingerprint mirrors config.SSHKeyFingerprint without importing config.
func fingerprint(publicKey string) (string, error) {
	fields := strings.Fields(publicKey)
	if len(fields) < 2 {
		return "", errors.New("invalid key")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}
