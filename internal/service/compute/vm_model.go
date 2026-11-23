package compute

import (
	"context"
	"errors"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// nonceTagPrefix marks the temporary tag used to find a VM after a lost
// create response. Such tags are hidden from state and removed after create.
const nonceTagPrefix = "tf-create-"

var additionalIPAttrTypes = map[string]attr.Type{
	"ip_address":    types.StringType,
	"gateway":       types.StringType,
	"prefix_length": types.Int64Type,
}

// vmComputed holds the attributes read from the API, shared by the resource
// and the data sources.
type vmComputed struct {
	ID                     types.String `tfsdk:"id"`
	ProvisioningState      types.String `tfsdk:"provisioning_state"`
	IPv4                   types.String `tfsdk:"ipv4"`
	IPv6                   types.String `tfsdk:"ipv6"`
	AdditionalIPv4         types.List   `tfsdk:"additional_ipv4"`
	CPU                    types.Int64  `tfsdk:"cpu"`
	MemoryMiB              types.Int64  `tfsdk:"memory_mib"`
	StorageMiB             types.Int64  `tfsdk:"storage_mib"`
	PlanCode               types.String `tfsdk:"plan_code"`
	PlanBillingType        types.String `tfsdk:"plan_billing_type"`
	ProviderOrganizationID types.String `tfsdk:"provider_organization_id"`
	ProviderName           types.String `tfsdk:"provider_name"`
	RegionCountry          types.String `tfsdk:"region_country"`
	RegionCity             types.String `tfsdk:"region_city"`
	PlatformDriver         types.String `tfsdk:"platform_driver"`
	Capabilities           types.List   `tfsdk:"capabilities"`
	FirewallRulesetID      types.String `tfsdk:"firewall_ruleset_id"`
	HasActiveBackup        types.Bool   `tfsdk:"has_active_backup"`
	InstantAppSlug         types.String `tfsdk:"instant_app_slug"`
	ReinstallStatus        types.String `tfsdk:"reinstall_status"`
	ReinstalledAt          types.String `tfsdk:"reinstalled_at"`
	ComplianceHeld         types.Bool   `tfsdk:"compliance_held"`
	CreatedAt              types.String `tfsdk:"created_at"`
}

func (m *vmComputed) set(ctx context.Context, vm *client.VirtualMachine, diags *diag.Diagnostics) {
	m.ID = types.StringValue(vm.ID)
	m.ProvisioningState = types.StringValue(trimEnum(vm.CRStatus, "CR_STATUS_"))
	m.IPv4 = types.StringValue(vm.IPv4)
	m.IPv6 = types.StringValue(vm.IPv6)
	ips := make([]map[string]attr.Value, 0, len(vm.AdditionalIPv4))
	for _, ip := range vm.AdditionalIPv4 {
		ips = append(ips, map[string]attr.Value{
			"ip_address": types.StringValue(ip.IPAddress), "gateway": types.StringValue(ip.Gateway), "prefix_length": types.Int64Value(int64(ip.PrefixLength)),
		})
	}
	m.AdditionalIPv4 = objectList(ctx, additionalIPAttrTypes, ips, diags)
	m.CPU = types.Int64Value(int64(vm.CPU))
	m.MemoryMiB = types.Int64Value(int64(vm.Memory))
	m.StorageMiB = types.Int64Value(int64(vm.Storage))
	m.PlanCode = types.StringValue(vm.PlanCode)
	m.PlanBillingType = types.StringValue(trimEnum(vm.PlanBillingType, "BILLING_TYPE_"))
	m.ProviderOrganizationID = types.StringValue(vm.ProviderOrganizationID)
	m.ProviderName = types.StringValue(vm.ProviderName)
	m.RegionCountry = types.StringValue(vm.RegionCountry)
	m.RegionCity = types.StringValue(vm.RegionCity)
	m.PlatformDriver = types.StringValue(trimEnum(vm.PlatformDriver, "PLATFORM_DRIVER_"))
	m.Capabilities = framework.Strings(ctx, vm.Capabilities, diags)
	m.FirewallRulesetID = types.StringValue(vm.FirewallRulesetID)
	m.HasActiveBackup = types.BoolValue(vm.HasActiveBackupSubscription)
	m.InstantAppSlug = types.StringValue(vm.InstantAppSlug)
	m.ReinstallStatus = types.StringValue(trimEnum(vm.ReinstallStatus, "REINSTALL_STATUS_"))
	m.ReinstalledAt = framework.Time(vm.ReinstalledAt)
	m.ComplianceHeld = types.BoolValue(vm.ComplianceHeld)
	m.CreatedAt = framework.Time(vm.CreateTime)
}

// userTags drops the reserved create nonce tags.
func userTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if !strings.HasPrefix(t, nonceTagPrefix) {
			out = append(out, t)
		}
	}
	return out
}

func nonceTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if strings.HasPrefix(t, nonceTagPrefix) {
			out = append(out, t)
		}
	}
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

func setStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

func stringMap(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	diags.Append(m.ElementsAs(ctx, &out, false)...)
	return out
}

// translateCreateError adds advice to errors users can fix.
func translateCreateError(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "payment method"):
		msg += "\n\nAdd a payment method to the organization in the NodeRings console. Paid plans, including usage-billed ones, need one."
	case strings.Contains(lower, "runway") || strings.Contains(lower, "insufficient"):
		msg += "\n\nTop up the organization's credit balance, or choose a cheaper plan."
	case strings.Contains(lower, "no vm template"):
		msg += "\n\nThis provider has no image for the chosen distro in this region. Pick another distro, or another offer with data.noderings_marketplace_offer."
	case strings.Contains(lower, "capacity") || strings.Contains(lower, "marketplace-ready node"):
		msg += "\n\nThe region is out of capacity for this plan. Try another provider or city."
	case strings.Contains(lower, "plan") && (strings.Contains(lower, "not found") || strings.Contains(lower, "was not found")):
		msg += "\n\nThe plan is not for sale (any more). Look up a current one with data.noderings_marketplace_offer and apply again."
	}
	var e *client.Error
	if errors.As(err, &e) && e.IsQuota() {
		msg += "\n\nThe organization has reached its virtual machine quota. Delete unused VMs or ask NodeRings support to raise the limit."
	}
	return msg
}

// computedAttr describes one read-only VM attribute.
type computedAttr struct {
	name, kind, desc string
	// stable attributes do not change after create, so plans keep the prior value.
	stable bool
}

var vmComputedAttrs = []computedAttr{
	{"provisioning_state", "string", "Provisioning state: `pending`, `processing`, `applied` (ready) or `failed`.", false},
	{"ipv4", "string", "Primary IPv4 address. On VirtFusion and SolusVM it can stay empty until the platform assigns one.", false},
	{"ipv6", "string", "Primary IPv6 address, empty if the region has no IPv6.", false},
	{"additional_ipv4", "ips", "Additional IPv4 addresses. Manage them with `noderings_virtual_machine_additional_ip`.", false},
	{"cpu", "int", "vCPUs.", true},
	{"memory_mib", "int", "Memory in MiB.", true},
	{"storage_mib", "int", "Disk size in MiB.", true},
	{"plan_code", "string", "Platform plan code, for example `SC-GC-2C-4G-80`.", true},
	{"plan_billing_type", "string", "`usage` (hourly), `subscription` (monthly) or `free`.", true},
	{"provider_organization_id", "string", "ID of the hosting provider.", true},
	{"provider_name", "string", "Name of the hosting provider.", true},
	{"region_country", "string", "Country of the region.", true},
	{"region_city", "string", "City of the region.", true},
	{"platform_driver", "string", "Hypervisor platform: `proxmox`, `virtfusion` or `solusvm`.", true},
	{"capabilities", "list", "Features the platform supports, such as `guest_firewall`, `on_demand_backup` and `snapshots`.", true},
	{"firewall_ruleset_id", "string", "Attached firewall ruleset, empty if none. Manage it with `noderings_firewall_ruleset_attachment`.", false},
	{"has_active_backup", "bool", "Whether the backup add-on is active. Manage it with `noderings_virtual_machine_backup`.", false},
	{"instant_app_slug", "string", "Slug of the Instant App installed at create or reinstall, if any.", false},
	{"reinstall_status", "string", "State of the last reinstall: `none`, `requested`, `deleting`, `provisioning`, `completed` or `failed`.", false},
	{"reinstalled_at", "string", "Time of the last completed reinstall (RFC 3339).", false},
	{"compliance_held", "bool", "Whether the VM is held for a compliance review. A held VM rejects most changes.", false},
	{"created_at", "string", "Creation time (RFC 3339).", true},
}
