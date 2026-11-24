package compute

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

// vmLocks serialises calls that add things to one VM, so a resource that
// diffs the VM before and after its own call does not adopt another's result.
var vmLocks sync.Map

func lockVM(id string) func() {
	m, _ := vmLocks.LoadOrStore(id, &sync.Mutex{})
	mu, _ := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func vmIDAttr(what string) schema.StringAttribute {
	return schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "ID of the virtual machine. Changing it " + what + ".",
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:          []validator.String{framework.UUID()},
	}
}

func splitID(id string, n int) ([]string, bool) {
	parts := strings.Split(id, "/")
	if len(parts) != n {
		return nil, false
	}
	for _, p := range parts {
		if p == "" {
			return nil, false
		}
	}
	return parts, true
}

func activeBackup(vm *client.VirtualMachine) *client.AddonSubscription {
	for i := range vm.AddonSubscriptions {
		a := &vm.AddonSubscriptions[i]
		if (a.AddonType == "ADDON_TYPE_BACKUP" || a.AddonType == "ADDON_TYPE_SNAPSHOT") && a.AddonStatus == "ADDON_STATUS_ACTIVE" {
			return a
		}
	}
	return nil
}

// --- noderings_virtual_machine_backup ---

var (
	_ resource.Resource                = &backupResource{}
	_ resource.ResourceWithImportState = &backupResource{}
)

// NewBackupResource returns the noderings_virtual_machine_backup resource.
func NewBackupResource() resource.Resource { return &backupResource{} }

type backupResource struct{ client *client.Client }

type backupModel struct {
	ID                  types.String `tfsdk:"id"`
	VirtualMachineID    types.String `tfsdk:"virtual_machine_id"`
	AddonID             types.String `tfsdk:"addon_id"`
	AddonSubscriptionID types.String `tfsdk:"addon_subscription_id"`
	AddonStatus         types.String `tfsdk:"addon_status"`
}

func (r *backupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_backup"
}

func (r *backupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Turns on the paid backup add-on for a virtual machine. The platform then takes backups on its schedule, " +
			"starting with one right away; list them with `data.noderings_virtual_machine_backups`.\n\n" +
			"Only platforms with the `on_demand_backup` capability (Proxmox) support backups.\n\n" +
			"**Billing:** the add-on is charged on the VM's subscription. Destroying this resource cancels the add-on immediately " +
			"and **deletes all of the VM's backups**. Reinstalling the VM also cancels it.\n\n" +
			"If the add-on is already active when this resource is created, it is adopted without enabling it again.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Same as `virtual_machine_id`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"virtual_machine_id": vmIDAttr("replaces this resource"),
			"addon_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Backup add-on, usually `data.noderings_marketplace_addon.<name>.id`. Changing it switches add-on in place; the old one is cancelled.",
				Validators:          []validator.String{framework.UUID()},
			},
			"addon_subscription_id": schema.StringAttribute{Computed: true, MarkdownDescription: "ID of the add-on subscription."},
			"addon_status":          schema.StringAttribute{Computed: true, MarkdownDescription: "Add-on status, `active` while backups are on."},
		},
	}
}

func (r *backupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *backupResource) enable(ctx context.Context, vmID, addonID string) (*client.VirtualMachine, error) {
	vm, err := r.client.GetVirtualMachine(ctx, vmID)
	if err != nil {
		return nil, err
	}
	if !vm.HasCapability("on_demand_backup") {
		return nil, fmt.Errorf("virtual machine %s runs on %s, which does not support backups (no on_demand_backup capability)",
			vm.Hostname, trimEnum(vm.PlatformDriver, "PLATFORM_DRIVER_"))
	}
	// Enabling an add-on that is already active starts another backup run
	// and may charge again, so adopt it instead.
	if a := activeBackup(vm); a != nil && a.AddonID == addonID {
		return vm, nil
	}
	if err := retryBusy(ctx, func() error { return r.client.EnableBackup(ctx, vmID, addonID) }); err != nil {
		return nil, err
	}
	return r.client.GetVirtualMachine(ctx, vmID)
}

func setBackup(m *backupModel, vm *client.VirtualMachine) bool {
	a := activeBackup(vm)
	if a == nil {
		return false
	}
	m.ID = types.StringValue(vm.ID)
	m.VirtualMachineID = types.StringValue(vm.ID)
	m.AddonID = types.StringValue(a.AddonID)
	m.AddonSubscriptionID = types.StringValue(a.ID)
	m.AddonStatus = types.StringValue(trimEnum(a.AddonStatus, "ADDON_STATUS_"))
	return true
}

func (r *backupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan backupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vm, err := r.enable(ctx, plan.VirtualMachineID.ValueString(), plan.AddonID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error enabling backups", err.Error())
		return
	}
	if !setBackup(&plan, vm) {
		resp.Diagnostics.AddError("Error enabling backups", "The API accepted the request but the VM shows no active backup add-on.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *backupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state backupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vm, err := r.client.GetVirtualMachine(ctx, state.VirtualMachineID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
		return
	}
	if !setBackup(&state, vm) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *backupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan backupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vm, err := r.enable(ctx, plan.VirtualMachineID.ValueString(), plan.AddonID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error switching backup add-on", err.Error())
		return
	}
	if !setBackup(&plan, vm) {
		resp.Diagnostics.AddError("Error switching backup add-on", "The VM shows no active backup add-on after the change.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *backupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state backupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := retryBusy(ctx, func() error {
		return r.client.DisableBackup(ctx, state.VirtualMachineID.ValueString(), state.AddonID.ValueString())
	})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error disabling backups", err.Error())
	}
}

func (r *backupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("virtual_machine_id"), req.ID)...)
}
