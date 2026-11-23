package compute

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	_ resource.Resource                = &firewallAttachmentResource{}
	_ resource.ResourceWithImportState = &firewallAttachmentResource{}
)

// NewFirewallAttachmentResource returns the noderings_firewall_ruleset_attachment resource.
func NewFirewallAttachmentResource() resource.Resource { return &firewallAttachmentResource{} }

type firewallAttachmentResource struct{ client *client.Client }

type firewallAttachmentModel struct {
	ID               types.String `tfsdk:"id"`
	VirtualMachineID types.String `tfsdk:"virtual_machine_id"`
	RulesetID        types.String `tfsdk:"ruleset_id"`
}

func (r *firewallAttachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_ruleset_attachment"
}

func (r *firewallAttachmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Attaches a firewall ruleset to a virtual machine. A VM has at most one ruleset; use one attachment per VM.\n\n" +
			"Only platforms with the `guest_firewall` capability (Proxmox) support firewalls. Rule changes reach the VM within about a minute; " +
			"the API does not report when they are applied.\n\n" +
			"The platform marks the VM as running when a ruleset is attached or detached. If the VM was stopped, this resource stops it again " +
			"afterwards, so `power_state` on `noderings_virtual_machine` does not drift.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Same as `virtual_machine_id`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"virtual_machine_id": vmIDAttr("replaces the attachment"),
			"ruleset_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the firewall ruleset. Changes in place: the old ruleset is detached, then the new one attached.",
				Validators:          []validator.String{framework.UUID()},
			},
		},
	}
}

func (r *firewallAttachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

// change runs attach or detach, retrying while the VM is busy, and stops the
// VM again if it was stopped before, because the platform marks it running.
func (r *firewallAttachmentResource) change(ctx context.Context, vmID string, ops ...func() error) error {
	vm, err := r.client.GetVirtualMachine(ctx, vmID)
	if err != nil {
		return err
	}
	if !vm.HasCapability("guest_firewall") {
		return fmt.Errorf("virtual machine %s runs on %s, which does not support the firewall (no guest_firewall capability). Choose an offer with require_capabilities = [\"guest_firewall\"]",
			vm.Hostname, trimEnum(vm.PlatformDriver, "PLATFORM_DRIVER_"))
	}
	wasStopped := powerState(vm.OperationStatus) == "stopped"
	for _, op := range ops {
		if err := retryBusy(ctx, op); err != nil {
			return err
		}
	}
	if wasStopped {
		if err := setVMPower(ctx, r.client, vmID, "stopped"); err != nil {
			return fmt.Errorf("the firewall change was made, but stopping the VM again failed: %w", err)
		}
	}
	return nil
}

// detachOK reports whether a detach error means nothing (of ours) is attached.
func detachOK(err error) bool {
	if err == nil || client.IsNotFound(err) {
		return true
	}
	var e *client.Error
	if !errors.As(err, &e) {
		return false
	}
	msg := strings.ToLower(e.Message)
	return (e.Code == client.CodeFailedPrecondition && strings.Contains(msg, "no firewall")) ||
		(e.Code == client.CodeInvalidArgument && strings.Contains(msg, "not attached"))
}

func (r *firewallAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vmID, rs := plan.VirtualMachineID.ValueString(), plan.RulesetID.ValueString()
	if err := r.change(ctx, vmID, func() error { return r.client.AttachFirewall(ctx, vmID, rs) }); err != nil {
		resp.Diagnostics.AddError("Error attaching firewall ruleset", err.Error())
		return
	}
	plan.ID = plan.VirtualMachineID
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *firewallAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallAttachmentModel
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
	if vm.FirewallRulesetID == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(vm.ID)
	state.RulesetID = types.StringValue(vm.FirewallRulesetID)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *firewallAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state firewallAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vmID, oldRS, newRS := state.VirtualMachineID.ValueString(), state.RulesetID.ValueString(), plan.RulesetID.ValueString()
	// Attach alone would replace the ruleset, but the old one's hypervisor
	// configuration would not be regenerated, so detach first.
	err := r.change(ctx, vmID,
		func() error {
			if err := r.client.DetachFirewall(ctx, vmID, oldRS); !detachOK(err) {
				return err
			}
			return nil
		},
		func() error { return r.client.AttachFirewall(ctx, vmID, newRS) },
	)
	if err != nil {
		resp.Diagnostics.AddError("Error changing firewall ruleset", err.Error())
		return
	}
	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *firewallAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vmID, rs := state.VirtualMachineID.ValueString(), state.RulesetID.ValueString()
	err := r.change(ctx, vmID, func() error {
		if err := r.client.DetachFirewall(ctx, vmID, rs); !detachOK(err) {
			return err
		}
		return nil
	})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error detaching firewall ruleset", err.Error())
	}
}

func (r *firewallAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("virtual_machine_id"), req.ID)...)
}
