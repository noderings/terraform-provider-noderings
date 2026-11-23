package dns

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var (
	_ resource.Resource                = &dnssecResource{}
	_ resource.ResourceWithImportState = &dnssecResource{}
)

// NewDNSSECResource returns the noderings_dns_zone_dnssec resource.
func NewDNSSECResource() resource.Resource { return &dnssecResource{} }

type dnssecResource struct{ client *client.Client }

type dnssecModel struct {
	ID           types.String `tfsdk:"id"`
	ZoneID       types.String `tfsdk:"zone_id"`
	ForceDestroy types.Bool   `tfsdk:"force_destroy"`
	Mode         types.String `tfsdk:"mode"`
	DS           types.String `tfsdk:"ds"`
	DNSKey       types.String `tfsdk:"dnskey"`
	KeyTag       types.Int64  `tfsdk:"key_tag"`
	Algorithm    types.String `tfsdk:"algorithm"`
	DigestType   types.Int64  `tfsdk:"digest_type"`
	DSAtParent   types.Bool   `tfsdk:"ds_at_parent"`
}

func (r *dnssecResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zone_dnssec"
}

func (r *dnssecResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Signs a zone with DNSSEC. Add the `ds` value as a DS record at your registrar; the zone becomes `signed_active` once NodeRings sees it there.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Same as `zone_id`.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"zone_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "Zone ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{framework.UUID()},
			},
			"force_destroy": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Turn DNSSEC off even while the registrar still has the DS record. That breaks resolution for validating resolvers until the DS is removed. Defaults to `false`.",
			},
			"mode":         schema.StringAttribute{Computed: true, MarkdownDescription: "`signed_ds_pending` until the DS is seen at the parent, then `signed_active`."},
			"ds":           schema.StringAttribute{Computed: true, MarkdownDescription: "DS record to add at the registrar, as `<key tag> <algorithm> <digest type> <digest>`."},
			"dnskey":       schema.StringAttribute{Computed: true, MarkdownDescription: "Public signing key."},
			"key_tag":      schema.Int64Attribute{Computed: true, MarkdownDescription: "Key tag."},
			"algorithm":    schema.StringAttribute{Computed: true, MarkdownDescription: "Signing algorithm."},
			"digest_type":  schema.Int64Attribute{Computed: true, MarkdownDescription: "DS digest type."},
			"ds_at_parent": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the parent zone has the DS record."},
		},
	}
}

func (r *dnssecResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *dnssecResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnssecModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	zoneID := plan.ZoneID.ValueString()
	info, err := r.client.GetDNSSEC(ctx, zoneID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading DNSSEC state", err.Error())
		return
	}
	// Enabling an already signed zone resets it to "DS pending", so only enable when unsigned.
	if info.Mode == "" || info.Mode == "DNSSEC_MODE_UNSIGNED" || info.Mode == "DNSSEC_MODE_UNSPECIFIED" {
		info, err = r.client.EnableDNSSEC(ctx, zoneID)
		if err != nil {
			resp.Diagnostics.AddError("Error enabling DNSSEC", err.Error())
			return
		}
	}
	plan.ID = types.StringValue(zoneID)
	setDNSSEC(&plan, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *dnssecResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnssecModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	info, err := r.client.GetDNSSEC(ctx, state.ZoneID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading DNSSEC state", err.Error())
		return
	}
	if info.Mode == "DNSSEC_MODE_UNSIGNED" {
		resp.State.RemoveResource(ctx)
		return
	}
	if state.ForceDestroy.IsNull() {
		state.ForceDestroy = types.BoolValue(false)
	}
	state.ID = state.ZoneID
	setDNSSEC(&state, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only changes force_destroy.
func (r *dnssecResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state dnssecModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.ForceDestroy = plan.ForceDestroy
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *dnssecResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dnssecModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.DisableDNSSEC(ctx, state.ZoneID.ValueString(), state.ForceDestroy.ValueBool())
	if err != nil && !client.IsNotFound(err) {
		detail := err.Error()
		if client.IsFailedPrecondition(err) {
			detail += "\n\nRemove the DS record at your registrar first, or set force_destroy = true."
		}
		resp.Diagnostics.AddError("Error disabling DNSSEC", detail)
	}
}

func (r *dnssecResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("zone_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("force_destroy"), false)...)
}

func setDNSSEC(m *dnssecModel, i *client.DNSSECInfo) {
	m.Mode = types.StringValue(framework.TrimEnum(i.Mode, "DNSSEC_MODE_"))
	m.DS = types.StringValue(i.DS)
	m.DNSKey = types.StringValue(i.DNSKey)
	m.KeyTag = types.Int64Value(int64(i.KeyTag))
	m.Algorithm = types.StringValue(i.Algorithm)
	m.DigestType = types.Int64Value(int64(i.DigestType))
	m.DSAtParent = types.BoolValue(i.DSAtParent)
}
