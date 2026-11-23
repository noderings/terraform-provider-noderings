// Package dns implements hosted DNS: zones, records, geo records and DNSSEC.
package dns

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/dnsutil"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// Resources lists this package's resources.
func Resources() []func() resource.Resource {
	return []func() resource.Resource{NewZoneResource, NewRecordResource, NewGeoRecordResource, NewDNSSECResource}
}

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewZoneDataSource}
}

var zoneNameRE = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}\.?$`)

var (
	_ resource.Resource                   = &zoneResource{}
	_ resource.ResourceWithImportState    = &zoneResource{}
	_ resource.ResourceWithValidateConfig = &zoneResource{}
)

// NewZoneResource returns the noderings_dns_zone resource.
func NewZoneResource() resource.Resource { return &zoneResource{} }

type zoneResource struct{ client *client.Client }

type zoneModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	ForceDestroy       types.Bool   `tfsdk:"force_destroy"`
	DeletionProtection types.Bool   `tfsdk:"deletion_protection"`
	State              types.String `tfsdk:"state"`
	DelegationOK       types.Bool   `tfsdk:"delegation_ok"`
	Nameservers        types.List   `tfsdk:"nameservers"`
	UnicastNameservers types.List   `tfsdk:"unicast_nameservers"`
	DNSSECMode         types.String `tfsdk:"dnssec_mode"`
	CreatedAt          types.String `tfsdk:"created_at"`
}

func (r *zoneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zone"
}

func (r *zoneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A DNS zone hosted on NodeRings. After creating it, point the domain's nameservers at the `nameservers` output at your registrar.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Zone ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Domain name, for example `example.com`. Stored lower-case without a trailing dot. Changing it replaces the zone. A domain can be hosted by only one organization at a time.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(zoneNameRE, "must be a domain name such as example.com")},
			},
			"force_destroy": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Delete the zone even while DNSSEC is active and the parent zone still has the DS record. That breaks resolution for validating resolvers until the DS is removed. Defaults to `false`.",
			},
			"deletion_protection": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "When `true`, Terraform refuses to delete the zone. Defaults to `false`.",
			},
			"state":         schema.StringAttribute{Computed: true, MarkdownDescription: "`pending_ns` until the domain is delegated to NodeRings, then `active`."},
			"delegation_ok": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the parent zone currently delegates to the NodeRings nameservers."},
			"nameservers": schema.ListAttribute{
				Computed: true, ElementType: types.StringType, PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Nameservers to set at the registrar.",
			},
			"unicast_nameservers": schema.ListAttribute{
				Computed: true, ElementType: types.StringType, PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Optional additional unicast nameservers.",
			},
			"dnssec_mode": schema.StringAttribute{Computed: true, MarkdownDescription: "`unsigned`, `signed_ds_pending` or `signed_active`. Manage DNSSEC with `noderings_dns_zone_dnssec`."},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
	}
}

func (r *zoneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *zoneResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	if name.IsNull() || name.IsUnknown() {
		return
	}
	if n := name.ValueString(); n != dnsutil.NormalizeZoneName(n) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Use the normalized zone name",
			"Write the zone name in lower case without a trailing dot, for example "+dnsutil.NormalizeZoneName(n)+". The API stores it that way.")
	}
}

func (r *zoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan zoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	zone, err := r.client.CreateDNSZone(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating DNS zone", err.Error())
		return
	}
	setZone(ctx, &plan, zone, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *zoneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state zoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	zone, err := r.client.GetDNSZone(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading DNS zone", err.Error())
		return
	}
	if state.ForceDestroy.IsNull() {
		state.ForceDestroy = types.BoolValue(false)
	}
	if state.DeletionProtection.IsNull() {
		state.DeletionProtection = types.BoolValue(false)
	}
	setZone(ctx, &state, zone, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only changes Terraform-side settings (force_destroy, deletion_protection).
func (r *zoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state zoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.ForceDestroy = plan.ForceDestroy
	state.DeletionProtection = plan.DeletionProtection
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *zoneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state zoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeletionProtection.ValueBool() {
		resp.Diagnostics.AddError("Deletion protection is on",
			"Set deletion_protection = false and apply before destroying DNS zone "+state.Name.ValueString()+".")
		return
	}
	err := r.client.DeleteDNSZone(ctx, state.ID.ValueString(), state.ForceDestroy.ValueBool())
	if err != nil && !client.IsNotFound(err) {
		detail := err.Error()
		if client.IsFailedPrecondition(err) {
			detail += "\n\nIf DNSSEC is active, remove the DS record at your registrar first, or set force_destroy = true."
		}
		resp.Diagnostics.AddError("Error deleting DNS zone", detail)
	}
}

func (r *zoneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func setZone(ctx context.Context, m *zoneModel, z *client.DNSZone, diags *diag.Diagnostics) {
	m.ID = types.StringValue(z.ID)
	m.Name = types.StringValue(z.Name)
	m.State = types.StringValue(framework.TrimEnum(z.State, "DNS_ZONE_STATE_"))
	m.DelegationOK = types.BoolValue(z.DelegationOK)
	m.Nameservers = framework.Strings(ctx, z.AssignedNameservers, diags)
	m.UnicastNameservers = framework.Strings(ctx, z.UnicastNameservers, diags)
	m.DNSSECMode = types.StringValue(framework.TrimEnum(z.DNSSECMode, "DNSSEC_MODE_"))
	m.CreatedAt = framework.Time(z.Timestamps.CreateTime)
}

// --- noderings_dns_zone data source ---

// NewZoneDataSource returns the noderings_dns_zone data source.
func NewZoneDataSource() datasource.DataSource { return &zoneDataSource{} }

type zoneDataSource struct{ client *client.Client }

type zoneDataModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	State              types.String `tfsdk:"state"`
	DelegationOK       types.Bool   `tfsdk:"delegation_ok"`
	Nameservers        types.List   `tfsdk:"nameservers"`
	UnicastNameservers types.List   `tfsdk:"unicast_nameservers"`
	DNSSECMode         types.String `tfsdk:"dnssec_mode"`
	CreatedAt          types.String `tfsdk:"created_at"`
}

func (d *zoneDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zone"
}

func (d *zoneDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Looks up a DNS zone in the organization by ID or name.",
		Attributes: map[string]dsschema.Attribute{
			"id":                  dsschema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Zone ID."},
			"name":                dsschema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Domain name."},
			"state":               dsschema.StringAttribute{Computed: true, MarkdownDescription: "`pending_ns` or `active`."},
			"delegation_ok":       dsschema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the parent zone delegates to NodeRings."},
			"nameservers":         dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Nameservers to set at the registrar."},
			"unicast_nameservers": dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Optional unicast nameservers."},
			"dnssec_mode":         dsschema.StringAttribute{Computed: true, MarkdownDescription: "DNSSEC mode."},
			"created_at":          dsschema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
		},
	}
}

func (d *zoneDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *zoneDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *zoneDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg zoneDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var zone *client.DNSZone
	if id := cfg.ID.ValueString(); id != "" {
		z, err := d.client.GetDNSZone(ctx, id)
		if err != nil {
			resp.Diagnostics.AddError("Error reading DNS zone", err.Error())
			return
		}
		zone = z
	} else {
		zones, err := d.client.ListDNSZones(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Error listing DNS zones", err.Error())
			return
		}
		want := dnsutil.NormalizeZoneName(cfg.Name.ValueString())
		for i := range zones {
			if zones[i].Name == want {
				zone = &zones[i]
				break
			}
		}
		if zone == nil {
			resp.Diagnostics.AddError("DNS zone not found", "The organization has no DNS zone named "+want+".")
			return
		}
	}
	var m zoneModel
	setZone(ctx, &m, zone, &resp.Diagnostics)
	out := zoneDataModel{ID: m.ID, Name: m.Name, State: m.State, DelegationOK: m.DelegationOK, Nameservers: m.Nameservers,
		UnicastNameservers: m.UnicastNameservers, DNSSECMode: m.DNSSECMode, CreatedAt: m.CreatedAt}
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}
