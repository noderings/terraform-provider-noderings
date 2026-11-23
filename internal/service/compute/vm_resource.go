package compute

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// Private state keys.
const (
	privIdempotencyKey = "idempotency_key"
	privNonce          = "create_nonce"
	// privImported marks a resource imported since its last apply: the
	// arguments the API never returns are null in state and must not plan a
	// replacement when configuration fills them in.
	privImported = "imported"
)

// Arguments that can only change by reinstalling the VM.
var reinstallArgs = []string{"hostname", "distro", "instant_app_id", "instant_app_variables", "ssh_key_ids", "password"}

// Arguments the API never returns.
var writeOnlyArgs = map[string]bool{"ssh_key_ids": true, "password": true, "instant_app_variables": true, "coupon_code": true}

var (
	_ resource.Resource                     = &vmResource{}
	_ resource.ResourceWithImportState      = &vmResource{}
	_ resource.ResourceWithModifyPlan       = &vmResource{}
	_ resource.ResourceWithConfigValidators = &vmResource{}
)

// NewVirtualMachineResource returns the noderings_virtual_machine resource.
func NewVirtualMachineResource() resource.Resource { return &vmResource{} }

type vmResource struct{ client *client.Client }

type vmModel struct {
	vmComputed
	Hostname            types.String `tfsdk:"hostname"`
	PlanID              types.String `tfsdk:"plan_id"`
	RegionID            types.String `tfsdk:"region_id"`
	Distro              types.String `tfsdk:"distro"`
	InstantAppID        types.String `tfsdk:"instant_app_id"`
	InstantAppVariables types.Map    `tfsdk:"instant_app_variables"`
	SSHKeyIDs           types.Set    `tfsdk:"ssh_key_ids"`
	Password            types.String `tfsdk:"password"`
	Tags                types.Set    `tfsdk:"tags"`
	PowerState          types.String `tfsdk:"power_state"`
	CouponCode          types.String `tfsdk:"coupon_code"`
	ReinstallOnChange   types.Bool   `tfsdk:"reinstall_on_change"`
	DeletionProtection  types.Bool   `tfsdk:"deletion_protection"`
	Timeouts            types.Object `tfsdk:"timeouts"`
}

func (r *vmResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

const replaceNote = " Changing it **replaces the VM** (a new VM, new IP addresses, a new subscription), or reinstalls it when `reinstall_on_change = true`."

func (r *vmResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, MarkdownDescription: "VM ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"hostname": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Hostname. Not unique: several VMs may share one. The platform also creates a firewall address group named after it, so don't name your own address groups after VM hostnames." + replaceNote,
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 253), framework.SafeString()},
		},
		"plan_id": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Plan to order, usually `data.noderings_marketplace_offer.<name>.plan_id`. Changing it **replaces the VM**: there is no resize.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{framework.UUID()},
		},
		"region_id": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Region of the plan, usually `data.noderings_marketplace_offer.<name>.region_id`. Changing it **replaces the VM**.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{framework.UUID()},
		},
		"distro": schema.StringAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: "Operating system, one of " + backtickList(distros) + ". See `data.noderings_distros`. Required unless `instant_app_id` is set; with an Instant App it defaults to the app's distro and must match it if set. Whether a provider has an image for a distro is only known at create." + replaceNote,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			Validators:          []validator.String{stringvalidator.OneOf(distros...)},
		},
		"instant_app_id": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Instant App to install, usually `data.noderings_instant_app.<name>.id`." + replaceNote,
			Validators:          []validator.String{framework.UUID()},
		},
		"instant_app_variables": schema.MapAttribute{
			Optional: true, Sensitive: true, ElementType: types.StringType,
			MarkdownDescription: "Settings for the Instant App. Unknown names are rejected; see the `variables` of `data.noderings_instant_apps`. The API does not return them, so after import write them in configuration." + replaceNote,
			Validators: []validator.Map{
				mapvalidator.AlsoRequires(path.MatchRoot("instant_app_id")),
				mapvalidator.ValueStringsAre(framework.SafeString()),
			},
		},
		"ssh_key_ids": schema.SetAttribute{
			Optional: true, ElementType: types.StringType,
			MarkdownDescription: "IDs of SSH keys (`noderings_ssh_key`) to install. Exactly one of `ssh_key_ids` and `password` is required. The API does not return them, so after import write them in configuration." + replaceNote,
			Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(framework.UUID())},
		},
		"password": schema.StringAttribute{
			Optional: true, Sensitive: true,
			MarkdownDescription: "Root password, at most 255 characters. Stored in the Terraform state. The API does not return it, so after import write it in configuration." + replaceNote,
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
		},
		"tags": schema.SetAttribute{
			Optional: true, Computed: true, ElementType: types.StringType,
			Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			MarkdownDescription: "Tags. Firewall rules can select VMs by tag. Changes in place. Tags starting with `" + nonceTagPrefix + "` are reserved.",
			Validators: []validator.Set{setvalidator.ValueStringsAre(
				stringvalidator.LengthBetween(1, 100), framework.SafeString(),
				notPrefix{nonceTagPrefix},
			)},
		},
		"power_state": schema.StringAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: "`running` or `stopped`. Changes in place. If unset, Terraform does not manage the power state and only reports it. This is the requested state; the platform does not report the hypervisor's actual state.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			Validators:          []validator.String{stringvalidator.OneOf("running", "stopped")},
		},
		"coupon_code": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Coupon to apply when ordering. Only used at create; later changes are ignored.",
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 64)},
		},
		"reinstall_on_change": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(false),
			MarkdownDescription: "When `true`, changes to `hostname`, `distro`, `instant_app_id`, `instant_app_variables`, `ssh_key_ids` or `password` **reinstall** the VM in place instead of replacing it. A reinstall keeps the VM, its IP addresses and subscription, but **wipes the disk, deletes all snapshots and cancels the backup add-on**. Defaults to `false`.",
		},
		"deletion_protection": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(false),
			MarkdownDescription: "When `true`, Terraform refuses to delete the VM, including for a replacement. Defaults to `false`.",
		},
	}
	for _, a := range vmComputedAttrs {
		attrs[a.name] = resourceComputedAttr(a)
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A virtual machine ordered from a provider on the NodeRings marketplace.\n\n" +
			"**Billing:** creating a VM starts a subscription for its plan. Destroying or replacing it cancels the subscription immediately " +
			"with **no refund** for prepaid (monthly) plans; usage-billed VMs are charged for the final partial hour.",
		Attributes: attrs,
		Blocks:     map[string]schema.Block{"timeouts": timeoutsBlock("create", "update", "delete")},
	}
}

func resourceComputedAttr(a computedAttr) schema.Attribute {
	switch a.kind {
	case "int":
		at := schema.Int64Attribute{Computed: true, MarkdownDescription: a.desc}
		if a.stable {
			at.PlanModifiers = []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
		}
		return at
	case "bool":
		at := schema.BoolAttribute{Computed: true, MarkdownDescription: a.desc}
		if a.stable {
			at.PlanModifiers = []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}
		}
		return at
	case "list":
		at := schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: a.desc}
		if a.stable {
			at.PlanModifiers = []planmodifier.List{listplanmodifier.UseStateForUnknown()}
		}
		return at
	case "ips":
		return schema.ListNestedAttribute{Computed: true, MarkdownDescription: a.desc, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"ip_address":    schema.StringAttribute{Computed: true, MarkdownDescription: "IPv4 address."},
			"gateway":       schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway."},
			"prefix_length": schema.Int64Attribute{Computed: true, MarkdownDescription: "Prefix length."},
		}}}
	}
	at := schema.StringAttribute{Computed: true, MarkdownDescription: a.desc}
	if a.stable {
		at.PlanModifiers = []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	}
	return at
}

func backtickList(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = "`" + v + "`"
	}
	return strings.Join(q, ", ")
}

func (r *vmResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("ssh_key_ids"), path.MatchRoot("password")),
		resourcevalidator.AtLeastOneOf(path.MatchRoot("distro"), path.MatchRoot("instant_app_id")),
	}
}

func (r *vmResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

type privateGetter interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}

// reinstallChanges lists the reinstall-only arguments that differ between
// plan and state. Arguments that are null in state only because of an import
// do not count.
func reinstallChanges(ctx context.Context, plan, state vmModel, configDistroNull bool, priv privateGetter) []string {
	imported := getPrivate(ctx, priv, privImported) != ""
	values := func(m vmModel) map[string]attr.Value {
		return map[string]attr.Value{
			"hostname": m.Hostname, "distro": m.Distro, "instant_app_id": m.InstantAppID,
			"instant_app_variables": m.InstantAppVariables, "ssh_key_ids": m.SSHKeyIDs, "password": m.Password,
		}
	}
	p, s := values(plan), values(state)
	var changed []string
	for _, name := range reinstallArgs {
		if name == "distro" && configDistroNull {
			continue
		}
		if s[name].IsNull() && imported && writeOnlyArgs[name] {
			continue
		}
		if !p[name].Equal(s[name]) {
			changed = append(changed, name)
		}
	}
	return changed
}

func (r *vmResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state vmModel
	var configDistro types.String
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("distro"), &configDistro)...)
	if resp.Diagnostics.HasError() {
		return
	}
	changed := reinstallChanges(ctx, plan, state, configDistro.IsNull(), req.Private)
	if len(changed) == 0 {
		return
	}
	if !plan.ReinstallOnChange.ValueBool() {
		for _, name := range changed {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root(name))
		}
		return
	}
	// A reinstall can change these.
	unknown := []string{"instant_app_slug"}
	if configDistro.IsNull() {
		unknown = append(unknown, "distro")
	}
	if plan.PowerState.Equal(state.PowerState) {
		var cfgPower types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("power_state"), &cfgPower)...)
		if cfgPower.IsNull() {
			unknown = append(unknown, "power_state")
		}
	}
	for _, name := range unknown {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(name), types.StringUnknown())...)
	}
	resp.Diagnostics.AddWarning("The virtual machine will be reinstalled",
		fmt.Sprintf("Changing %s reinstalls VM %s because reinstall_on_change = true. This wipes its disk, deletes all of its snapshots and cancels its backup add-on.",
			strings.Join(changed, ", "), state.Hostname.ValueString()))
}

// setFromVM copies API values into the model. Arguments the API does not
// return are left as they are.
func setFromVM(ctx context.Context, m *vmModel, vm *client.VirtualMachine, diags *diag.Diagnostics) {
	m.set(ctx, vm, diags)
	m.Hostname = types.StringValue(vm.Hostname)
	if vm.PlanID != "" {
		m.PlanID = types.StringValue(vm.PlanID)
	}
	if vm.RegionID != "" {
		m.RegionID = types.StringValue(vm.RegionID)
	}
	if vm.Distro != "" && vm.Distro != "DISTRO_UNSPECIFIED" {
		m.Distro = types.StringValue(trimEnum(vm.Distro, "DISTRO_"))
	} else if m.Distro.IsUnknown() {
		m.Distro = types.StringNull()
	}
	if vm.InstantAppID != "" {
		m.InstantAppID = types.StringValue(vm.InstantAppID)
	} else {
		m.InstantAppID = types.StringNull()
	}
	m.Tags = stringSet(ctx, userTags(vm.Tags), diags)
	if ps := powerState(vm.OperationStatus); ps != "" {
		m.PowerState = types.StringValue(ps)
	} else if m.PowerState.IsNull() || m.PowerState.IsUnknown() {
		// CREATED (just provisioned) and REINSTALLING end up running.
		m.PowerState = types.StringValue("running")
	}
	if m.ReinstallOnChange.IsNull() || m.ReinstallOnChange.IsUnknown() {
		m.ReinstallOnChange = types.BoolValue(false)
	}
	if m.DeletionProtection.IsNull() || m.DeletionProtection.IsUnknown() {
		m.DeletionProtection = types.BoolValue(false)
	}
	if m.Timeouts.IsUnknown() {
		m.Timeouts = types.ObjectNull(timeoutAttrTypes)
	}
}

func (r *vmResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(plan.Timeouts, "create", 30*time.Minute))
	defer cancel()
	wantPower := plan.PowerState

	in := client.CreateVirtualMachineRequest{
		Hostname:            plan.Hostname.ValueString(),
		PlanID:              plan.PlanID.ValueString(),
		RegionID:            plan.RegionID.ValueString(),
		Tags:                setStrings(ctx, plan.Tags, &resp.Diagnostics),
		SSHKeyIDs:           setStrings(ctx, plan.SSHKeyIDs, &resp.Diagnostics),
		Password:            plan.Password.ValueString(),
		InstantAppID:        plan.InstantAppID.ValueString(),
		InstantAppVariables: stringMap(ctx, plan.InstantAppVariables, &resp.Diagnostics),
		IdempotencyKey:      newUUID(),
	}
	if !plan.Distro.IsNull() && !plan.Distro.IsUnknown() {
		in.Distro = distroEnum(plan.Distro.ValueString())
	}
	if c := plan.CouponCode.ValueString(); c != "" {
		in.Coupon = &client.Coupon{Code: c}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// Free plans ignore the idempotency key, so a retry after a lost response
	// would order a second VM. Tag the VM with a nonce to find it instead.
	nonce := ""
	offer, err := r.client.GetMarketplacePlan(ctx, in.PlanID)
	switch {
	case client.IsNotFound(err):
		resp.Diagnostics.AddAttributeError(path.Root("plan_id"), "Plan is not for sale",
			fmt.Sprintf("Plan %s is no longer on the marketplace (it may be out of capacity or withdrawn). Look up a current offer with data.noderings_marketplace_offer and apply again.", in.PlanID))
		return
	case err != nil || offer.BillingType == "BILLING_TYPE_FREE":
		nonce = nonceTagPrefix + newUUID()
		in.Tags = append(in.Tags, nonce)
	}

	vm, err := r.createWithRecovery(ctx, &in, nonce)
	if err != nil {
		resp.Diagnostics.AddError("Error creating virtual machine", translateCreateError(err))
		return
	}

	// Save the VM right away, so a failure while waiting leaves it in state
	// (tainted) instead of orphaned.
	setFromVM(ctx, &plan, vm, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privIdempotencyKey, privateValue(in.IdempotencyKey))...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privNonce, privateValue(nonce))...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vm, err = r.waitReady(ctx, vm.ID)
	if vm != nil {
		setFromVM(ctx, &plan, vm, &resp.Diagnostics)
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Virtual machine did not become ready", err.Error())
		return
	}

	if nonce != "" {
		if _, err := r.client.UpdateVirtualMachineTags(ctx, vm.ID, nil, []string{nonce}); err != nil {
			resp.Diagnostics.AddWarning("Could not remove the temporary create tag",
				fmt.Sprintf("The provider tagged the VM with %s while creating it and could not remove the tag: %v. It will try again on the next refresh.", nonce, err))
		} else {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, privNonce, nil)...)
		}
	}

	if wantPower.ValueString() == "stopped" {
		if err := r.setPower(ctx, vm.ID, "stopped"); err != nil {
			resp.Diagnostics.AddError("Error stopping virtual machine", err.Error())
			return
		}
	}
	r.refresh(ctx, &plan, &resp.State, &resp.Diagnostics)
}

// createWithRecovery sends the create request, retrying after ambiguous
// failures. Paid plans honour the idempotency key, so a retry returns the same
// VM. On free plans the VM is first looked up by its nonce tag.
func (r *vmResource) createWithRecovery(ctx context.Context, in *client.CreateVirtualMachineRequest, nonce string) (*client.VirtualMachine, error) {
	var vm *client.VirtualMachine
	var lastErr error
	newKey := false
	attempts := 0
	err := poll(ctx, "the create request to succeed", func() (bool, error) {
		attempts++
		v, err := r.client.CreateVirtualMachine(ctx, *in)
		if err == nil {
			vm = v
			return true, nil
		}
		lastErr = err
		if client.IsFailedPrecondition(err) && strings.Contains(strings.ToLower(err.Error()), "new idempotency key") && !newKey {
			newKey = true
			in.IdempotencyKey = newUUID()
			return false, nil
		}
		if !client.IsAmbiguous(err) {
			return true, err
		}
		if nonce != "" {
			found, ferr := r.findByTag(ctx, nonce)
			if ferr == nil && found != nil {
				vm = found
				return true, nil
			}
		}
		if attempts >= 6 {
			return true, err
		}
		return false, nil
	})
	if err != nil {
		if lastErr != nil && !strings.Contains(err.Error(), lastErr.Error()) {
			return nil, fmt.Errorf("%w (last error: %v)", err, lastErr)
		}
		return nil, err
	}
	if vm == nil && nonce != "" {
		// An older API returns no object; find the VM by its nonce.
		found, ferr := r.findByTag(ctx, nonce)
		if ferr != nil {
			return nil, ferr
		}
		vm = found
	}
	if vm == nil || vm.ID == "" {
		return nil, fmt.Errorf("the API accepted the order but did not return the new virtual machine; check the console before applying again")
	}
	return vm, nil
}

func (r *vmResource) findByTag(ctx context.Context, tag string) (*client.VirtualMachine, error) {
	vms, err := r.client.ListVirtualMachines(ctx)
	if err != nil {
		return nil, err
	}
	for i := range vms {
		for _, t := range vms[i].Tags {
			if t == tag {
				return &vms[i], nil
			}
		}
	}
	return nil, nil
}

// waitReady polls until provisioning is applied or failed. It returns the
// last VM read even on error.
func (r *vmResource) waitReady(ctx context.Context, id string) (*client.VirtualMachine, error) {
	var vm *client.VirtualMachine
	err := poll(ctx, "the virtual machine to be provisioned", func() (bool, error) {
		v, err := r.client.GetVirtualMachine(ctx, id)
		if err != nil {
			if client.IsNotFound(err) {
				return true, fmt.Errorf("virtual machine %s disappeared while it was being provisioned", id)
			}
			return true, err
		}
		vm = v
		switch v.CRStatus {
		case "CR_STATUS_APPLIED":
			return true, nil
		case "CR_STATUS_FAILED":
			return true, fmt.Errorf("provisioning of virtual machine %s failed. The API does not report why. "+
				"The VM is marked tainted, so the next apply replaces it; if it keeps failing, try another offer or contact the provider", id)
		}
		return false, nil
	})
	return vm, err
}

// setPower starts or stops the VM and waits until the API reports the new state.
func (r *vmResource) setPower(ctx context.Context, id, want string) error {
	return setVMPower(ctx, r.client, id, want)
}

func setVMPower(ctx context.Context, c *client.Client, id, want string) error {
	err := retryBusy(ctx, func() error {
		var err error
		if want == "stopped" {
			_, err = c.StopVirtualMachine(ctx, id)
		} else {
			_, err = c.StartVirtualMachine(ctx, id)
		}
		return err
	})
	if err != nil {
		return err
	}
	return poll(ctx, "the virtual machine to be "+want, func() (bool, error) {
		vm, err := c.GetVirtualMachine(ctx, id)
		if err != nil {
			return true, err
		}
		return powerState(vm.OperationStatus) == want && vm.CRStatus != "CR_STATUS_PROCESSING", nil
	})
}

type stateSetter interface {
	Set(context.Context, any) diag.Diagnostics
}

func (r *vmResource) refresh(ctx context.Context, m *vmModel, st stateSetter, diags *diag.Diagnostics) {
	vm, err := r.client.GetVirtualMachine(ctx, m.ID.ValueString())
	if err != nil {
		diags.AddError("Error reading virtual machine", err.Error())
		return
	}
	setFromVM(ctx, m, vm, diags)
	diags.Append(st.Set(ctx, m)...)
}

func (r *vmResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vm, err := r.client.GetVirtualMachine(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
		return
	}
	// Remove a create nonce tag left behind by an interrupted create.
	if leftover := nonceTags(vm.Tags); len(leftover) > 0 && getPrivate(ctx, req.Private, privNonce) != "" {
		if _, err := r.client.UpdateVirtualMachineTags(ctx, vm.ID, nil, leftover); err == nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, privNonce, nil)...)
		}
	}
	setFromVM(ctx, &state, vm, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *vmResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmModel
	var configDistro types.String
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("distro"), &configDistro)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(plan.Timeouts, "update", 30*time.Minute))
	defer cancel()
	id := state.ID.ValueString()
	plan.ID = state.ID

	// Tags.
	want := setStrings(ctx, plan.Tags, &resp.Diagnostics)
	have := setStrings(ctx, state.Tags, &resp.Diagnostics)
	add, remove := diffStrings(want, have), diffStrings(have, want)
	if len(add) > 0 || len(remove) > 0 {
		if _, err := r.client.UpdateVirtualMachineTags(ctx, id, add, remove); err != nil {
			resp.Diagnostics.AddError("Error updating virtual machine tags", err.Error())
			return
		}
	}

	// Reinstall.
	reinstalled := false
	if changed := reinstallChanges(ctx, plan, state, configDistro.IsNull(), req.Private); len(changed) > 0 {
		if !plan.ReinstallOnChange.ValueBool() {
			resp.Diagnostics.AddError("Unexpected in-place change",
				"Changing "+strings.Join(changed, ", ")+" needs a reinstall, but reinstall_on_change is false. Please report this issue to the provider developers.")
			return
		}
		if err := r.reinstall(ctx, id, plan, configDistro.IsNull()); err != nil {
			resp.Diagnostics.AddError("Error reinstalling virtual machine", err.Error())
			return
		}
		reinstalled = true
	}

	// Power.
	if p := plan.PowerState; !p.IsNull() && !p.IsUnknown() && (!p.Equal(state.PowerState) || (reinstalled && p.ValueString() == "stopped")) {
		if err := r.setPower(ctx, id, p.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error changing virtual machine power state", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privImported, nil)...)
	r.refresh(ctx, &plan, &resp.State, &resp.Diagnostics)
}

func (r *vmResource) reinstall(ctx context.Context, id string, plan vmModel, distroFromApp bool) error {
	var diags diag.Diagnostics
	in := client.ReinstallVirtualMachineRequest{
		Hostname:            plan.Hostname.ValueString(),
		SSHKeyIDs:           setStrings(ctx, plan.SSHKeyIDs, &diags),
		Password:            plan.Password.ValueString(),
		InstantAppID:        plan.InstantAppID.ValueString(),
		InstantAppVariables: stringMap(ctx, plan.InstantAppVariables, &diags),
	}
	if !distroFromApp && !plan.Distro.IsUnknown() {
		in.Distro = distroEnum(plan.Distro.ValueString())
	}
	if diags.HasError() {
		return fmt.Errorf("reading the plan: %v", diags)
	}
	if err := retryBusy(ctx, func() error {
		_, err := r.client.ReinstallVirtualMachine(ctx, id, in)
		return err
	}); err != nil {
		return err
	}
	return poll(ctx, "the reinstall to finish", func() (bool, error) {
		vm, err := r.client.GetVirtualMachine(ctx, id)
		if err != nil {
			return true, err
		}
		switch vm.ReinstallStatus {
		case "REINSTALL_STATUS_COMPLETED":
			return vm.CRStatus != "CR_STATUS_PROCESSING", nil
		case "REINSTALL_STATUS_FAILED":
			return true, fmt.Errorf("the reinstall of virtual machine %s failed; the API does not report why", id)
		}
		return false, nil
	})
}

func diffStrings(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

func (r *vmResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeletionProtection.ValueBool() {
		resp.Diagnostics.AddError("Deletion protection is on",
			"Set deletion_protection = false and apply before destroying or replacing virtual machine "+state.Hostname.ValueString()+".")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(state.Timeouts, "delete", 15*time.Minute))
	defer cancel()
	err := retryBusy(ctx, func() error { return r.client.DeleteVirtualMachine(ctx, state.ID.ValueString()) })
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting virtual machine", err.Error())
	}
}

func (r *vmResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("timeouts"), types.ObjectNull(timeoutAttrTypes))...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privImported, privateValue("1"))...)
}
