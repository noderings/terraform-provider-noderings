package client

import (
	"context"
	"net/http"
	"net/url"
)

// Permission is one API method a role can be allowed to call.
type Permission struct {
	ID          string `json:"id"`
	MethodName  string `json:"methodName"`
	HTTPMethod  string `json:"httpMethod"`
	ServiceName string `json:"serviceName"`
	Scope       string `json:"scope"`
}

// UserSummary identifies a user that holds a role.
type UserSummary struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// Role is a named set of permissions.
type Role struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	IsBuiltin      bool          `json:"isBuiltin"`
	OrganizationID string        `json:"organizationId"`
	Permissions    []string      `json:"permissions"`
	GroupIDs       []string      `json:"groupIds"`
	Users          []UserSummary `json:"users"`
	Timestamps     Timestamps    `json:"timestamps"`
}

// Group is a set of users and service accounts that share roles.
type Group struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	GroupRoles  []string   `json:"groupRoles"`
	UserEmails  []string   `json:"userEmails"`
	Status      string     `json:"status"`
	Timestamps  Timestamps `json:"timestamps"`
}

// User is a member of the organization.
type User struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	FirstName  string     `json:"firstName"`
	LastName   string     `json:"lastName"`
	IsVerified bool       `json:"isVerified"`
	UserRoles  []string   `json:"userRoles"`
	GroupIDs   []string   `json:"groupIds"`
	Status     string     `json:"status"`
	Timestamps Timestamps `json:"timestamps"`
}

// ServiceAccount is a non-human principal that authenticates with tokens.
type ServiceAccount struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	IsActive bool     `json:"isActive"`
	GroupIDs []string `json:"groupIds"`
	RoleIDs  []string `json:"roleIds"`
}

// Secret is an organization secret. The API never returns its value.
type Secret struct {
	ID         string     `json:"id"`
	Key        string     `json:"key"`
	Status     string     `json:"status"`
	Timestamps Timestamps `json:"timestamps"`
}

// EnvironmentVariable is an organization environment variable.
type EnvironmentVariable struct {
	ID         string     `json:"id"`
	Key        string     `json:"key"`
	Value      string     `json:"value"`
	Status     string     `json:"status"`
	Timestamps Timestamps `json:"timestamps"`
}

// --- Permissions ---

// ListPermissions returns the permissions visible to the organization.
// filter is an optional case-insensitive substring of "Service::Method".
func (c *Client) ListPermissions(ctx context.Context, filter string) ([]Permission, error) {
	q := pageAll()
	if filter != "" {
		q.Set("filterName", filter)
	}
	var out struct {
		Permissions []Permission `json:"permissions"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListPermissions", path: "/v1/permissions", query: q, retryUnavailable: true}, &out)
	return out.Permissions, err
}

// AssignPermissionsToRole adds permissions to a custom role.
func (c *Client) AssignPermissionsToRole(ctx context.Context, roleID string, ids []string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "AssignPermissionToRole", path: "/v1/permissions/assign-to-role",
		body: map[string]any{"roleId": roleID, "permissionIds": ids}}, nil)
}

// RemovePermissionsFromRole removes permissions from a custom role.
func (c *Client) RemovePermissionsFromRole(ctx context.Context, roleID string, ids []string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "RemovePermissionFromRole", path: "/v1/permissions/remove-from-role",
		query: url.Values{"roleId": {roleID}, "permissionIds": ids}}, nil)
}

// --- Roles ---

// ListRoles returns every role in the organization, built-in ones included.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	var out struct {
		Roles []Role `json:"roles"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListRoles", path: "/v1/roles", query: pageAll(), retryUnavailable: true}, &out)
	return out.Roles, err
}

// GetRole finds a role by ID. The API has no single-role read, so it lists.
func (c *Client) GetRole(ctx context.Context, id string) (*Role, error) {
	roles, err := c.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	for i := range roles {
		if roles[i].ID == id {
			return &roles[i], nil
		}
	}
	return nil, notFoundErr("role not found")
}

// CreateRole creates a custom role.
func (c *Client) CreateRole(ctx context.Context, name, description string, permissionIDs []string) (*Role, error) {
	if permissionIDs == nil {
		permissionIDs = []string{}
	}
	var out struct {
		Role *Role `json:"role"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateRole", path: "/v1/roles",
		body: map[string]any{"name": name, "description": description, "permissions": permissionIDs}}, &out); err != nil {
		return nil, err
	}
	if out.Role == nil || out.Role.ID == "" {
		return nil, &Error{HTTPStatus: http.StatusInternalServerError, Message: "the API did not return the created role"}
	}
	return out.Role, nil
}

// UpdateRole changes a role's name and description. An empty description is ignored by the API.
func (c *Client) UpdateRole(ctx context.Context, id, name, description string) (*Role, error) {
	var out struct {
		Role *Role `json:"role"`
	}
	if err := c.do(ctx, request{method: http.MethodPatch, op: "UpdateRole", path: "/v1/roles/" + url.PathEscape(id),
		body: map[string]any{"name": name, "description": description}}, &out); err != nil {
		return nil, err
	}
	return out.Role, nil
}

// DeleteRole deletes a custom role.
func (c *Client) DeleteRole(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteRole", path: "/v1/roles/" + url.PathEscape(id)}, nil)
}

// Principal kinds accepted by ReplaceRole.
const (
	PrincipalUser           = "user"
	PrincipalGroup          = "group"
	PrincipalServiceAccount = "service_account"
)

// ReplaceRole sets the one direct role of a user, group or service account.
// An empty roleID removes it.
func (c *Client) ReplaceRole(ctx context.Context, kind, principalID, roleID string) error {
	var p, field, op string
	switch kind {
	case PrincipalUser:
		p, field, op = "/v1/roles/replace-for-user", "userId", "ReplaceRoleForUser"
	case PrincipalGroup:
		p, field, op = "/v1/roles/replace-for-group", "groupId", "ReplaceRoleForGroup"
	default:
		p, field, op = "/v1/roles/replace-for-service-account", "serviceAccountId", "ReplaceRoleForServiceAccount"
	}
	return c.do(ctx, request{method: http.MethodPut, op: op, path: p, body: map[string]string{field: principalID, "roleId": roleID}}, nil)
}

// --- Groups ---

// CreateGroup creates a group.
func (c *Client) CreateGroup(ctx context.Context, name, description string) (*Group, error) {
	var out struct {
		Group *Group `json:"group"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateGroup", path: "/v1/groups",
		body: map[string]any{"name": name, "description": description}}, &out); err != nil {
		return nil, err
	}
	if out.Group == nil || out.Group.ID == "" {
		return nil, &Error{HTTPStatus: http.StatusInternalServerError, Message: "the API did not return the created group"}
	}
	return out.Group, nil
}

// GetGroup returns one group.
func (c *Client) GetGroup(ctx context.Context, id string) (*Group, error) {
	var out struct {
		Group *Group `json:"group"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetGroup", path: "/v1/groups/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.Group == nil {
		return nil, notFoundErr("group not found")
	}
	return out.Group, nil
}

// UpdateGroup changes a group's name and description. The API requires the name every time.
func (c *Client) UpdateGroup(ctx context.Context, id, name, description string) (*Group, error) {
	var out struct {
		Group *Group `json:"group"`
	}
	if err := c.do(ctx, request{method: http.MethodPatch, op: "UpdateGroup", path: "/v1/groups/" + url.PathEscape(id),
		body: map[string]any{"name": name, "description": description}}, &out); err != nil {
		return nil, err
	}
	return out.Group, nil
}

// DeleteGroup deletes a group.
func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteGroup", path: "/v1/groups/" + url.PathEscape(id)}, nil)
}

// AddUsersToGroup adds existing users to a group.
func (c *Client) AddUsersToGroup(ctx context.Context, groupID string, userIDs []string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "AddUserToGroup", path: "/v1/groups/add-user",
		body: map[string]any{"groupId": groupID, "userIds": userIDs}}, nil)
}

// RemoveUsersFromGroup removes users from a group.
func (c *Client) RemoveUsersFromGroup(ctx context.Context, groupID string, userIDs []string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "RemoveUserFromGroup", path: "/v1/groups/remove-user",
		query: url.Values{"groupId": {groupID}, "userIds": userIDs}}, nil)
}

// AddServiceAccountsToGroup adds service accounts to a group.
func (c *Client) AddServiceAccountsToGroup(ctx context.Context, groupID string, ids []string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "AddServiceAccountToGroup", path: "/v1/groups/add-service-account",
		body: map[string]any{"groupId": groupID, "serviceAccountIds": ids}}, nil)
}

// RemoveServiceAccountsFromGroup removes service accounts from a group.
func (c *Client) RemoveServiceAccountsFromGroup(ctx context.Context, groupID string, ids []string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "RemoveServiceAccountFromGroup", path: "/v1/groups/remove-service-account",
		query: url.Values{"groupId": {groupID}, "serviceAccountIds": ids}}, nil)
}

// --- Users and service accounts ---

// ListUsers returns the organization's users.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var out struct {
		Users []User `json:"users"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListUsers", path: "/v1/users", query: pageAll(), retryUnavailable: true}, &out)
	return out.Users, err
}

// ListServiceAccounts returns the organization's service accounts.
func (c *Client) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	var out struct {
		ServiceAccounts []ServiceAccount `json:"serviceAccounts"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListServiceAccounts", path: "/v1/service-accounts", query: pageAll(), retryUnavailable: true}, &out)
	return out.ServiceAccounts, err
}

// GetServiceAccount returns one service account.
func (c *Client) GetServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetServiceAccount", path: "/v1/service-accounts/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, notFoundErr("service account not found")
	}
	return &out, nil
}

// --- Secrets ---

// CreateSecret stores a secret.
func (c *Client) CreateSecret(ctx context.Context, key, value string) (*Secret, error) {
	var out struct {
		Secret *Secret `json:"secret"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateSecret", path: "/v1/secrets",
		body: map[string]string{"key": key, "value": value}}, &out); err != nil {
		return nil, err
	}
	if out.Secret == nil || out.Secret.ID == "" {
		return nil, &Error{HTTPStatus: http.StatusInternalServerError, Message: "the API did not return the created secret"}
	}
	return out.Secret, nil
}

// GetSecret returns a secret's metadata (never its value).
func (c *Client) GetSecret(ctx context.Context, id string) (*Secret, error) {
	var out struct {
		Secret *Secret `json:"secret"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetSecret", path: "/v1/secrets/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.Secret == nil {
		return nil, notFoundErr("secret not found")
	}
	return out.Secret, nil
}

// UpdateSecret replaces a secret's value.
func (c *Client) UpdateSecret(ctx context.Context, id, value string) error {
	return c.do(ctx, request{method: http.MethodPatch, op: "UpdateSecret", path: "/v1/secrets/" + url.PathEscape(id),
		body: map[string]string{"value": value}}, nil)
}

// DeleteSecret deletes a secret.
func (c *Client) DeleteSecret(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteSecret", path: "/v1/secrets/" + url.PathEscape(id)}, nil)
}

// --- Environment variables ---

// CreateEnvironmentVariable stores an environment variable.
func (c *Client) CreateEnvironmentVariable(ctx context.Context, key, value string) (*EnvironmentVariable, error) {
	var out struct {
		EnvironmentVariable *EnvironmentVariable `json:"environmentVariable"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateEnvironmentVariable", path: "/v1/environment-variables",
		body: map[string]string{"key": key, "value": value}}, &out); err != nil {
		return nil, err
	}
	if out.EnvironmentVariable == nil || out.EnvironmentVariable.ID == "" {
		return nil, &Error{HTTPStatus: http.StatusInternalServerError, Message: "the API did not return the created environment variable"}
	}
	return out.EnvironmentVariable, nil
}

// GetEnvironmentVariable returns one environment variable.
func (c *Client) GetEnvironmentVariable(ctx context.Context, id string) (*EnvironmentVariable, error) {
	var out struct {
		EnvironmentVariable *EnvironmentVariable `json:"environmentVariable"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetEnvironmentVariable", path: "/v1/environment-variables/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.EnvironmentVariable == nil {
		return nil, notFoundErr("environment variable not found")
	}
	return out.EnvironmentVariable, nil
}

// UpdateEnvironmentVariable replaces a variable's value.
func (c *Client) UpdateEnvironmentVariable(ctx context.Context, id, value string) error {
	return c.do(ctx, request{method: http.MethodPatch, op: "UpdateEnvironmentVariable", path: "/v1/environment-variables/" + url.PathEscape(id),
		body: map[string]string{"value": value}}, nil)
}

// DeleteEnvironmentVariable deletes an environment variable.
func (c *Client) DeleteEnvironmentVariable(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteEnvironmentVariable", path: "/v1/environment-variables/" + url.PathEscape(id)}, nil)
}
