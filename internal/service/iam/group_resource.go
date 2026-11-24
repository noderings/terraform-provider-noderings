package iam

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                = &groupResource{}
	_ resource.ResourceWithImportState = &groupResource{}
)

// NewGroupResource returns the noderings_group resource.
func NewGroupResource() resource.Resource { return &groupResource{} }

type groupResource struct{ client *client.Client }

type groupModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A group of users and service accounts. Manage its members with `noderings_group_membership` and its role with `noderings_role_assignment`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Group ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name, 3 to 255 characters, unique in the organization (case-insensitive).",
				Validators:          []validator.String{stringvalidator.LengthBetween(3, 255), framework.SafeString()},
			},
			"description": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Description. The API cannot clear a description, so removing this argument keeps the current one.",
				PlanModifiers:       keep,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1), framework.SafeString()},
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func setGroup(m *groupModel, g *client.Group) {
	m.ID = types.StringValue(g.ID)
	m.Name = types.StringValue(g.Name)
	if g.Description == "" {
		m.Description = types.StringNull()
	} else {
		m.Description = types.StringValue(g.Description)
	}
	m.CreatedAt = framework.Time(g.Timestamps.CreateTime)
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.CreateGroup(ctx, plan.Name.ValueString(), plan.Description.ValueString())
	if err != nil {
		detail := err.Error()
		if client.IsAlreadyExists(err) {
			detail += "\n\nA group with this name already exists. Choose another name, or import the existing group with: terraform import <address> <group id>"
		}
		resp.Diagnostics.AddError("Error creating group", detail)
		return
	}
	setGroup(&plan, g)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGroup(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading group", err.Error())
		return
	}
	setGroup(&state, g)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The API requires the name on every update, so it is always sent.
	g, err := r.client.UpdateGroup(ctx, state.ID.ValueString(), plan.Name.ValueString(), plan.Description.ValueString())
	if err != nil {
		detail := err.Error()
		if client.IsAlreadyExists(err) {
			detail += "\n\nAnother group already has this name. Choose a different name."
		}
		resp.Diagnostics.AddError("Error updating group", detail)
		return
	}
	if g == nil || g.ID == "" {
		if g, err = r.client.GetGroup(ctx, state.ID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error reading group", err.Error())
			return
		}
	}
	setGroup(&plan, g)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGroup(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting group", err.Error())
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
