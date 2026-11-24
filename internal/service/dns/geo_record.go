package dns

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/dnsutil"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var (
	_ resource.Resource                = &geoRecordResource{}
	_ resource.ResourceWithImportState = &geoRecordResource{}
)

// NewGeoRecordResource returns the noderings_dns_geo_record resource.
func NewGeoRecordResource() resource.Resource { return &geoRecordResource{} }

type geoRecordResource struct{ client *client.Client }

type geoRecordModel struct {
	ID         types.String `tfsdk:"id"`
	ZoneID     types.String `tfsdk:"zone_id"`
	Name       types.String `tfsdk:"name"`
	Type       types.String `tfsdk:"type"`
	TTL        types.Int64  `tfsdk:"ttl"`
	Default    types.String `tfsdk:"default"`
	Continents types.Map    `tfsdk:"continents"`
	Countries  types.Map    `tfsdk:"countries"`
	Regions    types.Map    `tfsdk:"regions"`
}

var (
	continentKeyRE = regexp.MustCompile(`^(AF|AN|AS|EU|NA|OC|SA)$`)
	countryKeyRE   = regexp.MustCompile(`^[A-Z]{2}$`)
	regionKeyRE    = regexp.MustCompile(`^[A-Z]{2}-[A-Z0-9]{1,8}$`)
)

func (r *geoRecordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_geo_record"
}

func (r *geoRecordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	mapValues := []validator.String{stringvalidator.LengthAtLeast(1)}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A geo-routed record: resolvers get an answer chosen by their location. " +
			"The most specific match wins: region, then country, then continent, then `default`. " +
			"Only one geo record per name is supported.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`<zone_id>/<name>/<type>`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"zone_id": schema.StringAttribute{Required: true, MarkdownDescription: "Zone ID.", PlanModifiers: replace, Validators: []validator.String{framework.UUID()}},
			"name": schema.StringAttribute{
				Required: true, PlanModifiers: replace,
				MarkdownDescription: "Owner name relative to the zone, in lower case. Use `@` for the apex.",
				Validators:          []validator.String{stringvalidator.RegexMatches(ownerNameRE, "must be a lower-case name relative to the zone, such as www or @")},
			},
			"type": schema.StringAttribute{
				Required: true, PlanModifiers: replace,
				MarkdownDescription: "Record type: " + strings.Join(dnsutil.GeoRecordTypes, ", ") + ".",
				Validators:          []validator.String{stringvalidator.OneOf(dnsutil.GeoRecordTypes...)},
			},
			"ttl": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(60),
				MarkdownDescription: "Time to live in seconds, 30 to 60. Defaults to `60`.",
				Validators:          []validator.Int64{int64validator.Between(30, 60)},
			},
			"default": schema.StringAttribute{Required: true, MarkdownDescription: "Answer for resolvers that match no other rule.", Validators: mapValues},
			"continents": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Answers by continent code: `AF`, `AN`, `AS`, `EU`, `NA`, `OC`, `SA`.",
				Validators:          []validator.Map{mapvalidator.KeysAre(stringvalidator.RegexMatches(continentKeyRE, "must be a continent code")), mapvalidator.ValueStringsAre(mapValues...)},
			},
			"countries": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Answers by ISO 3166-1 alpha-2 country code, for example `DE`.",
				Validators:          []validator.Map{mapvalidator.KeysAre(stringvalidator.RegexMatches(countryKeyRE, "must be an ISO 3166-1 alpha-2 code")), mapvalidator.ValueStringsAre(mapValues...)},
			},
			"regions": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Answers by ISO 3166-2 subdivision such as `US-CA`, or a region group such as `US-IX`.",
				Validators:          []validator.Map{mapvalidator.KeysAre(stringvalidator.RegexMatches(regionKeyRE, "must be an ISO 3166-2 code or region group")), mapvalidator.ValueStringsAre(mapValues...)},
			},
		},
	}
}

func (r *geoRecordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *geoRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan geoRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	existing, err := r.client.ListDNSGeoRecords(ctx, plan.ZoneID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error listing geo records", err.Error())
		return
	}
	for _, g := range existing {
		if dnsutil.SameName(g.Name, plan.Name.ValueString(), "") {
			resp.Diagnostics.AddError("Geo record already exists",
				fmt.Sprintf("The zone already has a %s geo record at %s. A name can have only one geo record; import it with `terraform import <address> %s/%s/%s`.",
					g.Type, plan.Name.ValueString(), plan.ZoneID.ValueString(), plan.Name.ValueString(), g.Type))
			return
		}
	}
	r.write(ctx, &plan, &resp.State, &resp.Diagnostics, "creating")
}

func (r *geoRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan geoRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, &plan, &resp.State, &resp.Diagnostics, "updating")
}

func (r *geoRecordResource) write(ctx context.Context, plan *geoRecordModel, state *tfsdk.State, diags *diag.Diagnostics, verb string) {
	rec := client.DNSGeoRecord{
		Name: plan.Name.ValueString(), Type: plan.Type.ValueString(), TTL: client.Int64(plan.TTL.ValueInt64()),
		DefaultRData: plan.Default.ValueString(),
		Continents:   mapValue(ctx, plan.Continents, diags),
		Countries:    mapValue(ctx, plan.Countries, diags),
		Regions:      mapValue(ctx, plan.Regions, diags),
	}
	if diags.HasError() {
		return
	}
	if _, err := r.client.UpsertDNSGeoRecord(ctx, plan.ZoneID.ValueString(), rec); err != nil {
		diags.AddError("Error "+verb+" geo record", dnsError(err))
		return
	}
	got, err := r.client.GetDNSGeoRecord(ctx, plan.ZoneID.ValueString(), rec.Name, rec.Type)
	if err != nil {
		diags.AddError("Error reading geo record after "+verb, err.Error())
		return
	}
	plan.ID = types.StringValue(recordID(plan.ZoneID.ValueString(), rec.Name, rec.Type))
	setGeo(ctx, plan, got, diags)
	diags.Append(state.Set(ctx, plan)...)
}

func (r *geoRecordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state geoRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.client.GetDNSGeoRecord(ctx, state.ZoneID.ValueString(), state.Name.ValueString(), state.Type.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading geo record", err.Error())
		return
	}
	setGeo(ctx, &state, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *geoRecordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state geoRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteDNSGeoRecord(ctx, state.ZoneID.ValueString(), state.Name.ValueString(), state.Type.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting geo record", dnsError(err))
	}
}

func (r *geoRecordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Use <zone_id>/<name>/<type>, for example 0f3c.../app/A.")
		return
	}
	rrtype := strings.ToUpper(parts[2])
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), recordID(parts[0], parts[1], rrtype))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("zone_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), rrtype)...)
}

func setGeo(ctx context.Context, m *geoRecordModel, g *client.DNSGeoRecord, diags *diag.Diagnostics) {
	m.TTL = types.Int64Value(int64(g.TTL))
	m.Default = types.StringValue(g.DefaultRData)
	m.Continents = toMap(ctx, g.Continents, m.Continents, diags)
	m.Countries = toMap(ctx, g.Countries, m.Countries, diags)
	m.Regions = toMap(ctx, g.Regions, m.Regions, diags)
}

// toMap keeps an unset map null when the API returns it empty.
func toMap(ctx context.Context, in map[string]string, prior types.Map, diags *diag.Diagnostics) types.Map {
	if len(in) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.MapNull(types.StringType)
	}
	if in == nil {
		in = map[string]string{}
	}
	m, d := types.MapValueFrom(ctx, types.StringType, in)
	diags.Append(d...)
	return m
}

func mapValue(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	diags.Append(m.ElementsAs(ctx, &out, false)...)
	return out
}
