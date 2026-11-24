package iam

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var (
	_ resource.Resource                     = &roleAssignmentResource{}
	_ resource.ResourceWithImportState      = &roleAssignmentResource{}
	_ resource.ResourceWithConfigValidators = &roleAssignmentResource{}
)

// NewRoleAssignmentResource returns the noderings_role_assignment resource.
func NewRoleAssignmentResource() resource.Resource { return &roleAssignmentResource{} }

type roleAssignmentResource struct{ client *client.Client }

type roleAssignmentModel struct {
	ID               types.String `tfsdk:"id"`
	UserID           types.String `tfsdk:"user_id"`
	GroupID          types.String `tfsdk:"group_id"`
	ServiceAccountID types.String `tfsdk:"service_account_id"`
	RoleID           types.String `tfsdk:"role_id"`
}

// principal returns the principal kind and ID.
func (m *roleAssignmentModel) principal() (string, string) {
	switch {
	case !m.UserID.IsNull():
		return client.PrincipalUser, m.UserID.ValueString()
	case !m.GroupID.IsNull():
		return client.PrincipalGroup, m.GroupID.ValueString()
	default:
		return client.PrincipalServiceAccount, m.ServiceAccountID.ValueString()
	}
}

func (r *roleAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_assignment"
}

func (r *roleAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	principal := func(what string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "ID of the " + what + " that gets the role. Exactly one of `user_id`, `group_id` and `service_account_id` must be set. Changing it replaces the assignment.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{framework.UUID()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Gives a user, group or service account its role in the organization. " +
			"Each principal has exactly one direct role, so applying this resource replaces any role the principal had, " +
			"and destroying it leaves the principal with no direct role. Use one assignment per principal.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`user/<id>`, `group/<id>` or `service_account/<id>`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"user_id":            principal("user"),
			"group_id":           principal("group"),
			"service_account_id": principal("service account"),
			"role_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the role, custom or built-in. Changing it switches the principal to the new role in place.",
				Validators:          []validator.String{framework.UUID()},
			},
		},
	}
}

func (r *roleAssignmentResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("user_id"), path.MatchRoot("group_id"), path.MatchRoot("service_account_id")),
	}
}

func (r *roleAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func assignmentHint(kind string, err error) string {
	detail := err.Error()
	if client.IsNotFound(err) {
		detail += fmt.Sprintf("\n\nCheck that the role and the %s exist in this organization.", strings.ReplaceAll(kind, "_", " "))
	}
	return detail
}

func (r *roleAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	kind, id := plan.principal()
	if err := r.client.ReplaceRole(ctx, kind, id, plan.RoleID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error assigning role", assignmentHint(kind, err))
		return
	}
	plan.ID = types.StringValue(kind + "/" + id)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// currentRoles returns the principal's direct roles in the organization.
// found is false when the principal no longer exists.
func (r *roleAssignmentResource) currentRoles(ctx context.Context, kind, id string) (roles []string, found bool, err error) {
	switch kind {
	case client.PrincipalGroup:
		g, err := r.client.GetGroup(ctx, id)
		if client.IsNotFound(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return g.GroupRoles, true, nil
	case client.PrincipalServiceAccount:
		sa, err := r.client.GetServiceAccount(ctx, id)
		if client.IsNotFound(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		// A service account lists roles from every organization; keep this one's.
		orgRoles, err := r.client.ListRoles(ctx)
		if err != nil {
			return nil, false, err
		}
		inOrg := map[string]bool{}
		for _, role := range orgRoles {
			inOrg[role.ID] = true
		}
		for _, rid := range sa.RoleIDs {
			if inOrg[rid] {
				roles = append(roles, rid)
			}
		}
		return roles, true, nil
	default:
		orgRoles, err := r.client.ListRoles(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, role := range orgRoles {
			for _, u := range role.Users {
				if u.ID == id {
					roles = append(roles, role.ID)
				}
			}
		}
		return roles, true, nil
	}
}

func (r *roleAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state roleAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	kind, id := state.principal()
	roles, found, err := r.currentRoles(ctx, kind, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading role assignment", err.Error())
		return
	}
	if !found || len(roles) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	current := roles[0]
	for _, rid := range roles {
		if rid == state.RoleID.ValueString() {
			current = rid
		}
	}
	state.RoleID = types.StringValue(current)
	state.ID = types.StringValue(kind + "/" + id)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *roleAssignmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan roleAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	kind, id := plan.principal()
	if err := r.client.ReplaceRole(ctx, kind, id, plan.RoleID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error changing role", assignmentHint(kind, err))
		return
	}
	plan.ID = types.StringValue(kind + "/" + id)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete clears the principal's direct role.
func (r *roleAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	kind, id := state.principal()
	if err := r.client.ReplaceRole(ctx, kind, id, ""); err != nil && !client.IsNotFound(err) {
		detail := err.Error()
		if client.IsFailedPrecondition(err) {
			detail += "\n\nThe API refused to remove this role, for example because it would leave the organization without an owner. Assign another role instead of destroying this assignment."
		}
		resp.Diagnostics.AddError("Error removing role", detail)
	}
}

// ImportState takes user/<id>, group/<id> or service_account/<id>.
func (r *roleAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	kind, id, ok := strings.Cut(req.ID, "/")
	attr := map[string]string{client.PrincipalUser: "user_id", client.PrincipalGroup: "group_id", client.PrincipalServiceAccount: "service_account_id"}[kind]
	if !ok || attr == "" || id == "" {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected user/<id>, group/<id> or service_account/<id>, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attr), id)...)
}
