package compute

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

func dataComputedAttr(a computedAttr) schema.Attribute {
	switch a.kind {
	case "int":
		return schema.Int64Attribute{Computed: true, MarkdownDescription: a.desc}
	case "bool":
		return schema.BoolAttribute{Computed: true, MarkdownDescription: a.desc}
	case "list":
		return schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: a.desc}
	case "ips":
		return schema.ListNestedAttribute{Computed: true, MarkdownDescription: a.desc, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"ip_address":    schema.StringAttribute{Computed: true, MarkdownDescription: "IPv4 address."},
			"gateway":       schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway."},
			"prefix_length": schema.Int64Attribute{Computed: true, MarkdownDescription: "Prefix length."},
		}}}
	}
	return schema.StringAttribute{Computed: true, MarkdownDescription: a.desc}
}

// --- noderings_virtual_machine ---

var _ datasource.DataSourceWithConfigValidators = &vmDataSource{}

// NewVirtualMachineDataSource returns the noderings_virtual_machine data source.
func NewVirtualMachineDataSource() datasource.DataSource { return &vmDataSource{} }

type vmDataSource struct{ client *client.Client }

type vmDataModel struct {
	vmComputed
	Hostname     types.String `tfsdk:"hostname"`
	Tags         types.Set    `tfsdk:"tags"`
	PlanID       types.String `tfsdk:"plan_id"`
	RegionID     types.String `tfsdk:"region_id"`
	Distro       types.String `tfsdk:"distro"`
	InstantAppID types.String `tfsdk:"instant_app_id"`
	PowerState   types.String `tfsdk:"power_state"`
}

func (d *vmDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (d *vmDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id":       schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "VM ID.", Validators: []validator.String{framework.UUID()}},
		"hostname": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Hostname to look for. Hostnames are not unique; the lookup fails if more than one VM matches."},
		"tags": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType,
			MarkdownDescription: "Tags the VM must all have, when looking up by tags (alone or with `hostname`). The lookup fails unless exactly one VM matches. Afterwards, all of the VM's tags."},
		"plan_id":        schema.StringAttribute{Computed: true, MarkdownDescription: "Plan ID."},
		"region_id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Region ID."},
		"distro":         schema.StringAttribute{Computed: true, MarkdownDescription: "Operating system, for example `ubuntu_24_04`."},
		"instant_app_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Instant App installed, if any."},
		"power_state":    schema.StringAttribute{Computed: true, MarkdownDescription: "`running`, `stopped`, or empty while the VM is being created or reinstalled."},
	}
	for _, a := range vmComputedAttrs {
		attrs[a.name] = dataComputedAttr(a)
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up one virtual machine by ID, hostname or tags.",
		Attributes:          attrs,
	}
}

func (d *vmDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.AtLeastOneOf(path.MatchRoot("id"), path.MatchRoot("hostname"), path.MatchRoot("tags")),
		datasourcevalidator.Conflicting(path.MatchRoot("id"), path.MatchRoot("hostname")),
		datasourcevalidator.Conflicting(path.MatchRoot("id"), path.MatchRoot("tags")),
	}
}

func (d *vmDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func hasAllTags(vm *client.VirtualMachine, want []string) bool {
	have := map[string]bool{}
	for _, t := range vm.Tags {
		have[t] = true
	}
	for _, t := range want {
		if !have[t] {
			return false
		}
	}
	return true
}

func (d *vmDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg vmDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := cfg.ID.ValueString()
	if id == "" {
		vms, err := d.client.ListVirtualMachines(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Error listing virtual machines", err.Error())
			return
		}
		tags := setStrings(ctx, cfg.Tags, &resp.Diagnostics)
		var matches []string
		for i := range vms {
			if (cfg.Hostname.IsNull() || vms[i].Hostname == cfg.Hostname.ValueString()) && hasAllTags(&vms[i], tags) {
				matches = append(matches, vms[i].ID)
			}
		}
		if len(matches) != 1 {
			resp.Diagnostics.AddError("Virtual machine lookup did not match exactly one VM",
				fmt.Sprintf("%d virtual machines match. Narrow the lookup with tags, or use id.", len(matches)))
			return
		}
		id = matches[0]
	}
	vm, err := d.client.GetVirtualMachine(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
		return
	}
	var out vmDataModel
	out.set(ctx, vm, &resp.Diagnostics)
	out.Hostname = types.StringValue(vm.Hostname)
	out.Tags = stringSet(ctx, userTags(vm.Tags), &resp.Diagnostics)
	out.PlanID = types.StringValue(vm.PlanID)
	out.RegionID = types.StringValue(vm.RegionID)
	out.Distro = types.StringValue(trimEnum(vm.Distro, "DISTRO_"))
	out.InstantAppID = types.StringValue(vm.InstantAppID)
	out.PowerState = types.StringValue(powerState(vm.OperationStatus))
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// --- noderings_virtual_machines ---

// NewVirtualMachinesDataSource returns the noderings_virtual_machines data source.
func NewVirtualMachinesDataSource() datasource.DataSource { return &vmsDataSource{} }

type vmsDataSource struct{ client *client.Client }

var vmSummaryAttrTypes = map[string]attr.Type{
	"id": types.StringType, "hostname": types.StringType, "power_state": types.StringType, "provisioning_state": types.StringType,
	"ipv4": types.StringType, "ipv6": types.StringType, "tags": types.SetType{ElemType: types.StringType}, "distro": types.StringType,
	"plan_id": types.StringType, "plan_code": types.StringType, "region_id": types.StringType, "region_country": types.StringType,
	"region_city": types.StringType, "provider_name": types.StringType, "firewall_ruleset_id": types.StringType, "created_at": types.StringType,
}

type vmsDataModel struct {
	PowerState      types.String `tfsdk:"power_state"`
	Tags            types.Set    `tfsdk:"tags"`
	HostnameRegex   types.String `tfsdk:"hostname_regex"`
	VirtualMachines types.List   `tfsdk:"virtual_machines"`
}

func (d *vmsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machines"
}

func (d *vmsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.Attribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the organization's virtual machines, sorted by hostname.",
		Attributes: map[string]schema.Attribute{
			"power_state": schema.StringAttribute{Optional: true, MarkdownDescription: "Only VMs in this state: `running` or `stopped`.",
				Validators: []validator.String{stringvalidator.OneOf("running", "stopped")}},
			"tags":           schema.SetAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Only VMs that have all of these tags."},
			"hostname_regex": schema.StringAttribute{Optional: true, MarkdownDescription: "Only VMs whose hostname matches this regular expression (RE2 syntax)."},
			"virtual_machines": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Matching VMs.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": str("VM ID."), "hostname": str("Hostname."), "power_state": str("`running` or `stopped`, empty while transitioning."),
				"provisioning_state": str("Provisioning state."), "ipv4": str("Primary IPv4 address."), "ipv6": str("Primary IPv6 address."),
				"tags":   schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Tags."},
				"distro": str("Operating system."), "plan_id": str("Plan ID."), "plan_code": str("Plan code."), "region_id": str("Region ID."),
				"region_country": str("Country."), "region_city": str("City."), "provider_name": str("Hosting provider."),
				"firewall_ruleset_id": str("Attached firewall ruleset."), "created_at": str("Creation time (RFC 3339)."),
			}}},
		},
	}
}

func (d *vmsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *vmsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m vmsDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var re *regexp.Regexp
	if p := m.HostnameRegex.ValueString(); p != "" {
		var err error
		if re, err = regexp.Compile(p); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("hostname_regex"), "Invalid regular expression", err.Error())
			return
		}
	}
	vms, err := d.client.ListVirtualMachines(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing virtual machines", err.Error())
		return
	}
	tags := setStrings(ctx, m.Tags, &resp.Diagnostics)
	sort.SliceStable(vms, func(i, j int) bool {
		if vms[i].Hostname != vms[j].Hostname {
			return vms[i].Hostname < vms[j].Hostname
		}
		return vms[i].ID < vms[j].ID
	})
	var items []map[string]attr.Value
	for i := range vms {
		vm := &vms[i]
		ps := powerState(vm.OperationStatus)
		if (!m.PowerState.IsNull() && ps != m.PowerState.ValueString()) || !hasAllTags(vm, tags) || (re != nil && !re.MatchString(vm.Hostname)) {
			continue
		}
		items = append(items, map[string]attr.Value{
			"id": types.StringValue(vm.ID), "hostname": types.StringValue(vm.Hostname), "power_state": types.StringValue(ps),
			"provisioning_state": types.StringValue(trimEnum(vm.CRStatus, "CR_STATUS_")), "ipv4": types.StringValue(vm.IPv4),
			"ipv6": types.StringValue(vm.IPv6), "tags": stringSet(ctx, userTags(vm.Tags), &resp.Diagnostics),
			"distro": types.StringValue(trimEnum(vm.Distro, "DISTRO_")), "plan_id": types.StringValue(vm.PlanID),
			"plan_code": types.StringValue(vm.PlanCode), "region_id": types.StringValue(vm.RegionID),
			"region_country": types.StringValue(vm.RegionCountry), "region_city": types.StringValue(vm.RegionCity),
			"provider_name": types.StringValue(vm.ProviderName), "firewall_ruleset_id": types.StringValue(vm.FirewallRulesetID),
			"created_at": framework.Time(vm.CreateTime),
		})
	}
	m.VirtualMachines = objectList(ctx, vmSummaryAttrTypes, items, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_virtual_machine_tags ---

// NewTagsDataSource returns the noderings_virtual_machine_tags data source.
func NewTagsDataSource() datasource.DataSource { return &tagsDataSource{} }

type tagsDataSource struct{ client *client.Client }

func (d *tagsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_tags"
}

func (d *tagsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Every tag in use on the organization's virtual machines.",
		Attributes: map[string]schema.Attribute{
			"tags": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Tags, sorted."},
		},
	}
}

func (d *tagsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *tagsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	tags, err := d.client.ListVMTags(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing virtual machine tags", err.Error())
		return
	}
	tags = userTags(tags)
	sort.Strings(tags)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tags"), framework.Strings(ctx, tags, &resp.Diagnostics))...)
}

// --- noderings_virtual_machine_backups ---

// NewBackupsDataSource returns the noderings_virtual_machine_backups data source.
func NewBackupsDataSource() datasource.DataSource { return &backupsDataSource{} }

type backupsDataSource struct{ client *client.Client }

var backupAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "addon_id": types.StringType, "size_bytes": types.Int64Type,
	"created_at": types.StringType, "operation_status": types.StringType, "restore_status": types.StringType,
}

func (d *backupsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_backups"
}

func (d *backupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.Attribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists a virtual machine's backups, newest first. Backups are taken by the platform once `noderings_virtual_machine_backup` is enabled.",
		Attributes: map[string]schema.Attribute{
			"virtual_machine_id": schema.StringAttribute{Required: true, MarkdownDescription: "ID of the virtual machine.", Validators: []validator.String{framework.UUID()}},
			"backups": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Backups.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": str("Backup ID."), "name": str("Name."), "addon_id": str("Backup add-on that took it."),
				"size_bytes": schema.Int64Attribute{Computed: true, MarkdownDescription: "Size in bytes."},
				"created_at": str("Creation time (RFC 3339)."), "operation_status": str("`pending`, `processing`, `completed` or `failed`."),
				"restore_status": str("State of the last restore from this backup."),
			}}},
		},
	}
}

func (d *backupsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *backupsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var vmID types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("virtual_machine_id"), &vmID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	backups, err := d.client.ListBackups(ctx, vmID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error listing backups", err.Error())
		return
	}
	items := make([]map[string]attr.Value, 0, len(backups))
	for _, b := range backups {
		items = append(items, map[string]attr.Value{
			"id": types.StringValue(b.ID), "name": types.StringValue(b.Name), "addon_id": types.StringValue(b.AddonID),
			"size_bytes": types.Int64Value(int64(b.SizeBytes)), "created_at": framework.Time(b.CreatedAt),
			"operation_status": types.StringValue(trimEnum(b.OperationStatus, "BACKUP_OPERATION_STATUS_")),
			"restore_status":   types.StringValue(trimEnum(b.RestoreStatus, "RESTORE_STATUS_")),
		})
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("virtual_machine_id"), vmID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("backups"), objectList(ctx, backupAttrTypes, items, &resp.Diagnostics))...)
}

// --- noderings_virtual_machine_snapshots ---

// NewSnapshotsDataSource returns the noderings_virtual_machine_snapshots data source.
func NewSnapshotsDataSource() datasource.DataSource { return &snapshotsDataSource{} }

type snapshotsDataSource struct{ client *client.Client }

var snapshotAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "description": types.StringType, "created_at": types.StringType,
	"status": types.StringType, "operation_status": types.StringType, "restore_status": types.StringType,
}

func (d *snapshotsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_snapshots"
}

func (d *snapshotsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.Attribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "**Experimental.** Lists a virtual machine's snapshots, newest first.",
		Attributes: map[string]schema.Attribute{
			"virtual_machine_id": schema.StringAttribute{Required: true, MarkdownDescription: "ID of the virtual machine.", Validators: []validator.String{framework.UUID()}},
			"snapshots": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Snapshots.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": str("Snapshot ID."), "name": str("Name."), "description": str("Description."), "created_at": str("Creation time (RFC 3339)."),
				"status": str("`active`, `deleted` or `expired`."), "operation_status": str("`pending`, `processing`, `completed` or `failed`."),
				"restore_status": str("State of the last restore from this snapshot."),
			}}},
		},
	}
}

func (d *snapshotsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *snapshotsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var vmID types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("virtual_machine_id"), &vmID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	snaps, err := d.client.ListSnapshots(ctx, vmID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error listing snapshots", err.Error())
		return
	}
	items := make([]map[string]attr.Value, 0, len(snaps))
	for _, s := range snaps {
		items = append(items, map[string]attr.Value{
			"id": types.StringValue(s.ID), "name": types.StringValue(s.Name), "description": types.StringValue(s.Description),
			"created_at": framework.Time(s.CreatedAt), "status": types.StringValue(trimEnum(s.SnapshotStatus, "SNAPSHOT_STATUS_")),
			"operation_status": types.StringValue(trimEnum(s.OperationStatus, "SNAPSHOT_OPERATION_STATUS_")),
			"restore_status":   types.StringValue(trimEnum(s.RestoreStatus, "RESTORE_STATUS_")),
		})
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("virtual_machine_id"), vmID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("snapshots"), objectList(ctx, snapshotAttrTypes, items, &resp.Diagnostics))...)
}
