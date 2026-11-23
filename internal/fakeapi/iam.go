package fakeapi

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Fixed users and service accounts in the fake organization. Users join an
// organization through invites, which Terraform does not manage, so tests use
// these.
const (
	UserAliceID    = "1a2b0000-0000-4000-8000-000000000001"
	UserAliceEmail = "alice@example.com"
	UserBobID      = "1a2b0000-0000-4000-8000-000000000002"
	UserBobEmail   = "bob@example.com"
	UserCarolID    = "1a2b0000-0000-4000-8000-000000000003"
	UserCarolEmail = "carol@example.com"

	ServiceAccountCIID     = "2b3c0000-0000-4000-8000-000000000001"
	ServiceAccountDeployID = "2b3c0000-0000-4000-8000-000000000002"
)

// Built-in role IDs in the fake organization.
const (
	RoleOwnerID         = "3c4d0000-0000-4000-8000-000000000001"
	RoleAdministratorID = "3c4d0000-0000-4000-8000-000000000002"
	RoleBillingID       = "3c4d0000-0000-4000-8000-000000000003"
	RoleOperatorID      = "3c4d0000-0000-4000-8000-000000000004"
	RoleViewerID        = "3c4d0000-0000-4000-8000-000000000005"
)

// fakePermissions is a small fixed catalogue: service name → methods.
var fakePermissions = []struct {
	service string
	methods []string
}{
	{"DnsZoneService", []string{"CreateDnsZone", "GetDnsZone", "ListDnsZones", "DeleteDnsZone"}},
	{"DnsRecordService", []string{"ListDnsRecords", "UpsertDnsRecord", "DeleteDnsRecord"}},
	{"DnssecService", []string{"GetDnssec", "EnableDnssec", "DisableDnssec"}},
	{"SSHKeyService", []string{"CreateSSHKey", "GetSSHKey", "ListSSHKeys", "DeleteSSHKey"}},
	{"RoleService", []string{"CreateRole", "ListRoles", "UpdateRole", "DeleteRole"}},
	{"GroupService", []string{"CreateGroup", "GetGroup", "ListGroups", "DeleteGroup"}},
}

type fakePermission struct {
	ID, Service, Method, HTTPMethod string
}

type fakeRole struct {
	ID, Name, Description string
	Builtin               bool
	Perms                 map[string]bool
}

type fakeGroup struct {
	ID, Name, Description string
	Users, SAs            map[string]bool
	Roles                 []string
}

type fakeUser struct{ ID, Email, First, Last string }

type fakeKV struct{ ID, Key, Value string }

type iamState struct {
	perms   []fakePermission
	roles   map[string]*fakeRole
	groups  map[string]*fakeGroup
	users   []fakeUser
	sas     []string
	direct  map[string]string // "user|<id>" → role ID; also "service_account|<id>"
	secrets map[string]*fakeKV
	env     map[string]*fakeKV
}

func iam(s *Server) *iamState {
	return state(s, "iam", func() *iamState {
		st := &iamState{
			roles:   map[string]*fakeRole{},
			groups:  map[string]*fakeGroup{},
			direct:  map[string]string{},
			secrets: map[string]*fakeKV{},
			env:     map[string]*fakeKV{},
			users: []fakeUser{
				{UserAliceID, UserAliceEmail, "Alice", "Example"},
				{UserBobID, UserBobEmail, "Bob", "Example"},
				{UserCarolID, UserCarolEmail, "Carol", "Example"},
			},
			sas: []string{ServiceAccountCIID, ServiceAccountDeployID},
		}
		n := 0
		for _, svc := range fakePermissions {
			for _, m := range svc.methods {
				n++
				hm := http.MethodPost
				switch {
				case strings.HasPrefix(m, "Get"), strings.HasPrefix(m, "List"):
					hm = http.MethodGet
				case strings.HasPrefix(m, "Delete"), strings.HasPrefix(m, "Disable"):
					hm = http.MethodDelete
				}
				st.perms = append(st.perms, fakePermission{ID: fmt.Sprintf("4d5e0000-0000-4000-8000-%012d", n), Service: svc.service, Method: m, HTTPMethod: hm})
			}
		}
		all, reads := map[string]bool{}, map[string]bool{}
		for _, p := range st.perms {
			all[p.ID] = true
			if p.HTTPMethod == http.MethodGet {
				reads[p.ID] = true
			}
		}
		for _, b := range []struct {
			id, name string
			perms    map[string]bool
		}{
			{RoleOwnerID, "Owner", all}, {RoleAdministratorID, "Administrator", all}, {RoleBillingID, "Billing", map[string]bool{}},
			{RoleOperatorID, "Operator", all}, {RoleViewerID, "Viewer", reads},
		} {
			st.roles[b.id] = &fakeRole{ID: b.id, Name: b.name, Description: "Built-in " + b.name + " role", Builtin: true, Perms: b.perms}
		}
		st.direct["user|"+UserAliceID] = RoleOwnerID
		return st
	})
}

// PermissionID returns the fake ID of "Service::Method", or "" if unknown.
func (s *Server) PermissionID(service, method string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range iam(s).perms {
		if p.Service == service && p.Method == method {
			return p.ID
		}
	}
	return ""
}

// DeleteRoleOutOfBand removes a custom role as if someone deleted it in the console.
func (s *Server) DeleteRoleOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	iam(s).deleteRole(id)
}

// AddRolePermissionOutOfBand adds a permission to a role as if someone did it in the console.
func (s *Server) AddRolePermissionOutOfBand(roleID, permissionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := iam(s).roles[roleID]; r != nil {
		r.Perms[permissionID] = true
	}
}

// DeleteGroupOutOfBand removes a group as if someone deleted it in the console.
func (s *Server) DeleteGroupOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(iam(s).groups, id)
}

// RenameGroupOutOfBand renames a group as if someone did it in the console.
func (s *Server) RenameGroupOutOfBand(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := iam(s).groups[id]; g != nil {
		g.Name = name
	}
}

// AddGroupMemberOutOfBand adds a user (or, with serviceAccount, a service account) to a group.
func (s *Server) AddGroupMemberOutOfBand(groupID, principalID string, serviceAccount bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := iam(s).groups[groupID]; g != nil {
		if serviceAccount {
			g.SAs[principalID] = true
		} else {
			g.Users[principalID] = true
		}
	}
}

// SetRoleOutOfBand sets a principal's direct role ("user", "group" or
// "service_account"); an empty roleID removes it.
func (s *Server) SetRoleOutOfBand(kind, principalID, roleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	iam(s).replaceRole(kind, principalID, roleID)
}

// DeleteSecretOutOfBand removes a secret as if someone deleted it in the console.
func (s *Server) DeleteSecretOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(iam(s).secrets, id)
}

// SecretValue returns a secret's stored value, for assertions.
func (s *Server) SecretValue(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := iam(s).secrets[id]; v != nil {
		return v.Value
	}
	return ""
}

// SetEnvironmentVariableOutOfBand changes a variable's value as if someone did it in the console.
func (s *Server) SetEnvironmentVariableOutOfBand(id, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := iam(s).env[id]; v != nil {
		v.Value = value
	}
}

// DeleteEnvironmentVariableOutOfBand removes a variable as if someone deleted it in the console.
func (s *Server) DeleteEnvironmentVariableOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(iam(s).env, id)
}

func init() {
	register(func(s *Server, w http.ResponseWriter, r *http.Request, p []string) bool {
		if len(p) < 2 || p[0] != "v1" {
			return false
		}
		st := iam(s)
		switch p[1] {
		case "permissions":
			st.permissions(w, r, p)
		case "roles":
			st.rolesAPI(s, w, r, p)
		case "groups":
			st.groupsAPI(s, w, r, p)
		case "users":
			st.usersAPI(w, r, p)
		case "service-accounts":
			st.serviceAccountsAPI(w, r, p)
		case "secrets":
			st.kvAPI(s, w, r, p, st.secrets, "secret", "secrets", 255)
		case "environment-variables":
			st.kvAPI(s, w, r, p, st.env, "environmentVariable", "environmentVariables", 32767)
		default:
			return false
		}
		return true
	})
}

// requirePageSize mimics the IAM list endpoints, which reject a missing page size.
func requirePageSize(w http.ResponseWriter, r *http.Request) bool {
	if ps := r.URL.Query().Get("pageSize"); ps == "" || ps == "0" {
		apiError(w, 400, 3, "page_size: value is required")
		return false
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (st *iamState) permView(p fakePermission) map[string]any {
	return map[string]any{"id": p.ID, "methodName": p.Method, "httpMethod": p.HTTPMethod, "serviceName": strings.ToLower(p.Service), "scope": "client"}
}

func (st *iamState) permissions(w http.ResponseWriter, r *http.Request, p []string) {
	switch {
	case len(p) == 2 && r.Method == http.MethodGet:
		if !requirePageSize(w, r) {
			return
		}
		f := strings.ToLower(r.URL.Query().Get("filterName"))
		list := []any{}
		for _, perm := range st.perms {
			if f == "" || strings.Contains(strings.ToLower(perm.Service+"::"+perm.Method), f) {
				list = append(list, st.permView(perm))
			}
		}
		writeJSON(w, 200, map[string]any{"permissions": list, "totalCount": len(list)})
	case len(p) == 3 && p[2] == "assign-to-role" && r.Method == http.MethodPost:
		var in struct {
			RoleID        string   `json:"roleId"`
			PermissionIDs []string `json:"permissionIds"`
		}
		if !decode(w, r, &in) {
			return
		}
		role := st.roles[in.RoleID]
		if role == nil {
			notFound(w, "Role")
			return
		}
		if role.Builtin {
			apiError(w, 400, 3, "Built-in roles cannot be modified.")
			return
		}
		if !st.validPerms(w, in.PermissionIDs) {
			return
		}
		for _, id := range in.PermissionIDs {
			role.Perms[id] = true
		}
		writeJSON(w, 200, map[string]any{"success": true})
	case len(p) == 3 && p[2] == "remove-from-role" && r.Method == http.MethodDelete:
		q := r.URL.Query()
		role := st.roles[q.Get("roleId")]
		if role == nil {
			notFound(w, "Role")
			return
		}
		if role.Builtin {
			apiError(w, 400, 3, "Built-in roles cannot be modified.")
			return
		}
		for _, id := range q["permissionIds"] {
			delete(role.Perms, id)
		}
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (st *iamState) validPerms(w http.ResponseWriter, ids []string) bool {
	for _, id := range ids {
		found := false
		for _, p := range st.perms {
			if p.ID == id {
				found = true
				break
			}
		}
		if !found {
			apiError(w, 400, 3, "Permission "+id+" does not exist.")
			return false
		}
	}
	return true
}

func (st *iamState) roleView(role *fakeRole) map[string]any {
	users := []any{}
	for _, u := range st.users {
		if st.direct["user|"+u.ID] == role.ID {
			users = append(users, map[string]any{"id": u.ID, "email": u.Email, "firstName": u.First, "lastName": u.Last})
		}
	}
	groups := []string{}
	for _, g := range st.sortedGroups() {
		for _, rid := range g.Roles {
			if rid == role.ID {
				groups = append(groups, g.ID)
			}
		}
	}
	org := OrgID
	return map[string]any{"id": role.ID, "name": role.Name, "description": role.Description, "isBuiltin": role.Builtin,
		"organizationId": org, "permissions": sortedKeys(role.Perms), "groupIds": groups, "users": users,
		"metadata": map[string]any{}, "status": "STATUS_ACTIVE", "timestamps": map[string]any{"createTime": now(), "updateTime": now()}}
}

func (st *iamState) nameTaken(name, except string) bool {
	for _, r := range st.roles {
		if r.ID != except && strings.EqualFold(r.Name, name) {
			return true
		}
	}
	return false
}

func (st *iamState) deleteRole(id string) {
	delete(st.roles, id)
	for k, v := range st.direct {
		if v == id {
			delete(st.direct, k)
		}
	}
	for _, g := range st.groups {
		kept := g.Roles[:0]
		for _, rid := range g.Roles {
			if rid != id {
				kept = append(kept, rid)
			}
		}
		g.Roles = kept
	}
}

func (st *iamState) replaceRole(kind, principalID, roleID string) {
	if kind == "group" {
		if g := st.groups[principalID]; g != nil {
			g.Roles = nil
			if roleID != "" {
				g.Roles = []string{roleID}
			}
		}
		return
	}
	if roleID == "" {
		delete(st.direct, kind+"|"+principalID)
	} else {
		st.direct[kind+"|"+principalID] = roleID
	}
}

func (st *iamState) rolesAPI(s *Server, w http.ResponseWriter, r *http.Request, p []string) {
	switch {
	case len(p) == 2 && r.Method == http.MethodGet:
		if !requirePageSize(w, r) {
			return
		}
		f := strings.ToLower(r.URL.Query().Get("filterName"))
		ids := make([]string, 0, len(st.roles))
		for id := range st.roles {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		list := []any{}
		for _, id := range ids {
			if role := st.roles[id]; f == "" || strings.Contains(strings.ToLower(role.Name), f) {
				list = append(list, st.roleView(role))
			}
		}
		writeJSON(w, 200, map[string]any{"roles": list, "totalCount": len(list)})
	case len(p) == 2 && r.Method == http.MethodPost:
		var in struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Permissions []string `json:"permissions"`
		}
		if !decode(w, r, &in) {
			return
		}
		if len(in.Name) < 3 || len(in.Name) > 255 {
			apiError(w, 400, 3, "name: value length must be between 3 and 255 characters")
			return
		}
		if st.nameTaken(in.Name, "") {
			apiError(w, 409, 6, "A role with this name already exists.")
			return
		}
		if !st.validPerms(w, in.Permissions) {
			return
		}
		role := &fakeRole{ID: s.id(), Name: in.Name, Description: in.Description, Perms: map[string]bool{}}
		for _, id := range in.Permissions {
			role.Perms[id] = true
		}
		st.roles[role.ID] = role
		writeJSON(w, 200, map[string]any{"success": true, "role": st.roleView(role)})
	case len(p) == 3 && (p[2] == "replace-for-user" || p[2] == "replace-for-group" || p[2] == "replace-for-service-account") && r.Method == http.MethodPut:
		var in struct {
			RoleID           string `json:"roleId"`
			UserID           string `json:"userId"`
			GroupID          string `json:"groupId"`
			ServiceAccountID string `json:"serviceAccountId"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.RoleID != "" && st.roles[in.RoleID] == nil {
			notFound(w, "Role")
			return
		}
		switch p[2] {
		case "replace-for-user":
			if !st.hasUser(in.UserID) {
				notFound(w, "User")
				return
			}
			st.replaceRole("user", in.UserID, in.RoleID)
		case "replace-for-group":
			if st.groups[in.GroupID] == nil {
				notFound(w, "Group")
				return
			}
			st.replaceRole("group", in.GroupID, in.RoleID)
		default:
			if !st.hasSA(in.ServiceAccountID) {
				notFound(w, "Service account")
				return
			}
			st.replaceRole("service_account", in.ServiceAccountID, in.RoleID)
		}
		writeJSON(w, 200, map[string]any{"success": true})
	case len(p) == 3 && r.Method == http.MethodPatch:
		role := st.roles[p[2]]
		if role == nil {
			notFound(w, "Role")
			return
		}
		if role.Builtin {
			apiError(w, 400, 3, "Built-in roles cannot be modified.")
			return
		}
		var in struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Permissions []string `json:"permissions"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Name != "" {
			if st.nameTaken(in.Name, role.ID) {
				apiError(w, 409, 6, "A role with this name already exists.")
				return
			}
			role.Name = in.Name
		}
		// Like the real API, an empty description is ignored and permissions are only added.
		if in.Description != "" {
			role.Description = in.Description
		}
		if !st.validPerms(w, in.Permissions) {
			return
		}
		for _, id := range in.Permissions {
			role.Perms[id] = true
		}
		writeJSON(w, 200, map[string]any{"role": st.roleView(role)})
	case len(p) == 3 && r.Method == http.MethodDelete:
		role := st.roles[p[2]]
		if role == nil {
			notFound(w, "Role")
			return
		}
		if role.Builtin {
			apiError(w, 400, 9, "Built-in roles cannot be deleted.")
			return
		}
		for k, v := range st.direct {
			if v == role.ID && strings.HasPrefix(k, "user|") {
				apiError(w, 400, 9, "Role is assigned to users and cannot be deleted.")
				return
			}
		}
		st.deleteRole(role.ID)
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (st *iamState) hasUser(id string) bool {
	for _, u := range st.users {
		if u.ID == id {
			return true
		}
	}
	return false
}

func (st *iamState) hasSA(id string) bool {
	for _, sa := range st.sas {
		if sa == id {
			return true
		}
	}
	return false
}

func (st *iamState) sortedGroups() []*fakeGroup {
	out := make([]*fakeGroup, 0, len(st.groups))
	for _, g := range st.groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (st *iamState) groupView(g *fakeGroup) map[string]any {
	emails := []string{}
	for _, u := range st.users {
		if g.Users[u.ID] {
			emails = append(emails, u.Email)
		}
	}
	roles := append([]string{}, g.Roles...)
	return map[string]any{"id": g.ID, "name": g.Name, "description": g.Description, "groupRoles": roles, "userEmails": emails,
		"metadata": map[string]any{}, "status": "STATUS_ACTIVE", "timestamps": map[string]any{"createTime": now(), "updateTime": now()}}
}

func (st *iamState) groupNameTaken(name, except string) bool {
	for _, g := range st.groups {
		if g.ID != except && strings.EqualFold(g.Name, name) {
			return true
		}
	}
	return false
}

func (st *iamState) groupsAPI(s *Server, w http.ResponseWriter, r *http.Request, p []string) {
	switch {
	case len(p) == 2 && r.Method == http.MethodGet:
		if !requirePageSize(w, r) {
			return
		}
		list := []any{}
		for _, g := range st.sortedGroups() {
			list = append(list, st.groupView(g))
		}
		writeJSON(w, 200, map[string]any{"groups": list, "totalCount": len(list)})
	case len(p) == 2 && r.Method == http.MethodPost:
		var in struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decode(w, r, &in) {
			return
		}
		if len(in.Name) < 3 || len(in.Name) > 255 {
			apiError(w, 400, 3, "name: value length must be between 3 and 255 characters")
			return
		}
		if st.groupNameTaken(in.Name, "") {
			apiError(w, 409, 6, "A group with this name already exists.")
			return
		}
		g := &fakeGroup{ID: s.id(), Name: in.Name, Description: in.Description, Users: map[string]bool{}, SAs: map[string]bool{}}
		st.groups[g.ID] = g
		writeJSON(w, 200, map[string]any{"success": true, "group": st.groupView(g)})
	case len(p) == 3 && (p[2] == "add-user" || p[2] == "add-service-account") && r.Method == http.MethodPost:
		var in struct {
			GroupID           string   `json:"groupId"`
			UserIDs           []string `json:"userIds"`
			ServiceAccountIDs []string `json:"serviceAccountIds"`
		}
		if !decode(w, r, &in) {
			return
		}
		g := st.groups[in.GroupID]
		if g == nil {
			notFound(w, "Group")
			return
		}
		if p[2] == "add-user" {
			for _, id := range in.UserIDs {
				if !st.hasUser(id) {
					notFound(w, "User")
					return
				}
			}
			for _, id := range in.UserIDs {
				g.Users[id] = true
			}
		} else {
			for _, id := range in.ServiceAccountIDs {
				if !st.hasSA(id) {
					notFound(w, "Service account")
					return
				}
			}
			for _, id := range in.ServiceAccountIDs {
				g.SAs[id] = true
			}
		}
		writeJSON(w, 200, map[string]any{"success": true})
	case len(p) == 3 && (p[2] == "remove-user" || p[2] == "remove-service-account") && r.Method == http.MethodDelete:
		q := r.URL.Query()
		g := st.groups[q.Get("groupId")]
		if g == nil {
			notFound(w, "Group")
			return
		}
		if p[2] == "remove-user" {
			for _, id := range q["userIds"] {
				delete(g.Users, id)
			}
		} else {
			for _, id := range q["serviceAccountIds"] {
				delete(g.SAs, id)
			}
		}
		writeJSON(w, 200, map[string]any{"success": true})
	case len(p) == 3:
		g := st.groups[p[2]]
		if g == nil {
			notFound(w, "Group")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{"group": st.groupView(g)})
		case http.MethodPatch:
			var in struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				GroupRoles  []string `json:"groupRoles"`
			}
			if !decode(w, r, &in) {
				return
			}
			if len(in.Name) < 3 || len(in.Name) > 255 {
				apiError(w, 400, 3, "name: value length must be between 3 and 255 characters")
				return
			}
			if st.groupNameTaken(in.Name, g.ID) {
				apiError(w, 409, 6, "A group with this name already exists.")
				return
			}
			g.Name = in.Name
			if in.Description != "" {
				g.Description = in.Description
			}
			if len(in.GroupRoles) > 0 {
				g.Roles = in.GroupRoles
			}
			writeJSON(w, 200, map[string]any{"group": st.groupView(g)})
		case http.MethodDelete:
			delete(st.groups, g.ID)
			writeJSON(w, 200, map[string]any{"success": true})
		default:
			apiError(w, 404, 5, "Not Found")
		}
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (st *iamState) usersAPI(w http.ResponseWriter, r *http.Request, p []string) {
	view := func(u fakeUser) map[string]any {
		roles, groups := []string{}, []string{}
		if rid := st.direct["user|"+u.ID]; rid != "" {
			roles = append(roles, rid)
		}
		for _, g := range st.sortedGroups() {
			if g.Users[u.ID] {
				groups = append(groups, g.ID)
			}
		}
		return map[string]any{"id": u.ID, "email": u.Email, "password": "", "firstName": u.First, "lastName": u.Last, "isVerified": true,
			"organization": OrgID, "userRoles": roles, "groupIds": groups, "status": "STATUS_ACTIVE",
			"timestamps": map[string]any{"createTime": now(), "updateTime": now()}}
	}
	switch {
	case len(p) == 2 && r.Method == http.MethodGet:
		if !requirePageSize(w, r) {
			return
		}
		f := strings.ToLower(r.URL.Query().Get("filterEmail"))
		list := []any{}
		for _, u := range st.users {
			if f == "" || strings.Contains(u.Email, f) {
				list = append(list, view(u))
			}
		}
		writeJSON(w, 200, map[string]any{"users": list, "totalCount": len(list)})
	case len(p) == 3 && r.Method == http.MethodGet:
		for _, u := range st.users {
			if u.ID == p[2] {
				writeJSON(w, 200, map[string]any{"user": view(u)})
				return
			}
		}
		notFound(w, "User")
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (st *iamState) serviceAccountsAPI(w http.ResponseWriter, r *http.Request, p []string) {
	view := func(id string) map[string]any {
		roles, groups := []string{}, []string{}
		if rid := st.direct["service_account|"+id]; rid != "" {
			roles = append(roles, rid)
		}
		for _, g := range st.sortedGroups() {
			if g.SAs[id] {
				groups = append(groups, g.ID)
			}
		}
		name := "ci"
		if id == ServiceAccountDeployID {
			name = "deploy"
		}
		return map[string]any{"id": id, "name": name, "description": "", "isActive": true, "allowedIps": []string{}, "organizationId": OrgID,
			"activeTokenCount": 1, "groupIds": groups, "roleIds": roles}
	}
	switch {
	case len(p) == 2 && r.Method == http.MethodGet:
		if !requirePageSize(w, r) {
			return
		}
		list := []any{}
		for _, id := range st.sas {
			list = append(list, view(id))
		}
		writeJSON(w, 200, map[string]any{"serviceAccounts": list, "totalCount": len(list)})
	case len(p) == 3 && r.Method == http.MethodGet:
		if !st.hasSA(p[2]) {
			notFound(w, "Service account")
			return
		}
		writeJSON(w, 200, view(p[2]))
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

var keyStrip = regexp.MustCompile(`[^a-z0-9_-]`)

// kvAPI serves secrets and environment variables, which share their shape.
// Secrets never return their value.
func (st *iamState) kvAPI(s *Server, w http.ResponseWriter, r *http.Request, p []string, store map[string]*fakeKV, one, many string, maxLen int) {
	secret := one == "secret"
	view := func(v *fakeKV) map[string]any {
		out := map[string]any{"id": v.ID, "key": v.Key, "user": "", "organization": OrgID, "metadata": map[string]any{},
			"status": "STATUS_ACTIVE", "timestamps": map[string]any{"createTime": now(), "updateTime": now()}}
		if !secret {
			out["value"] = v.Value
		}
		return out
	}
	switch {
	case len(p) == 2 && r.Method == http.MethodGet:
		if !requirePageSize(w, r) {
			return
		}
		ids := make([]string, 0, len(store))
		for id := range store {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		list := []any{}
		for _, id := range ids {
			list = append(list, view(store[id]))
		}
		writeJSON(w, 200, map[string]any{many: list, "totalCount": len(list)})
	case len(p) == 2 && r.Method == http.MethodPost:
		var in struct{ Key, Value string }
		if !decode(w, r, &in) {
			return
		}
		// The real API lower-cases keys and silently drops characters it does not allow.
		key := keyStrip.ReplaceAllString(strings.ToLower(in.Key), "")
		if !secret {
			key = strings.ReplaceAll(key, "-", "")
		}
		if key == "" {
			apiError(w, 400, 3, "key: value is required")
			return
		}
		if len(in.Value) < 1 || len(in.Value) > maxLen {
			apiError(w, 400, 3, fmt.Sprintf("value: value length must be between 1 and %d characters", maxLen))
			return
		}
		for _, v := range store {
			if v.Key == key {
				apiError(w, 409, 6, "A "+strings.ToLower(one)+" with this key already exists.")
				return
			}
		}
		v := &fakeKV{ID: s.id(), Key: key, Value: in.Value}
		store[v.ID] = v
		resp := map[string]any{one: view(v)}
		if !secret {
			resp["success"] = true
		}
		writeJSON(w, 200, resp)
	case len(p) == 3:
		v := store[p[2]]
		if v == nil {
			notFound(w, one)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{one: view(v)})
		case http.MethodPatch:
			var in struct{ Value string }
			if !decode(w, r, &in) {
				return
			}
			if len(in.Value) > maxLen {
				apiError(w, 400, 3, fmt.Sprintf("value: value length must be at most %d characters", maxLen))
				return
			}
			// An empty value is ignored, as in the real API.
			if in.Value != "" {
				v.Value = in.Value
			}
			writeJSON(w, 200, map[string]any{"success": true})
		case http.MethodDelete:
			delete(store, v.ID)
			writeJSON(w, 200, map[string]any{"success": true})
		default:
			apiError(w, 404, 5, "Not Found")
		}
	default:
		apiError(w, 404, 5, "Not Found")
	}
}
