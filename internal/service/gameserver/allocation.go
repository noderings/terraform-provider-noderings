package gameserver

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var (
	_ resource.Resource                = &allocationResource{}
	_ resource.ResourceWithImportState = &allocationResource{}
)

// NewAllocationResource returns the noderings_game_server_allocation resource.
func NewAllocationResource() resource.Resource { return &allocationResource{} }

type allocationResource struct{ subResource }

type allocationModel struct {
	ID           types.String `tfsdk:"id"`
	GameServerID types.String `tfsdk:"game_server_id"`
	AllocationID types.Int64  `tfsdk:"allocation_id"`
	Notes        types.String `tfsdk:"notes"`
	Primary      types.Bool   `tfsdk:"primary"`
	IP           types.String `tfsdk:"ip"`
	Port         types.Int64  `tfsdk:"port"`
	Alias        types.String `tfsdk:"alias"`
}

func (r *allocationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server_allocation"
}

func (r *allocationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An extra port (allocation) for a game server, for example a query or voice port. NodeRings picks the IP and port. " +
			"Extra allocations count against the plan's `allocation_limit`. The server's own primary address is not managed by this resource; " +
			"read it from `noderings_game_server.primary_ip` and `primary_port`. The game server must be ready.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute(),
			"game_server_id": serverIDAttribute(),
			"allocation_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "Allocation ID.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"notes": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				MarkdownDescription: "Free-form notes, for example what the port is for. Changes in place.",
				Validators:          []validator.String{framework.SafeString()},
			},
			"primary": schema.BoolAttribute{
				Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Set to `true` to make this the server's primary address, the one players connect to. " +
					"It cannot be set back to `false` directly: make another allocation primary instead. " +
					"Destroying a primary allocation first makes the server's oldest other allocation primary.",
			},
			"ip":    schema.StringAttribute{Computed: true, MarkdownDescription: "IP address.", PlanModifiers: keep},
			"port":  schema.Int64Attribute{Computed: true, MarkdownDescription: "Port.", PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"alias": schema.StringAttribute{Computed: true, MarkdownDescription: "Host name alias for the IP, if any.", PlanModifiers: keep},
		},
	}
}

func (r *allocationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan allocationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := plan.GameServerID.ValueString()
	before, err := r.client.ListGameServerAllocations(ctx, server)
	if err != nil {
		resp.Diagnostics.AddError("Error creating game server allocation", explainNotReady(ctx, r.client, server, err))
		return
	}
	a, err := r.client.CreateGameServerAllocation(ctx, server)
	invalidate(r.client, server, "allocations")
	if err != nil && client.IsGameServerDeadlineExceeded(err) {
		// The allocation may have been created anyway: adopt a single new one.
		a = adoptAllocation(ctx, r.client, server, before)
		if a != nil {
			err = nil
		}
	}
	if err != nil {
		detail := explainNotReady(ctx, r.client, server, err)
		resp.Diagnostics.AddError("Error creating game server allocation", detail)
		return
	}
	id := a.ID
	plan.ID = types.StringValue(fmt.Sprintf("%s/%d", server, id))
	plan.AllocationID = types.Int64Value(int64(id))
	if plan.Notes.ValueString() != "" {
		if _, err := r.client.UpdateGameServerAllocation(ctx, server, id, plan.Notes.ValueString()); err != nil {
			r.saveCreated(ctx, resp, &plan, a)
			resp.Diagnostics.AddError("Error setting allocation notes", err.Error())
			return
		}
	}
	if plan.Primary.ValueBool() {
		if err := r.client.SetPrimaryGameServerAllocation(ctx, server, id); err != nil {
			r.saveCreated(ctx, resp, &plan, a)
			resp.Diagnostics.AddError("Error making the allocation primary", err.Error())
			return
		}
	}
	invalidate(r.client, server, "allocations")
	if !r.refresh(ctx, &plan, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Allocation not found after creating it", "Allocation "+plan.ID.ValueString()+" was created but does not appear in the server's allocations.")
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// saveCreated keeps a partly configured allocation in state so it is not lost.
func (r *allocationResource) saveCreated(ctx context.Context, resp *resource.CreateResponse, m *allocationModel, a *client.GameServerAllocation) {
	setAllocation(m, a)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func adoptAllocation(ctx context.Context, c *client.Client, server string, before []client.GameServerAllocation) *client.GameServerAllocation {
	after, err := c.ListGameServerAllocations(ctx, server)
	if err != nil {
		return nil
	}
	known := map[int32]bool{}
	for _, a := range before {
		known[a.ID] = true
	}
	var found *client.GameServerAllocation
	for i := range after {
		if !known[after[i].ID] {
			if found != nil {
				return nil
			}
			found = &after[i]
		}
	}
	return found
}

// refresh reads the allocation from the (cached) list. It returns false if
// the allocation is gone.
func (r *allocationResource) refresh(ctx context.Context, m *allocationModel, diags *diag.Diagnostics) bool {
	list, err := listAllocations(ctx, r.client, m.GameServerID.ValueString())
	if err != nil {
		diags.AddError("Error reading game server allocations", err.Error())
		return false
	}
	for i := range list {
		if int64(list[i].ID) == m.AllocationID.ValueInt64() {
			setAllocation(m, &list[i])
			return true
		}
	}
	return false
}

func setAllocation(m *allocationModel, a *client.GameServerAllocation) {
	m.AllocationID = types.Int64Value(int64(a.ID))
	m.ID = types.StringValue(fmt.Sprintf("%s/%d", m.GameServerID.ValueString(), a.ID))
	m.Notes = types.StringValue(a.Notes)
	m.Primary = types.BoolValue(a.IsDefault)
	m.IP = types.StringValue(a.IP)
	m.Port = types.Int64Value(int64(a.Port))
	m.Alias = types.StringValue(a.Alias)
}

func (r *allocationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state allocationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := state.GameServerID.ValueString()
	list, ok := readSub(ctx, r.client, server, "allocations", listAllocations, &resp.Diagnostics, func() { resp.State.RemoveResource(ctx) })
	if !ok {
		return
	}
	for i := range list {
		if int64(list[i].ID) == state.AllocationID.ValueInt64() {
			setAllocation(&state, &list[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *allocationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state allocationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := state.GameServerID.ValueString()
	id := int32(state.AllocationID.ValueInt64())
	if !plan.Primary.IsUnknown() && !plan.Primary.ValueBool() && state.Primary.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("primary"), "Cannot unset the primary allocation",
			"An allocation stops being primary only when another one becomes primary. Set primary = true on another allocation instead.")
		return
	}
	if !plan.Notes.Equal(state.Notes) {
		_, err := r.client.UpdateGameServerAllocation(ctx, server, id, plan.Notes.ValueString())
		invalidate(r.client, server, "allocations")
		if err != nil {
			resp.Diagnostics.AddError("Error updating allocation notes", explainNotReady(ctx, r.client, server, err))
			return
		}
	}
	if plan.Primary.ValueBool() && !state.Primary.ValueBool() {
		err := r.client.SetPrimaryGameServerAllocation(ctx, server, id)
		invalidate(r.client, server, "allocations")
		if err != nil {
			resp.Diagnostics.AddError("Error making the allocation primary", explainNotReady(ctx, r.client, server, err))
			return
		}
	}
	plan.AllocationID = state.AllocationID
	if !r.refresh(ctx, &plan, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Allocation disappeared", "Allocation "+state.ID.ValueString()+" was deleted while it was being updated.")
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *allocationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state allocationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := state.GameServerID.ValueString()
	id := int32(state.AllocationID.ValueInt64())
	// The primary allocation cannot be deleted. Hand the primary role to the
	// server's oldest other allocation first, so destroying works.
	invalidate(r.client, server, "allocations")
	list, st, _, lerr := listOrExplain(ctx, r.client, server, listAllocations)
	if lerr == nil && st == parentGone {
		return
	}
	if lerr == nil && st == parentReady {
		var self, other *client.GameServerAllocation
		for i := range list {
			a := &list[i]
			if a.ID == id {
				self = a
			} else if other == nil || a.ID < other.ID {
				other = a
			}
		}
		if self == nil {
			return
		}
		if self.IsDefault && other != nil {
			if err := r.client.SetPrimaryGameServerAllocation(ctx, server, other.ID); err != nil {
				resp.Diagnostics.AddError("Error moving the primary address before deleting the allocation", explainNotReady(ctx, r.client, server, err))
				return
			}
			resp.Diagnostics.AddWarning("Primary address moved",
				fmt.Sprintf("Allocation %d was the server's primary address. Allocation %d (%s:%d) is now primary so that it could be deleted.", id, other.ID, other.IP, other.Port))
		}
	}
	err := deleteSub(ctx, r.client, server, "allocations",
		func() error { return r.client.DeleteGameServerAllocation(ctx, server, id) },
		listAllocations, func(a client.GameServerAllocation) bool { return a.ID == id })
	if err != nil {
		detail := err.Error()
		if state.Primary.ValueBool() {
			detail += "\n\nThis is the server's primary allocation. Make another allocation primary before deleting it."
		}
		resp.Diagnostics.AddError("Error deleting game server allocation", detail)
	}
}

func (r *allocationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	obj, ok := subImport(ctx, req, resp, "allocation")
	if !ok {
		return
	}
	n, err := strconv.ParseInt(obj, 10, 32)
	if err != nil || n <= 0 {
		resp.Diagnostics.AddError("Invalid import ID", "The allocation ID must be a positive number, got "+strconv.Quote(obj)+".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allocation_id"), n)...)
}
