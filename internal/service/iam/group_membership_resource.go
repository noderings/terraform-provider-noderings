package iam

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                     = &groupMembershipResource{}
	_ resource.ResourceWithImportState      = &groupMembershipResource{}
	_ resource.ResourceWithConfigValidators = &groupMembershipResource{}
)

// NewGroupMembershipResource returns the noderings_group_membership resource.
func NewGroupMembershipResource() resource.Resource { return &groupMembershipResource{} }

type groupMembershipResource struct{ client *client.Client }

type groupMembershipModel struct {
	ID                types.String `tfsdk:"id"`
	GroupID           types.String `tfsdk:"group_id"`
	UserIDs           types.Set    `tfsdk:"user_ids"`
	ServiceAccountIDs types.Set    `tfsdk:"service_account_ids"`
}

func (r *groupMembershipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_membership"
}

func (r *groupMembershipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	uuids := []validator.Set{setvalidator.ValueStringsAre(framework.UUID())}
	resp.Schema = schema.Schema{
		MarkdownDescription: "The members of one group. This resource is authoritative for each member type it sets: " +
			"when `user_ids` is set, users not in it are removed from the group, and likewise for `service_account_ids`. " +
			"A member type left unset is not managed. Use only one `noderings_group_membership` per group.\n\n" +
			"Users must already belong to the organization (they join through invites). Find their IDs with the `noderings_users` data source.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Same as `group_id`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"group_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the group. Changing it replaces the resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{framework.UUID()},
			},
			"user_ids": schema.SetAttribute{
				Optional: true, ElementType: types.StringType, Validators: uuids,
				MarkdownDescription: "IDs of the users in the group. An empty set removes every user.",
			},
			"service_account_ids": schema.SetAttribute{
				Optional: true, ElementType: types.StringType, Validators: uuids,
				MarkdownDescription: "IDs of the service accounts in the group. An empty set removes every service account.",
			},
		},
	}
}

func (r *groupMembershipResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.AtLeastOneOf(path.MatchRoot("user_ids"), path.MatchRoot("service_account_ids")),
	}
}

func (r *groupMembershipResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

// currentUsers returns the IDs of the group's users. The API lists members by
// email, so they are resolved through the organization's users.
func (r *groupMembershipResource) currentUsers(ctx context.Context, g *client.Group, diags *diag.Diagnostics) ([]string, error) {
	if len(g.UserEmails) == 0 {
		return []string{}, nil
	}
	users, err := r.client.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	byEmail := make(map[string]string, len(users))
	for _, u := range users {
		byEmail[strings.ToLower(u.Email)] = u.ID
	}
	ids := []string{}
	for _, e := range g.UserEmails {
		if id, ok := byEmail[strings.ToLower(e)]; ok {
			ids = append(ids, id)
		} else {
			diags.AddWarning("Group member not found", fmt.Sprintf("Group %q lists member %s, who is not among the organization's users. Terraform cannot manage this member.", g.Name, e))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// currentServiceAccounts returns the IDs of the group's service accounts.
// Groups do not list them, so each service account's groups are checked.
func (r *groupMembershipResource) currentServiceAccounts(ctx context.Context, groupID string) ([]string, error) {
	sas, err := r.client.ListServiceAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing service accounts: %w", err)
	}
	ids := []string{}
	for _, sa := range sas {
		for _, g := range sa.GroupIDs {
			if g == groupID {
				ids = append(ids, sa.ID)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// reconcile makes the group's members of each managed type match the plan.
func (r *groupMembershipResource) reconcile(ctx context.Context, m *groupMembershipModel, diags *diag.Diagnostics) {
	groupID := m.GroupID.ValueString()
	g, err := r.client.GetGroup(ctx, groupID)
	if err != nil {
		if client.IsNotFound(err) {
			diags.AddError("Group not found", fmt.Sprintf("Group %s does not exist. Check group_id.", groupID))
		} else {
			diags.AddError("Error reading group", err.Error())
		}
		return
	}
	if !m.UserIDs.IsNull() {
		want := setStrings(ctx, m.UserIDs, diags)
		have, err := r.currentUsers(ctx, g, diags)
		if err != nil {
			diags.AddError("Error reading group members", err.Error())
			return
		}
		if add := difference(want, have); len(add) > 0 {
			if err := r.client.AddUsersToGroup(ctx, groupID, add); err != nil {
				detail := err.Error()
				if client.IsNotFound(err) {
					detail += "\n\nEvery user must already belong to the organization. Look up user IDs with the noderings_users data source."
				}
				diags.AddError("Error adding users to group", detail)
				return
			}
		}
		if remove := difference(have, want); len(remove) > 0 {
			if err := r.client.RemoveUsersFromGroup(ctx, groupID, remove); err != nil {
				diags.AddError("Error removing users from group", err.Error())
				return
			}
		}
	}
	if !m.ServiceAccountIDs.IsNull() {
		want := setStrings(ctx, m.ServiceAccountIDs, diags)
		have, err := r.currentServiceAccounts(ctx, groupID)
		if err != nil {
			diags.AddError("Error reading group members", err.Error())
			return
		}
		if add := difference(want, have); len(add) > 0 {
			if err := r.client.AddServiceAccountsToGroup(ctx, groupID, add); err != nil {
				diags.AddError("Error adding service accounts to group", err.Error())
				return
			}
		}
		if remove := difference(have, want); len(remove) > 0 {
			if err := r.client.RemoveServiceAccountsFromGroup(ctx, groupID, remove); err != nil {
				diags.AddError("Error removing service accounts from group", err.Error())
				return
			}
		}
	}
	m.ID = m.GroupID
}

func (r *groupMembershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupMembershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.reconcile(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupMembershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state groupMembershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGroup(ctx, state.GroupID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading group", err.Error())
		return
	}
	if !state.UserIDs.IsNull() {
		ids, err := r.currentUsers(ctx, g, &resp.Diagnostics)
		if err != nil {
			resp.Diagnostics.AddError("Error reading group members", err.Error())
			return
		}
		state.UserIDs = stringSet(ctx, ids, &resp.Diagnostics)
	}
	if !state.ServiceAccountIDs.IsNull() {
		ids, err := r.currentServiceAccounts(ctx, g.ID)
		if err != nil {
			resp.Diagnostics.AddError("Error reading group members", err.Error())
			return
		}
		state.ServiceAccountIDs = stringSet(ctx, ids, &resp.Diagnostics)
	}
	state.ID = types.StringValue(g.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *groupMembershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan groupMembershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.reconcile(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete removes the members this resource manages.
func (r *groupMembershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupMembershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID := state.GroupID.ValueString()
	if ids := setStrings(ctx, state.UserIDs, &resp.Diagnostics); len(ids) > 0 {
		if err := r.client.RemoveUsersFromGroup(ctx, groupID, ids); err != nil && !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Error removing users from group", err.Error())
			return
		}
	}
	if ids := setStrings(ctx, state.ServiceAccountIDs, &resp.Diagnostics); len(ids) > 0 {
		if err := r.client.RemoveServiceAccountsFromGroup(ctx, groupID, ids); err != nil && !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Error removing service accounts from group", err.Error())
		}
	}
}

// ImportState takes a group ID. Both member types become managed.
func (r *groupMembershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	empty := types.SetValueMust(types.StringType, nil)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_ids"), empty)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_account_ids"), empty)...)
}
