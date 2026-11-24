package compute

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = &snapshotResource{}
	_ resource.ResourceWithImportState = &snapshotResource{}
)

// NewSnapshotResource returns the noderings_virtual_machine_snapshot resource.
func NewSnapshotResource() resource.Resource { return &snapshotResource{} }

type snapshotResource struct{ client *client.Client }

type snapshotModel struct {
	ID               types.String `tfsdk:"id"`
	VirtualMachineID types.String `tfsdk:"virtual_machine_id"`
	Description      types.String `tfsdk:"description"`
	SnapshotID       types.String `tfsdk:"snapshot_id"`
	Name             types.String `tfsdk:"name"`
	Status           types.String `tfsdk:"status"`
	OperationStatus  types.String `tfsdk:"operation_status"`
	CreatedAt        types.String `tfsdk:"created_at"`
	Timeouts         types.Object `tfsdk:"timeouts"`
}

func (r *snapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_snapshot"
}

func (r *snapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "**Experimental.** A snapshot of a virtual machine's disk. The NodeRings console does not show snapshots yet, " +
			"so this resource may change in a later release.\n\n" +
			"Only platforms with the `snapshots` capability (Proxmox) support snapshots. Reinstalling or deleting the VM deletes its snapshots. " +
			"Restoring a snapshot is not managed by Terraform.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, MarkdownDescription: "`<virtual_machine_id>/<snapshot_id>`.", PlanModifiers: keep},
			"virtual_machine_id": vmIDAttr("replaces the snapshot"),
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Description. Changing it replaces the snapshot.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255), framework.SafeString()},
			},
			"snapshot_id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Snapshot ID.", PlanModifiers: keep},
			"name":             schema.StringAttribute{Computed: true, MarkdownDescription: "Name the platform gave the snapshot.", PlanModifiers: keep},
			"status":           schema.StringAttribute{Computed: true, MarkdownDescription: "`active` until the snapshot is deleted or expires."},
			"operation_status": schema.StringAttribute{Computed: true, MarkdownDescription: "`completed` once the snapshot is taken, or `failed`."},
			"created_at":       schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
		Blocks: map[string]schema.Block{"timeouts": timeoutsBlock("create")},
	}
}

func (r *snapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func setSnapshot(m *snapshotModel, s *client.Snapshot) {
	m.ID = types.StringValue(s.VirtualMachineID + "/" + s.ID)
	m.VirtualMachineID = types.StringValue(s.VirtualMachineID)
	if s.Description != "" || !m.Description.IsNull() {
		m.Description = types.StringValue(s.Description)
	}
	m.SnapshotID = types.StringValue(s.ID)
	m.Name = types.StringValue(s.Name)
	m.Status = types.StringValue(trimEnum(s.SnapshotStatus, "SNAPSHOT_STATUS_"))
	m.OperationStatus = types.StringValue(trimEnum(s.OperationStatus, "SNAPSHOT_OPERATION_STATUS_"))
	m.CreatedAt = framework.Time(s.CreatedAt)
	if m.Timeouts.IsUnknown() {
		m.Timeouts = types.ObjectNull(map[string]attr.Type{"create": types.StringType})
	}
}

func (r *snapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan snapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(plan.Timeouts, "create", 15*time.Minute))
	defer cancel()
	vmID := plan.VirtualMachineID.ValueString()
	unlock := lockVM(vmID)
	defer unlock()

	vm, err := r.client.GetVirtualMachine(ctx, vmID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
		return
	}
	if !vm.HasCapability("snapshots") {
		resp.Diagnostics.AddError("Snapshots are not supported",
			fmt.Sprintf("Virtual machine %s runs on %s, which does not support snapshots (no snapshots capability).", vm.Hostname, trimEnum(vm.PlatformDriver, "PLATFORM_DRIVER_")))
		return
	}
	before, err := r.client.ListSnapshots(ctx, vmID)
	if err != nil {
		resp.Diagnostics.AddError("Error listing snapshots", err.Error())
		return
	}
	snap, err := r.client.CreateSnapshot(ctx, vmID, plan.Description.ValueString())
	if (err == nil && snap == nil) || client.IsAmbiguous(err) {
		// There is no idempotency key, so never retry; adopt the snapshot that appeared.
		snap, err = findNewSnapshot(ctx, r.client, vmID, before, plan.Description.ValueString(), err)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating snapshot", err.Error())
		return
	}
	setSnapshot(&plan, snap)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)

	err = poll(ctx, "the snapshot to complete", func() (bool, error) {
		s, err := r.client.GetSnapshot(ctx, vmID, snap.ID)
		if err != nil {
			return true, err
		}
		setSnapshot(&plan, s)
		switch s.OperationStatus {
		case "SNAPSHOT_OPERATION_STATUS_COMPLETED":
			return true, nil
		case "SNAPSHOT_OPERATION_STATUS_FAILED":
			return true, fmt.Errorf("snapshot %s of virtual machine %s failed; the API does not report why", s.ID, vm.Hostname)
		}
		return false, nil
	})
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if err != nil {
		resp.Diagnostics.AddError("Snapshot did not complete", err.Error())
	}
}

func findNewSnapshot(ctx context.Context, c *client.Client, vmID string, before []client.Snapshot, description string, cause error) (*client.Snapshot, error) {
	after, err := c.ListSnapshots(ctx, vmID)
	if err != nil {
		return nil, fmt.Errorf("%v; listing snapshots afterwards failed too: %v", cause, err)
	}
	old := map[string]bool{}
	for _, s := range before {
		old[s.ID] = true
	}
	var found []client.Snapshot
	for _, s := range after {
		if !old[s.ID] && s.Description == description {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("the create request failed (%v) and %d matching new snapshots appeared; check the VM's snapshots before applying again", cause, len(found))
	}
	return &found[0], nil
}

func (r *snapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state snapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.GetSnapshot(ctx, state.VirtualMachineID.ValueString(), state.SnapshotID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading snapshot", err.Error())
		return
	}
	setSnapshot(&state, s)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *snapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Only timeouts can change in place.
	var plan, state snapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	state.Timeouts = plan.Timeouts
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *snapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state snapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := retryBusy(ctx, func() error {
		return r.client.DeleteSnapshot(ctx, state.VirtualMachineID.ValueString(), state.SnapshotID.ValueString())
	})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting snapshot", err.Error())
	}
}

func (r *snapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitID(req.ID, 2)
	if !ok {
		resp.Diagnostics.AddError("Invalid import ID", "Use <virtual_machine_id>/<snapshot_id>.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("virtual_machine_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("snapshot_id"), parts[1])...)
}
