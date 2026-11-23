package iam

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// builtinRoleNames are the roles every organization has. They are read-only.
var builtinRoleNames = []string{"Owner", "Administrator", "Billing", "Operator", "Viewer"}

func isBuiltinName(name string) bool {
	for _, b := range builtinRoleNames {
		if strings.EqualFold(b, name) {
			return true
		}
	}
	return false
}

var (
	_ resource.Resource                   = &roleResource{}
	_ resource.ResourceWithImportState    = &roleResource{}
	_ resource.ResourceWithValidateConfig = &roleResource{}
)

// NewRoleResource returns the noderings_role resource.
func NewRoleResource() resource.Resource { return &roleResource{} }

type roleResource struct{ client *client.Client }

type roleModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	PermissionIDs types.Set    `tfsdk:"permission_ids"`
	Builtin       types.Bool   `tfsdk:"builtin"`
}

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A custom role: a named set of permissions that can be given to users, groups and service accounts with `noderings_role_assignment`. " +
			"Built-in roles (" + strings.Join(builtinRoleNames, ", ") + ") are read-only; look them up with the `noderings_role` data source.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Role ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name, 3 to 255 characters, unique in the organization (case-insensitive).",
				Validators:          []validator.String{stringvalidator.LengthBetween(3, 255), framework.SafeString()},
			},
			"description": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Description. The API cannot clear a description, so removing this argument keeps the current one.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1), framework.SafeString()},
			},
			"permission_ids": schema.SetAttribute{
				Required: true, ElementType: types.StringType,
				MarkdownDescription: "IDs of the permissions the role grants. Look them up with the `noderings_permission` or `noderings_permissions` data sources. " +
					"Permissions added outside Terraform are removed on the next apply.",
				Validators: []validator.Set{setvalidator.ValueStringsAre(framework.UUID())},
			},
			"builtin": schema.BoolAttribute{
				Computed: true, MarkdownDescription: "Always `false` for roles managed by this resource.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *roleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	if !name.IsNull() && !name.IsUnknown() && isBuiltinName(name.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Built-in role name",
			fmt.Sprintf("%q is a built-in role, which cannot be created or changed. Choose another name, or use the noderings_role data source to reference the built-in role.", name.ValueString()))
	}
}

func setStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	var out []string
	if s.IsNull() || s.IsUnknown() {
		return out
	}
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	sort.Strings(out)
	return out
}

func stringSet(ctx context.Context, in []string, diags *diag.Diagnostics) types.Set {
	if in == nil {
		in = []string{}
	}
	s, d := types.SetValueFrom(ctx, types.StringType, in)
	diags.Append(d...)
	return s
}

// difference returns the items of a that are not in b.
func difference(a, b []string) []string {
	seen := make(map[string]bool, len(b))
	for _, x := range b {
		seen[x] = true
	}
	var out []string
	for _, x := range a {
		if !seen[x] {
			out = append(out, x)
		}
	}
	return out
}

func (r *roleResource) setRole(ctx context.Context, m *roleModel, role *client.Role, diags *diag.Diagnostics) {
	m.ID = types.StringValue(role.ID)
	m.Name = types.StringValue(role.Name)
	if role.Description == "" {
		m.Description = types.StringNull()
	} else {
		m.Description = types.StringValue(role.Description)
	}
	m.PermissionIDs = stringSet(ctx, role.Permissions, diags)
	m.Builtin = types.BoolValue(role.IsBuiltin)
}

// syncPermissions makes the role's permissions exactly want. The role PATCH
// only adds permissions, so this uses the assign and remove calls.
func (r *roleResource) syncPermissions(ctx context.Context, roleID string, have, want []string) error {
	if add := difference(want, have); len(add) > 0 {
		if err := r.client.AssignPermissionsToRole(ctx, roleID, add); err != nil {
			return fmt.Errorf("adding permissions: %w", err)
		}
	}
	if remove := difference(have, want); len(remove) > 0 {
		if err := r.client.RemovePermissionsFromRole(ctx, roleID, remove); err != nil {
			return fmt.Errorf("removing permissions: %w", err)
		}
	}
	return nil
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := setStrings(ctx, plan.PermissionIDs, &resp.Diagnostics)
	role, err := r.client.CreateRole(ctx, plan.Name.ValueString(), plan.Description.ValueString(), want)
	if err != nil {
		detail := err.Error()
		if client.IsAlreadyExists(err) {
			detail += "\n\nA role with this name already exists. Choose another name, or import the existing role with: terraform import <address> <role id or name>"
		}
		resp.Diagnostics.AddError("Error creating role", detail)
		return
	}
	// Save the ID first so a failure below leaves the role tracked.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), role.ID)...)
	if err := r.syncPermissions(ctx, role.ID, role.Permissions, want); err != nil {
		resp.Diagnostics.AddError("Error setting role permissions", err.Error())
		return
	}
	if role, err = r.client.GetRole(ctx, role.ID); err != nil {
		resp.Diagnostics.AddError("Error reading role", err.Error())
		return
	}
	r.setRole(ctx, &plan, role, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.GetRole(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading role", err.Error())
		return
	}
	if role.IsBuiltin {
		resp.Diagnostics.AddError("Built-in role", fmt.Sprintf("Role %q is built in and cannot be managed. Remove it from Terraform state (terraform state rm) and reference it with the noderings_role data source instead.", role.Name))
		return
	}
	r.setRole(ctx, &state, role, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if !plan.Name.Equal(state.Name) || (!plan.Description.IsUnknown() && !plan.Description.Equal(state.Description)) {
		if _, err := r.client.UpdateRole(ctx, id, plan.Name.ValueString(), plan.Description.ValueString()); err != nil {
			detail := err.Error()
			if client.IsAlreadyExists(err) {
				detail += "\n\nAnother role already has this name. Choose a different name."
			}
			resp.Diagnostics.AddError("Error updating role", detail)
			return
		}
	}
	want := setStrings(ctx, plan.PermissionIDs, &resp.Diagnostics)
	role, err := r.client.GetRole(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading role", err.Error())
		return
	}
	if err := r.syncPermissions(ctx, id, role.Permissions, want); err != nil {
		resp.Diagnostics.AddError("Error updating role permissions", err.Error())
		return
	}
	if role, err = r.client.GetRole(ctx, id); err != nil {
		resp.Diagnostics.AddError("Error reading role", err.Error())
		return
	}
	r.setRole(ctx, &plan, role, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteRole(ctx, state.ID.ValueString())
	switch {
	case err == nil, client.IsNotFound(err):
	case client.IsFailedPrecondition(err):
		resp.Diagnostics.AddError("Role is still in use",
			fmt.Sprintf("%s\n\nRole %q is still assigned to users or to accepted invites. Remove those assignments first (for example the noderings_role_assignment resources that use it, or in the console), then run terraform destroy again.", err, state.Name.ValueString()))
	default:
		resp.Diagnostics.AddError("Error deleting role", err.Error())
	}
}

// ImportState accepts a role ID or name. Built-in roles are refused.
func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	roles, err := r.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing roles", err.Error())
		return
	}
	for i := range roles {
		role := &roles[i]
		if role.ID != req.ID && !strings.EqualFold(role.Name, req.ID) {
			continue
		}
		if role.IsBuiltin {
			resp.Diagnostics.AddError("Cannot import a built-in role",
				fmt.Sprintf("%q is a built-in role and cannot be managed. Reference it with the noderings_role data source instead.", role.Name))
			return
		}
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), role.ID)...)
		return
	}
	resp.Diagnostics.AddError("Role not found", fmt.Sprintf("No role with ID or name %q exists in the organization.", req.ID))
}
