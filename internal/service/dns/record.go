package dns

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/dnsutil"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// ownerNameRE matches a lower-case owner name relative to the zone.
var ownerNameRE = regexp.MustCompile(`^(@|\*|(\*\.)?[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?(\.[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?)*)$`)

var (
	_ resource.Resource                   = &recordResource{}
	_ resource.ResourceWithImportState    = &recordResource{}
	_ resource.ResourceWithValidateConfig = &recordResource{}
)

// NewRecordResource returns the noderings_dns_record resource.
func NewRecordResource() resource.Resource { return &recordResource{} }

type recordResource struct{ client *client.Client }

type recordModel struct {
	ID             types.String `tfsdk:"id"`
	ZoneID         types.String `tfsdk:"zone_id"`
	Name           types.String `tfsdk:"name"`
	Type           types.String `tfsdk:"type"`
	TTL            types.Int64  `tfsdk:"ttl"`
	Records        types.Set    `tfsdk:"records"`
	AllowOverwrite types.Bool   `tfsdk:"allow_overwrite"`
}

func (r *recordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_record"
}

func (r *recordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "All records of one name and type in a zone (an RRset). Use one resource per name and type, with every value in `records`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`<zone_id>/<name>/<type>`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"zone_id": schema.StringAttribute{Required: true, MarkdownDescription: "Zone ID.", PlanModifiers: replace, Validators: []validator.String{framework.UUID()}},
			"name": schema.StringAttribute{
				Required: true, PlanModifiers: replace,
				MarkdownDescription: "Owner name relative to the zone, in lower case, for example `www` or `_dmarc`. Use `@` for the zone apex and `*` for a wildcard.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 253), stringvalidator.RegexMatches(ownerNameRE, "must be a lower-case name relative to the zone, such as www, _dmarc, *.app or @")},
			},
			"type": schema.StringAttribute{
				Required: true, PlanModifiers: replace,
				MarkdownDescription: "Record type: " + strings.Join(dnsutil.RecordTypes, ", ") + ".",
				Validators:          []validator.String{stringvalidator.OneOf(dnsutil.RecordTypes...)},
			},
			"ttl": schema.Int64Attribute{
				Required: true, MarkdownDescription: "Time to live in seconds, 30 to 86400.",
				Validators: []validator.Int64{int64validator.Between(30, 86400)},
			},
			"records": schema.SetAttribute{
				Required: true, ElementType: types.StringType,
				MarkdownDescription: "Record values in presentation format, for example `192.0.2.10`, `10 mail.example.com.` (MX) or `0 issue \"letsencrypt.org\"` (CAA). " +
					"Write TXT values as plain text without surrounding quotes; long values are split for you. Hostnames should end with a dot.",
				Validators: []validator.Set{setvalidator.SizeAtLeast(1)},
			},
			"allow_overwrite": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Take over records of this name and type that already exist in the zone. Without it, creating fails if they exist. Defaults to `false`.",
			},
		},
	}
}

func (r *recordResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *recordResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m recordModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Type.IsUnknown() || m.Records.IsUnknown() || m.Type.IsNull() || m.Records.IsNull() {
		return
	}
	var values []string
	for _, e := range m.Records.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsUnknown() || s.IsNull() {
			return
		}
		values = append(values, s.ValueString())
	}
	if err := dnsutil.ValidateValues(m.Type.ValueString(), values); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("records"), "Invalid record values", err.Error())
	}
	if !m.Name.IsUnknown() && m.Name.ValueString() == "@" {
		switch m.Type.ValueString() {
		case "CNAME":
			resp.Diagnostics.AddAttributeError(path.Root("type"), "CNAME not allowed at the apex", "Use ALIAS for the zone apex.")
		case "NS", "DS":
			resp.Diagnostics.AddAttributeError(path.Root("type"), "Managed by NodeRings", "Apex NS and DS records are managed by NodeRings.")
		}
	}
}

func (r *recordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan recordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	zoneID, name, rrtype := plan.ZoneID.ValueString(), plan.Name.ValueString(), plan.Type.ValueString()
	if !plan.AllowOverwrite.ValueBool() {
		existing, err := r.find(ctx, zoneID, name, rrtype)
		if err != nil {
			resp.Diagnostics.AddError("Error checking for existing DNS records", err.Error())
			return
		}
		if existing != nil {
			resp.Diagnostics.AddError("DNS records already exist",
				fmt.Sprintf("%s %s records already exist in the zone. Import them with `terraform import <address> %s/%s/%s`, or set allow_overwrite = true to replace them.",
					name, rrtype, zoneID, name, rrtype))
			return
		}
	}
	r.write(ctx, &plan, &resp.State, &resp.Diagnostics, "creating")
}

func (r *recordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan recordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, &plan, &resp.State, &resp.Diagnostics, "updating")
}

func (r *recordResource) write(ctx context.Context, plan *recordModel, state *tfsdk.State, diags *diag.Diagnostics, verb string) {
	var values []string
	diags.Append(plan.Records.ElementsAs(ctx, &values, false)...)
	if diags.HasError() {
		return
	}
	rrtype := plan.Type.ValueString()
	rdata := make([]string, len(values))
	for i, v := range values {
		rdata[i] = dnsutil.ToAPI(rrtype, v)
	}
	zoneID, name := plan.ZoneID.ValueString(), plan.Name.ValueString()
	_, err := r.client.UpsertDNSRecord(ctx, zoneID, client.DNSRecord{
		Name: name, Type: rrtype, TTL: client.Int64(plan.TTL.ValueInt64()), RData: rdata,
	})
	if err != nil {
		diags.AddError("Error "+verb+" DNS records", dnsError(err))
		return
	}
	// The upsert response echoes the request; read back what the server stored.
	got, err := r.find(ctx, zoneID, name, rrtype)
	if err != nil {
		diags.AddError("Error reading DNS records after "+verb, err.Error())
		return
	}
	if got == nil {
		diags.AddError("DNS records missing after "+verb, "The API accepted the change but the records are not listed. Try again.")
		return
	}
	plan.ID = types.StringValue(recordID(zoneID, name, rrtype))
	setRecordValues(ctx, plan, got, diags)
	diags.Append(state.Set(ctx, plan)...)
}

func (r *recordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state recordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.find(ctx, state.ZoneID.ValueString(), state.Name.ValueString(), state.Type.ValueString())
	if client.IsNotFound(err) || (err == nil && got == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading DNS records", err.Error())
		return
	}
	if state.AllowOverwrite.IsNull() {
		state.AllowOverwrite = types.BoolValue(false)
	}
	setRecordValues(ctx, &state, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *recordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state recordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	zoneID := state.ZoneID.ValueString()
	err := r.client.DeleteDNSRecord(ctx, zoneID, state.Name.ValueString(), state.Type.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting DNS records", dnsError(err))
	}
}

func (r *recordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Use <zone_id>/<name>/<type>, for example 0f3c.../www/A or 0f3c.../@/MX.")
		return
	}
	rrtype := strings.ToUpper(parts[2])
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), recordID(parts[0], parts[1], rrtype))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("zone_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), rrtype)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allow_overwrite"), false)...)
}

// find returns the RRset with exactly this name and type, or nil. The API's
// name filter is a substring match, so results are matched exactly here.
func (r *recordResource) find(ctx context.Context, zoneID, name, rrtype string) (*client.DNSRecord, error) {
	rel := dnsutil.RelativeName(name, "")
	filter := rel
	if rel == "@" {
		filter = ""
	}
	recs, err := r.client.ListDNSRecords(ctx, zoneID, filter, rrtype)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if dnsutil.RelativeName(recs[i].Name, "") == rel && strings.EqualFold(recs[i].Type, rrtype) && !recs[i].System {
			return &recs[i], nil
		}
	}
	return nil, nil
}

// setRecordValues stores the server's TTL and values. When the values are
// equivalent to what is already in the model, the model's spelling is kept.
func setRecordValues(ctx context.Context, m *recordModel, rec *client.DNSRecord, diags *diag.Diagnostics) {
	rrtype := m.Type.ValueString()
	m.TTL = types.Int64Value(int64(rec.TTL))
	server := make([]string, len(rec.RData))
	for i, v := range rec.RData {
		server[i] = dnsutil.FromAPI(rrtype, v)
	}
	var current []string
	if !m.Records.IsNull() && !m.Records.IsUnknown() {
		diags.Append(m.Records.ElementsAs(ctx, &current, false)...)
	}
	if current != nil && dnsutil.SameValues(rrtype, current, server) {
		return
	}
	set, d := types.SetValueFrom(ctx, types.StringType, server)
	diags.Append(d...)
	m.Records = set
}

func recordID(zoneID, name, rrtype string) string {
	return zoneID + "/" + name + "/" + strings.ToUpper(rrtype)
}

func dnsError(err error) string {
	msg := err.Error()
	var e *client.Error
	if errors.As(err, &e) && e.HTTPStatus == 503 {
		msg += "\n\nThe DNS backend rejected the change. Check the record values: hostnames should end with a dot, and values must match the record type."
	}
	return msg
}
