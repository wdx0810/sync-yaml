package store

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Role represents user role.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// MFASettings holds per-user MFA (TOTP) configuration.
type MFASettings struct {
	Enabled bool   `json:"enabled"`
	Secret  string `json:"secret,omitempty"`
	// Configured indicates whether the user has ever completed MFA setup (i.e. a secret exists).
	// This field is never persisted — it is recomputed on read and exposed to clients so the UI
	// can distinguish "never set up" from "set up but disabled".
	Configured bool `json:"configured,omitempty"`
}

// User represents a system user.
type User struct {
	Username string      `json:"username"`
	Password string      `json:"password"`
	Role     Role        `json:"role"`
	Enabled  bool        `json:"enabled"`
	MFA      MFASettings `json:"mfa"`
}

// TaskPermission represents a user's permission on a specific task.
type TaskPermission struct {
	TaskID string `json:"taskId"`
	CanView bool   `json:"canView"`
	CanSync bool   `json:"canSync"`
	CanEdit bool   `json:"canEdit"`
}

// ProjectPermission represents a user's permission on all tasks in a project.
type ProjectPermission struct {
	Project string `json:"project"`
	CanView bool   `json:"canView"`
	CanSync bool   `json:"canSync"`
	CanEdit bool   `json:"canEdit"`
}

// UserPermissions groups all permissions for a user.
type UserPermissions struct {
	Username           string              `json:"username"`
	Permissions        []TaskPermission    `json:"permissions"`
	ProjectPermissions []ProjectPermission `json:"projectPermissions"`
}

// UserStore manages users persistence.
type UserStore interface {
	ListUsers() ([]User, error)
	GetUser(username string) (*User, error)
	GetUserWithSecrets(username string) (*User, error)
	CreateUser(user *User) error
	UpdateUser(username string, user *User) error
	DeleteUser(username string) error
	Authenticate(username, password string) (*User, error)

	// MFA (per-user)
	SetUserMFA(username string, mfa MFASettings) error

	// Permission management
	GetUserPermissions(username string) (*UserPermissions, error)
	SetTaskPermission(username string, perm TaskPermission) error
	RemoveTaskPermission(username, taskID string) error
	SetProjectPermission(username string, perm ProjectPermission) error
	RemoveProjectPermission(username, project string) error
	// CanAccessTask returns whether the user can perform an action on the task.
	// Permissions are resolved in order: task-level → project-level.
	CanAccessTask(username, taskID, project, action string) (bool, error)
}

type userStore struct {
	users       []User
	permissions map[string]*UserPermissions // username -> permissions
	storagePath string
	mu          sync.RWMutex
	logger      *slog.Logger
}

// NewUserStore creates a new UserStore.
func NewUserStore(storagePath string) UserStore {
	s := &userStore{
		storagePath: storagePath,
		permissions: make(map[string]*UserPermissions),
		logger:      slog.Default().With("component", "user-store"),
	}
	s.load()
	return s
}

func (s *userStore) usersFilePath() string {
	return filepath.Join(s.storagePath, "users.json")
}

func (s *userStore) permissionsFilePath() string {
	return filepath.Join(s.storagePath, "permissions.json")
}

func (s *userStore) load() {
	// Load users
	data, err := os.ReadFile(s.usersFilePath())
	if err == nil {
		var users []User
		if err := json.Unmarshal(data, &users); err != nil {
			s.logger.Warn("failed to parse users.json", "error", err)
		} else {
			s.users = users
		}
	}

	// Ensure admin user exists
	hasAdmin := false
	for _, u := range s.users {
		if u.Role == RoleAdmin {
			hasAdmin = true
			break
		}
	}
	if !hasAdmin {
		s.users = append(s.users, User{
			Username: "admin",
			Password: "admin123",
			Role:     RoleAdmin,
			Enabled:  true,
		})
		s.saveUsers()
	}

	// Load permissions
	pdata, err := os.ReadFile(s.permissionsFilePath())
	if err == nil {
		var perms map[string]*UserPermissions
		if err := json.Unmarshal(pdata, &perms); err != nil {
			s.logger.Warn("failed to parse permissions.json", "error", err)
		} else {
			s.permissions = perms
		}
	}
}

func (s *userStore) saveUsers() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	// Ensure the derived Configured flag is never persisted.
	for i := range s.users {
		s.users[i].MFA.Configured = false
	}
	data, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.usersFilePath(), data, 0600)
}

func (s *userStore) savePermissions() error {
	if err := os.MkdirAll(s.storagePath, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.permissions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.permissionsFilePath(), data, 0600)
}

func (s *userStore) ListUsers() ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]User, len(s.users))
	copy(result, s.users)
	// Don't return passwords or raw secrets; expose Configured flag instead.
	for i := range result {
		result[i].Password = ""
		result[i].MFA.Configured = result[i].MFA.Secret != ""
		result[i].MFA.Secret = ""
	}
	return result, nil
}

func (s *userStore) GetUser(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Username == username {
			out := u
			out.Password = ""
			out.MFA.Configured = out.MFA.Secret != ""
			out.MFA.Secret = ""
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "user", Name: username}
}

// GetUserWithSecrets returns the raw user record including password and MFA secret.
// Used internally by auth flows; never expose to HTTP handlers directly.
func (s *userStore) GetUserWithSecrets(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.Username == username {
			out := u
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "user", Name: username}
}

// SetUserMFA updates the MFA settings for a user.
func (s *userStore) SetUserMFA(username string, mfa MFASettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		if s.users[i].Username == username {
			s.users[i].MFA = mfa
			return s.saveUsers()
		}
	}
	return &ErrNotFound{Entity: "user", Name: username}
}

func (s *userStore) CreateUser(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.users {
		if existing.Username == user.Username {
			return &ErrDuplicate{Entity: "user", Name: user.Username}
		}
	}

	u := *user
	if u.Role == "" {
		u.Role = RoleUser
	}
	if u.Enabled {
		u.Enabled = true
	}
	s.users = append(s.users, u)
	return s.saveUsers()
}

func (s *userStore) UpdateUser(username string, user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.users {
		if existing.Username == username {
			u := *user
			u.Username = username // prevent renaming
			// Keep existing password if not provided
			if u.Password == "" {
				u.Password = existing.Password
			}
			// Preserve existing MFA (only SetUserMFA modifies it).
			u.MFA = existing.MFA
			s.users[i] = u
			return s.saveUsers()
		}
	}
	return &ErrNotFound{Entity: "user", Name: username}
}

func (s *userStore) DeleteUser(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Prevent deleting the last admin
	adminCount := 0
	for _, u := range s.users {
		if u.Role == RoleAdmin {
			adminCount++
		}
	}

	for i, existing := range s.users {
		if existing.Username == username {
			if existing.Role == RoleAdmin && adminCount <= 1 {
				return &ErrReferenced{Entity: "admin user", Name: username, TaskNames: []string{"cannot delete last admin"}}
			}
			s.users = append(s.users[:i], s.users[i+1:]...)
			delete(s.permissions, username)
			s.saveUsers()
			s.savePermissions()
			return nil
		}
	}
	return &ErrNotFound{Entity: "user", Name: username}
}

func (s *userStore) Authenticate(username, password string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, u := range s.users {
		if u.Username == username && u.Password == password {
			if !u.Enabled {
				return nil, &ErrNotFound{Entity: "user (disabled)", Name: username}
			}
			out := u
			out.Password = ""
			out.MFA.Secret = ""
			return &out, nil
		}
	}
	return nil, &ErrNotFound{Entity: "user (invalid credentials)", Name: username}
}

// Permission methods

func (s *userStore) GetUserPermissions(username string) (*UserPermissions, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check user exists
	userExists := false
	for _, u := range s.users {
		if u.Username == username {
			userExists = true
			break
		}
	}
	if !userExists {
		return nil, &ErrNotFound{Entity: "user", Name: username}
	}

	perms, ok := s.permissions[username]
	if !ok {
		return &UserPermissions{
			Username:           username,
			Permissions:        []TaskPermission{},
			ProjectPermissions: []ProjectPermission{},
		}, nil
	}
	// Ensure non-nil slices in JSON response.
	out := *perms
	if out.Permissions == nil {
		out.Permissions = []TaskPermission{}
	}
	if out.ProjectPermissions == nil {
		out.ProjectPermissions = []ProjectPermission{}
	}
	return &out, nil
}

func (s *userStore) SetTaskPermission(username string, perm TaskPermission) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check user exists
	userExists := false
	for _, u := range s.users {
		if u.Username == username {
			userExists = true
			break
		}
	}
	if !userExists {
		return &ErrNotFound{Entity: "user", Name: username}
	}

	perms, ok := s.permissions[username]
	if !ok {
		perms = &UserPermissions{Username: username, Permissions: []TaskPermission{}}
		s.permissions[username] = perms
	}

	// Update or add permission
	found := false
	for i, p := range perms.Permissions {
		if p.TaskID == perm.TaskID {
			perms.Permissions[i] = perm
			found = true
			break
		}
	}
	if !found {
		perms.Permissions = append(perms.Permissions, perm)
	}

	return s.savePermissions()
}

func (s *userStore) RemoveTaskPermission(username, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	perms, ok := s.permissions[username]
	if !ok {
		return nil
	}

	for i, p := range perms.Permissions {
		if p.TaskID == taskID {
			perms.Permissions = append(perms.Permissions[:i], perms.Permissions[i+1:]...)
			break
		}
	}

	return s.savePermissions()
}

// SetProjectPermission adds or updates a project-level permission.
func (s *userStore) SetProjectPermission(username string, perm ProjectPermission) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	userExists := false
	for _, u := range s.users {
		if u.Username == username {
			userExists = true
			break
		}
	}
	if !userExists {
		return &ErrNotFound{Entity: "user", Name: username}
	}

	perms, ok := s.permissions[username]
	if !ok {
		perms = &UserPermissions{Username: username}
		s.permissions[username] = perms
	}

	found := false
	for i, p := range perms.ProjectPermissions {
		if p.Project == perm.Project {
			perms.ProjectPermissions[i] = perm
			found = true
			break
		}
	}
	if !found {
		perms.ProjectPermissions = append(perms.ProjectPermissions, perm)
	}
	return s.savePermissions()
}

// RemoveProjectPermission removes a project-level permission.
func (s *userStore) RemoveProjectPermission(username, project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	perms, ok := s.permissions[username]
	if !ok {
		return nil
	}
	for i, p := range perms.ProjectPermissions {
		if p.Project == project {
			perms.ProjectPermissions = append(perms.ProjectPermissions[:i], perms.ProjectPermissions[i+1:]...)
			break
		}
	}
	return s.savePermissions()
}

// CanAccessTask returns whether the user can perform `action` on the given task.
// Resolution order: task-level permission first, then project-level permission.
// `project` may be empty — in that case only task-level permission is consulted.
func (s *userStore) CanAccessTask(username, taskID, project, action string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check user exists and is enabled.
	var user *User
	for i, u := range s.users {
		if u.Username == username {
			user = &s.users[i]
			break
		}
	}
	if user == nil {
		return false, &ErrNotFound{Entity: "user", Name: username}
	}
	if !user.Enabled {
		return false, nil
	}

	// Admin has full access.
	if user.Role == RoleAdmin {
		return true, nil
	}

	perms, ok := s.permissions[username]
	if !ok {
		return false, nil
	}

	check := func(canView, canSync, canEdit bool) bool {
		switch action {
		case "view":
			return canView
		case "sync":
			return canSync
		case "edit":
			return canEdit
		default:
			return canView
		}
	}

	// Task-level first.
	for _, p := range perms.Permissions {
		if p.TaskID == taskID {
			return check(p.CanView, p.CanSync, p.CanEdit), nil
		}
	}

	// Project-level fallback.
	if project != "" {
		for _, p := range perms.ProjectPermissions {
			if p.Project == project {
				return check(p.CanView, p.CanSync, p.CanEdit), nil
			}
		}
	}

	return false, nil
}
