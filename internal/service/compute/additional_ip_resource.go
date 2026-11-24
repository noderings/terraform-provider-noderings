package compute

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var (
	_ resource.Resource                = &additionalIPResource{}
	_ resource.ResourceWithImportState = &additionalIPResource{}
)

// NewAdditionalIPResource returns the noderings_virtual_machine_additional_ip resource.
func NewAdditionalIPResource() resource.Resource { return &additionalIPResource{} }

type additionalIPResource struct{ client *client.Client }

type additionalIPModel struct {
	ID                   types.String `tfsdk:"id"`
	VirtualMachineID     types.String `tfsdk:"virtual_machine_id"`
	IPAddress            types.String `tfsdk:"ip_address"`
	Gateway              types.String `tfsdk:"gateway"`
	PrefixLength         types.Int64  `tfsdk:"prefix_length"`
	MonthlyUnitPrice     types.String `tfsdk:"monthly_unit_price"`
	ProratedChargeAmount types.String `tfsdk:"prorated_charge_amount"`
	Currency             types.String `tfsdk:"currency"`
}

func (r *additionalIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_additional_ip"
}

func (r *additionalIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "One extra IPv4 address for a virtual machine, from the same network as its primary address. A VM can have at most 10.\n\n" +
			"**Billing:** each address is a paid add-on. VMs on a monthly plan are charged a prorated amount when the address is added. " +
			"Destroying this resource releases the address immediately; you may not get the same address back.",
		Attributes: map[string]schema.Attribute{
			"id":                     schema.StringAttribute{Computed: true, MarkdownDescription: "`<virtual_machine_id>/<ip_address>`.", PlanModifiers: keep},
			"virtual_machine_id":     vmIDAttr("replaces this resource"),
			"ip_address":             schema.StringAttribute{Computed: true, MarkdownDescription: "The address.", PlanModifiers: keep},
			"gateway":                schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway.", PlanModifiers: keep},
			"prefix_length":          schema.Int64Attribute{Computed: true, MarkdownDescription: "Prefix length of the network."},
			"monthly_unit_price":     schema.StringAttribute{Computed: true, MarkdownDescription: "Monthly price of the address, as a decimal string. Only known for addresses created by Terraform.", PlanModifiers: keep},
			"prorated_charge_amount": schema.StringAttribute{Computed: true, MarkdownDescription: "Amount charged when the address was added. Only known for addresses created by Terraform.", PlanModifiers: keep},
			"currency":               schema.StringAttribute{Computed: true, MarkdownDescription: "Currency of the prices.", PlanModifiers: keep},
		},
	}
}

func (r *additionalIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func ipSet(vm *client.VirtualMachine) map[string]client.AdditionalIP {
	out := map[string]client.AdditionalIP{}
	for _, ip := range vm.AdditionalIPv4 {
		out[ip.IPAddress] = ip
	}
	return out
}

func (r *additionalIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan additionalIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vmID := plan.VirtualMachineID.ValueString()
	unlock := lockVM(vmID)
	defer unlock()

	before, err := r.client.GetVirtualMachine(ctx, vmID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
		return
	}
	res, err := r.client.EnableAdditionalIP(ctx, vmID)
	var ip *client.AdditionalIP
	switch {
	case err == nil && res.Allocation != nil:
		ip = res.Allocation
		plan.MonthlyUnitPrice = types.StringValue(res.MonthlyUnitPrice)
		plan.ProratedChargeAmount = types.StringValue(res.ProratedChargeAmount)
		plan.Currency = types.StringValue(res.Currency)
	case err == nil || client.IsAmbiguous(err):
		// Every call adds and bills an address, so never retry. Adopt the
		// address that appeared instead.
		after, gerr := r.client.GetVirtualMachine(ctx, vmID)
		if gerr != nil {
			resp.Diagnostics.AddError("Error adding IP address", fmt.Sprintf("%v; then reading the VM failed too: %v. Check the VM in the console before applying again.", err, gerr))
			return
		}
		old := ipSet(before)
		var added []client.AdditionalIP
		for _, a := range after.AdditionalIPv4 {
			if _, ok := old[a.IPAddress]; !ok {
				added = append(added, a)
			}
		}
		if len(added) != 1 {
			resp.Diagnostics.AddError("Error adding IP address", fmt.Sprintf("The request failed (%v) and %d new addresses appeared on the VM. "+
				"Check the VM in the console, and import an address that was added with terraform import.", err, len(added)))
			return
		}
		ip = &added[0]
		plan.MonthlyUnitPrice, plan.ProratedChargeAmount, plan.Currency = types.StringNull(), types.StringNull(), types.StringNull()
	default:
		detail := err.Error()
		if client.IsFailedPrecondition(err) {
			detail += "\n\nThe VM may have no subscription, or a payment for the prorated charge failed."
		}
		resp.Diagnostics.AddError("Error adding IP address", detail)
		return
	}
	setIP(&plan, vmID, *ip)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func setIP(m *additionalIPModel, vmID string, ip client.AdditionalIP) {
	m.ID = types.StringValue(vmID + "/" + ip.IPAddress)
	m.VirtualMachineID = types.StringValue(vmID)
	m.IPAddress = types.StringValue(ip.IPAddress)
	m.Gateway = types.StringValue(ip.Gateway)
	m.PrefixLength = types.Int64Value(int64(ip.PrefixLength))
	for _, v := range []*types.String{&m.MonthlyUnitPrice, &m.ProratedChargeAmount, &m.Currency} {
		if v.IsUnknown() {
			*v = types.StringNull()
		}
	}
}

func (r *additionalIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state additionalIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vmID := state.VirtualMachineID.ValueString()
	vm, err := r.client.GetVirtualMachine(ctx, vmID)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
		return
	}
	ip, ok := ipSet(vm)[state.IPAddress.ValueString()]
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	setIP(&state, vmID, ip)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *additionalIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every argument forces replacement.
	var plan additionalIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *additionalIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state additionalIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DisableAdditionalIP(ctx, state.VirtualMachineID.ValueString(), state.IPAddress.ValueString())
	if err == nil || client.IsNotFound(err) || (client.IsFailedPrecondition(err) && strings.Contains(strings.ToLower(err.Error()), "no additional ip")) {
		return
	}
	resp.Diagnostics.AddError("Error removing IP address", err.Error())
}

func (r *additionalIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitID(req.ID, 2)
	if !ok {
		resp.Diagnostics.AddError("Invalid import ID", "Use <virtual_machine_id>/<ip_address>, for example 00000000-0000-0000-0000-000000000000/192.0.2.10.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("virtual_machine_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ip_address"), parts[1])...)
}
