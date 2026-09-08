package audit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	gl "github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// UserRegistry manages thread-safe discovery, caching, and display formatting of all users across the fleet.
type UserRegistry struct {
	mu         sync.RWMutex
	users      map[int]*UserInfo
	client     gl.GitLabClient
	classifier *BotClassifier
}

// NewUserRegistry creates an empty UserRegistry.
func NewUserRegistry(client gl.GitLabClient, classifier ...*BotClassifier) *UserRegistry {
	var c *BotClassifier
	if len(classifier) > 0 {
		c = classifier[0]
	}
	return &UserRegistry{
		users:      make(map[int]*UserInfo),
		client:     client,
		classifier: c,
	}
}

// SetClassifier configures the BotClassifier for user categorization.
func (r *UserRegistry) SetClassifier(c *BotClassifier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.classifier = c
}

// Register adds or updates a user in the registry.
func (r *UserRegistry) Register(userID int, username, name, email, state, webURL string, projectPath string) *UserInfo {
	if userID <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	u, exists := r.users[userID]
	if !exists {
		var isBot bool
		if r.classifier != nil {
			isBot = r.classifier.IsBot(userID, username, name, email)
		} else {
			isBot = IsBotOrServiceAccount(username, name)
		}
		acctType := "Human"
		if isBot {
			acctType = "Service Account / Bot"
		}
		u = &UserInfo{
			ID:          userID,
			Username:    username,
			Name:        name,
			Email:       email,
			State:       state,
			WebURL:      webURL,
			IsBot:       isBot,
			AccountType: acctType,
		}
		r.users[userID] = u
	} else {
		if u.Username == "" && username != "" {
			u.Username = username
		}
		if u.Name == "" && name != "" {
			u.Name = name
		}
		if u.Email == "" && email != "" {
			u.Email = email
		}
		if u.State == "" && state != "" {
			u.State = state
		}
		if u.WebURL == "" && webURL != "" {
			u.WebURL = webURL
		}
	}

	if projectPath != "" {
		found := false
		for _, p := range u.ProjectPaths {
			if p == projectPath {
				found = true
				break
			}
		}
		if !found {
			u.ProjectPaths = append(u.ProjectPaths, projectPath)
			u.ProjectsCount = len(u.ProjectPaths)
		}
	}

	return u
}

// Resolve looks up a user by numeric ID, optionally fetching from the GitLab API if uncached.
func (r *UserRegistry) Resolve(ctx context.Context, userID int) *UserInfo {
	if userID <= 0 {
		return nil
	}
	r.mu.RLock()
	u, exists := r.users[userID]
	r.mu.RUnlock()
	if exists && u.Username != "" {
		return u
	}

	// Fetch from GitLab API if client is available
	if r.client != nil {
		user, resp, err := r.client.Users().GetUser(userID, gitlab.GetUsersOptions{}, gitlab.WithContext(ctx))
		if err == nil && user != nil && resp != nil && resp.StatusCode == 200 {
			return r.Register(user.ID, user.Username, user.Name, user.Email, user.State, user.WebURL, "")
		}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.users[userID]
}

// FormatUser formats a user into a readable string: "@username (Name) [ID: X]" or "User ID X".
func (r *UserRegistry) FormatUser(ctx context.Context, userID int) string {
	if userID <= 0 {
		return ""
	}
	u := r.Resolve(ctx, userID)
	if u != nil && u.Username != "" {
		if u.Name != "" && u.Name != u.Username {
			return fmt.Sprintf("@%s (%s) [ID: %d]", u.Username, u.Name, userID)
		}
		return fmt.Sprintf("@%s [ID: %d]", u.Username, userID)
	}
	return fmt.Sprintf("User ID %d", userID)
}

// AllUsers returns a sorted slice of all unique users registered across the fleet.
func (r *UserRegistry) AllUsers() []*UserInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]*UserInfo, 0, len(r.users))
	for _, u := range r.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Username == list[j].Username {
			return list[i].ID < list[j].ID
		}
		return strings.ToLower(list[i].Username) < strings.ToLower(list[j].Username)
	})
	return list
}
