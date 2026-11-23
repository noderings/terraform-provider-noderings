package iam

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var (
	identRE          = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	permissionNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*::[A-Za-z][A-Za-z0-9]*$`)
)

// --- noderings_permission ---

var _ datasource.DataSourceWithConfigValidators = &permissionDataSource{}

// NewPermissionDataSource returns the noderings_permission data source.
func NewPermissionDataSource() datasource.DataSource { return &permissionDataSource{} }

type permissionDataSource struct{ client *client.Client }

type permissionModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Service    types.String `tfsdk:"service"`
	Method     types.String `tfsdk:"method"`
	HTTPMethod types.String `tfsdk:"http_method"`
	Scope      types.String `tfsdk:"scope"`
}

func (d *permissionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_permission"
}

func (d *permissionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up one permission, which allows one API method, by its service and method name. Permission IDs differ between deployments, so roles should look them up rather than hard-code them.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Full name, `Service::Method`, for example `DnsZoneService::CreateDnsZone`. Set either this or `service` and `method`.",
				Validators:          []validator.String{stringvalidator.RegexMatches(permissionNameRE, "must look like Service::Method, for example DnsZoneService::CreateDnsZone")},
			},
			"service": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Service name, for example `DnsZoneService` (case-insensitive).",
				Validators:          []validator.String{stringvalidator.RegexMatches(identRE, "must be a service name such as DnsZoneService")},
			},
			"method": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Method name, for example `CreateDnsZone` (case-insensitive).",
				Validators:          []validator.String{stringvalidator.RegexMatches(identRE, "must be a method name such as CreateDnsZone")},
			},
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Permission ID."},
			"http_method": schema.StringAttribute{Computed: true, MarkdownDescription: "HTTP method of the API call, for example `POST`."},
			"scope":       schema.StringAttribute{Computed: true, MarkdownDescription: "Permission scope as reported by the API."},
		},
	}
}

func (d *permissionDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("name"), path.MatchRoot("service")),
		datasourcevalidator.RequiredTogether(path.MatchRoot("service"), path.MatchRoot("method")),
	}
}

func (d *permissionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *permissionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m permissionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	service, method := m.Service.ValueString(), m.Method.ValueString()
	if !m.Name.IsNull() {
		service, method, _ = strings.Cut(m.Name.ValueString(), "::")
	}
	name := service + "::" + method
	perms, err := d.client.ListPermissions(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Error listing permissions", err.Error())
		return
	}
	for _, p := range perms {
		if strings.EqualFold(p.ServiceName, service) && strings.EqualFold(p.MethodName, method) {
			m.ID = types.StringValue(p.ID)
			m.Name = types.StringValue(name)
			m.Service = types.StringValue(service)
			m.Method = types.StringValue(method)
			m.HTTPMethod = types.StringValue(p.HTTPMethod)
			m.Scope = types.StringValue(p.Scope)
			resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
			return
		}
	}
	resp.Diagnostics.AddError("Permission not found",
		fmt.Sprintf("No permission named %s is available to this organization. Check the service and method names; the noderings_permissions data source lists a service's permissions.", name))
}

// --- noderings_permissions ---

// NewPermissionsDataSource returns the noderings_permissions data source.
func NewPermissionsDataSource() datasource.DataSource { return &permissionsDataSource{} }

type permissionsDataSource struct{ client *client.Client }

type permissionsModel struct {
	Services    types.List `tfsdk:"services"`
	IDs         types.Set  `tfsdk:"ids"`
	Permissions types.List `tfsdk:"permissions"`
}

var permissionAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "service": types.StringType, "method": types.StringType,
	"http_method": types.StringType, "scope": types.StringType,
}

func (d *permissionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_permissions"
}

func (d *permissionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists permissions, optionally only those of some services. Use `ids` as a role's `permission_ids` to grant everything a service offers.",
		Attributes: map[string]schema.Attribute{
			"services": schema.ListAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Service names, for example `[\"DnsZoneService\", \"DnsRecordService\"]` (case-insensitive). Every service must match at least one permission. Omit to list every permission.",
				Validators:          []validator.List{listvalidator.SizeAtLeast(1), listvalidator.ValueStringsAre(stringvalidator.RegexMatches(identRE, "must be a service name such as DnsZoneService"))},
			},
			"ids": schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "IDs of the matching permissions."},
			"permissions": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The matching permissions, sorted by name.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Permission ID."},
					"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "`service::method`, with the service name in lower case as the API reports it."},
					"service":     schema.StringAttribute{Computed: true, MarkdownDescription: "Service name in lower case, as the API reports it."},
					"method":      schema.StringAttribute{Computed: true, MarkdownDescription: "Method name."},
					"http_method": schema.StringAttribute{Computed: true, MarkdownDescription: "HTTP method of the API call."},
					"scope":       schema.StringAttribute{Computed: true, MarkdownDescription: "Permission scope."},
				}},
			},
		},
	}
}

func (d *permissionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *permissionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m permissionsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var services []string
	if !m.Services.IsNull() {
		resp.Diagnostics.Append(m.Services.ElementsAs(ctx, &services, false)...)
	}
	perms, err := d.client.ListPermissions(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Error listing permissions", err.Error())
		return
	}
	matched := map[string]bool{}
	var keep []client.Permission
	for _, p := range perms {
		if len(services) == 0 {
			keep = append(keep, p)
			continue
		}
		for _, s := range services {
			if strings.EqualFold(p.ServiceName, s) {
				matched[strings.ToLower(s)] = true
				keep = append(keep, p)
				break
			}
		}
	}
	var missing []string
	for _, s := range services {
		if !matched[strings.ToLower(s)] {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		resp.Diagnostics.AddAttributeError(path.Root("services"), "Unknown service",
			fmt.Sprintf("No permissions match %s. Check the spelling of the service names, for example DnsZoneService.", strings.Join(missing, ", ")))
		return
	}
	sort.Slice(keep, func(i, j int) bool {
		return keep[i].ServiceName+"::"+keep[i].MethodName < keep[j].ServiceName+"::"+keep[j].MethodName
	})
	ids := make([]string, 0, len(keep))
	objs := make([]attr.Value, 0, len(keep))
	for _, p := range keep {
		ids = append(ids, p.ID)
		obj, diags := types.ObjectValue(permissionAttrTypes, map[string]attr.Value{
			"id": types.StringValue(p.ID), "name": types.StringValue(p.ServiceName + "::" + p.MethodName),
			"service": types.StringValue(p.ServiceName), "method": types.StringValue(p.MethodName),
			"http_method": types.StringValue(p.HTTPMethod), "scope": types.StringValue(p.Scope),
		})
		resp.Diagnostics.Append(diags...)
		objs = append(objs, obj)
	}
	m.IDs = stringSet(ctx, ids, &resp.Diagnostics)
	list, diags := types.ListValue(types.ObjectType{AttrTypes: permissionAttrTypes}, objs)
	resp.Diagnostics.Append(diags...)
	m.Permissions = list
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_role (data source) ---

// NewRoleDataSource returns the noderings_role data source.
func NewRoleDataSource() datasource.DataSource { return &roleDataSource{} }

type roleDataSource struct{ client *client.Client }

func (d *roleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (d *roleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a role by name: a custom role or a built-in one (" + strings.Join(builtinRoleNames, ", ") + ").",
		Attributes: map[string]schema.Attribute{
			"name":           schema.StringAttribute{Required: true, MarkdownDescription: "Role name (case-insensitive)."},
			"id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Role ID."},
			"description":    schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
			"builtin":        schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this is a built-in role."},
			"permission_ids": schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "IDs of the permissions the role grants."},
		},
	}
}

func (d *roleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *roleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m roleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roles, err := d.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing roles", err.Error())
		return
	}
	for i := range roles {
		if strings.EqualFold(roles[i].Name, m.Name.ValueString()) {
			role := &roles[i]
			m.ID = types.StringValue(role.ID)
			m.Name = types.StringValue(role.Name)
			m.Description = types.StringValue(role.Description)
			m.Builtin = types.BoolValue(role.IsBuiltin)
			m.PermissionIDs = stringSet(ctx, role.Permissions, &resp.Diagnostics)
			resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
			return
		}
	}
	resp.Diagnostics.AddError("Role not found", fmt.Sprintf("No role named %q exists in the organization.", m.Name.ValueString()))
}

// --- noderings_roles ---

// NewRolesDataSource returns the noderings_roles data source.
func NewRolesDataSource() datasource.DataSource { return &rolesDataSource{} }

type rolesDataSource struct{ client *client.Client }

type rolesModel struct {
	Roles types.List `tfsdk:"roles"`
}

var roleAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "description": types.StringType, "builtin": types.BoolType,
	"permission_ids": types.SetType{ElemType: types.StringType},
}

func (d *rolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_roles"
}

func (d *rolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every role in the organization, built-in and custom.",
		Attributes: map[string]schema.Attribute{
			"roles": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The roles, sorted by name.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Role ID."},
					"name":           schema.StringAttribute{Computed: true, MarkdownDescription: "Name."},
					"description":    schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
					"builtin":        schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this is a built-in role."},
					"permission_ids": schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "IDs of the permissions the role grants."},
				}},
			},
		},
	}
}

func (d *rolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *rolesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	roles, err := d.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing roles", err.Error())
		return
	}
	sort.Slice(roles, func(i, j int) bool { return strings.ToLower(roles[i].Name) < strings.ToLower(roles[j].Name) })
	objs := make([]attr.Value, 0, len(roles))
	for _, role := range roles {
		obj, diags := types.ObjectValue(roleAttrTypes, map[string]attr.Value{
			"id": types.StringValue(role.ID), "name": types.StringValue(role.Name), "description": types.StringValue(role.Description),
			"builtin": types.BoolValue(role.IsBuiltin), "permission_ids": stringSet(ctx, role.Permissions, &resp.Diagnostics),
		})
		resp.Diagnostics.Append(diags...)
		objs = append(objs, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: roleAttrTypes}, objs)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, rolesModel{Roles: list})...)
}

// --- noderings_users ---

// NewUsersDataSource returns the noderings_users data source.
func NewUsersDataSource() datasource.DataSource { return &usersDataSource{} }

type usersDataSource struct{ client *client.Client }

type usersModel struct {
	Emails     types.List `tfsdk:"emails"`
	Users      types.List `tfsdk:"users"`
	IDs        types.List `tfsdk:"ids"`
	IDsByEmail types.Map  `tfsdk:"ids_by_email"`
}

var userAttrTypes = map[string]attr.Type{
	"id": types.StringType, "email": types.StringType, "first_name": types.StringType, "last_name": types.StringType, "status": types.StringType,
}

func (d *usersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *usersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the organization's users, or looks some up by email, so group memberships and role assignments can be written with email addresses.",
		Attributes: map[string]schema.Attribute{
			"emails": schema.ListAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Email addresses to look up (case-insensitive). Every address must belong to a user of the organization. `ids` follows the same order. Omit to list every user.",
				Validators:          []validator.List{listvalidator.ValueStringsAre(stringvalidator.RegexMatches(regexp.MustCompile(`^[^@\s]+@[^@\s]+$`), "must be an email address"))},
			},
			"ids": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "User IDs, in the order of `emails` when it is set, otherwise sorted by email."},
			"ids_by_email": schema.MapAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "User IDs keyed by lower-case email address.",
			},
			"users": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The users.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "User ID."},
					"email":      schema.StringAttribute{Computed: true, MarkdownDescription: "Email address."},
					"first_name": schema.StringAttribute{Computed: true, MarkdownDescription: "First name."},
					"last_name":  schema.StringAttribute{Computed: true, MarkdownDescription: "Last name."},
					"status":     schema.StringAttribute{Computed: true, MarkdownDescription: "Account status, for example `active`."},
				}},
			},
		},
	}
}

func (d *usersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *usersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m usersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	all, err := d.client.ListUsers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing users", err.Error())
		return
	}
	byEmail := map[string]client.User{}
	for _, u := range all {
		byEmail[strings.ToLower(u.Email)] = u
	}
	var users []client.User
	if m.Emails.IsNull() {
		users = all
		sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Email) < strings.ToLower(users[j].Email) })
	} else {
		var emails, missing []string
		resp.Diagnostics.Append(m.Emails.ElementsAs(ctx, &emails, false)...)
		for _, e := range emails {
			u, ok := byEmail[strings.ToLower(e)]
			if !ok {
				missing = append(missing, e)
				continue
			}
			users = append(users, u)
		}
		if len(missing) > 0 {
			resp.Diagnostics.AddAttributeError(path.Root("emails"), "User not found",
				fmt.Sprintf("No user of the organization has the email address %s. Users join an organization by accepting an invite.", strings.Join(missing, ", ")))
			return
		}
	}
	ids := make([]string, 0, len(users))
	idMap := map[string]string{}
	objs := make([]attr.Value, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
		idMap[strings.ToLower(u.Email)] = u.ID
		obj, diags := types.ObjectValue(userAttrTypes, map[string]attr.Value{
			"id": types.StringValue(u.ID), "email": types.StringValue(u.Email), "first_name": types.StringValue(u.FirstName),
			"last_name": types.StringValue(u.LastName), "status": types.StringValue(framework.TrimEnum(u.Status, "STATUS_")),
		})
		resp.Diagnostics.Append(diags...)
		objs = append(objs, obj)
	}
	m.IDs = framework.Strings(ctx, ids, &resp.Diagnostics)
	mv, diags := types.MapValueFrom(ctx, types.StringType, idMap)
	resp.Diagnostics.Append(diags...)
	m.IDsByEmail = mv
	list, diags := types.ListValue(types.ObjectType{AttrTypes: userAttrTypes}, objs)
	resp.Diagnostics.Append(diags...)
	m.Users = list
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
