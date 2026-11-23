package marketplace

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

func computedStr(desc string) schema.Attribute {
	return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
}

func objList(attrTypes map[string]attr.Type, items []map[string]attr.Value, diags *diag.Diagnostics) types.List {
	vals := make([]attr.Value, 0, len(items))
	for _, it := range items {
		o, d := types.ObjectValue(attrTypes, it)
		diags.Append(d...)
		vals = append(vals, o)
	}
	l, d := types.ListValue(types.ObjectType{AttrTypes: attrTypes}, vals)
	diags.Append(d...)
	return l
}

var regionAttrTypes = map[string]attr.Type{"region_id": types.StringType, "country": types.StringType, "city": types.StringType}

func regionsSchema() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Regions the plan is sold in.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
		"region_id": computedStr("Region ID."), "country": computedStr("Country."), "city": computedStr("City."),
	}}}
}

func regionsValue(regions []client.PlanRegion, diags *diag.Diagnostics) types.List {
	items := make([]map[string]attr.Value, 0, len(regions))
	for _, r := range regions {
		items = append(items, map[string]attr.Value{"region_id": types.StringValue(r.RegionID), "country": types.StringValue(r.Country), "city": types.StringValue(r.City)})
	}
	return objList(regionAttrTypes, items, diags)
}

// --- noderings_marketplace_provider ---

// NewProviderDataSource returns the noderings_marketplace_provider data source.
func NewProviderDataSource() datasource.DataSource { return &providerDataSource{} }

type providerDataSource struct{ client *client.Client }

var catalogPlanAttrTypes = map[string]attr.Type{
	"plan_id": types.StringType, "plan_code": types.StringType, "plan_name": types.StringType, "billing_type": types.StringType,
	"compute_type": types.StringType, "asset_type": types.StringType,
	"regions": types.ListType{ElemType: types.ObjectType{AttrTypes: regionAttrTypes}},
	"prices":  types.ListType{ElemType: types.ObjectType{AttrTypes: priceAttrTypes}},
}

type providerModel struct {
	ID             types.String  `tfsdk:"id"`
	StorefrontSlug types.String  `tfsdk:"storefront_slug"`
	Name           types.String  `tfsdk:"name"`
	Description    types.String  `tfsdk:"description"`
	Website        types.String  `tfsdk:"website"`
	Rating         types.Float64 `tfsdk:"rating"`
	TotalReviews   types.Int64   `tfsdk:"total_reviews"`
	PlatformDriver types.String  `tfsdk:"platform_driver"`
	Capabilities   types.List    `tfsdk:"capabilities"`
	HasIPv6        types.Bool    `tfsdk:"has_ipv6"`
	Cities         types.List    `tfsdk:"cities"`
	Plans          types.List    `tfsdk:"plans"`
}

func (d *providerDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_provider"
}

func (d *providerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a hosting provider by ID or storefront slug, with its full catalog: every plan with its regions and prices.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Provider ID.", Validators: []validator.String{framework.UUID()}},
			"storefront_slug": schema.StringAttribute{Optional: true, MarkdownDescription: "Slug of the provider's public storefront, for example `acme-hosting`.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"name":            computedStr("Provider name."),
			"description":     computedStr("Description."),
			"website":         computedStr("Website."),
			"rating":          schema.Float64Attribute{Computed: true, MarkdownDescription: "Average customer rating (0-5)."},
			"total_reviews":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of reviews."},
			"platform_driver": computedStr("Platform, for example `proxmox`, `virtfusion` or `solusvm`."),
			"capabilities":    schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Features the platform supports."},
			"has_ipv6":        schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the provider offers IPv6."},
			"cities":          schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Cities the provider sells in, sorted."},
			"plans": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Plans, sorted by plan code, billing type and ID.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"plan_id": computedStr("Plan ID."), "plan_code": computedStr("Plan code."), "plan_name": computedStr("Plan name."),
				"billing_type": computedStr("`usage`, `subscription` or `free`."), "compute_type": computedStr("Compute type, for example `general_compute`."),
				"asset_type": computedStr("`vm` or `gameserver`."), "regions": regionsSchema(), "prices": pricesSchema(),
			}}},
		},
	}
}

func (d *providerDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("storefront_slug"))}
}

func (d *providerDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *providerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := m.ID.ValueString()
	if slug := m.StorefrontSlug.ValueString(); slug != "" {
		sf, err := d.client.GetPublicStorefront(ctx, slug)
		if err != nil {
			resp.Diagnostics.AddError("Error reading storefront", err.Error())
			return
		}
		id = sf.ProviderID
	}
	p, err := d.client.GetProvider(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading marketplace provider", err.Error())
		return
	}
	plans := p.AvailablePlans
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].PlanCode != plans[j].PlanCode {
			return plans[i].PlanCode < plans[j].PlanCode
		}
		if plans[i].BillingType != plans[j].BillingType {
			return plans[i].BillingType < plans[j].BillingType
		}
		return plans[i].PlanID < plans[j].PlanID
	})
	items := make([]map[string]attr.Value, 0, len(plans))
	for _, pl := range plans {
		items = append(items, map[string]attr.Value{
			"plan_id": types.StringValue(pl.PlanID), "plan_code": types.StringValue(pl.PlanCode), "plan_name": types.StringValue(pl.PlanName),
			"billing_type": types.StringValue(lower(pl.BillingType, "BILLING_TYPE_")), "compute_type": types.StringValue(lower(pl.ComputeType, "COMPUTE_TYPE_")),
			"asset_type": types.StringValue(lower(pl.AssetType, "ASSET_TYPE_")), "regions": regionsValue(pl.Regions, &resp.Diagnostics),
			"prices": pricesValue(pl.PlanPrices, &resp.Diagnostics),
		})
	}
	cities := append([]string{}, p.AvailableRegions...)
	sort.Strings(cities)
	m.ID = types.StringValue(p.Provider.OrganizationID)
	m.Name = types.StringValue(p.Provider.OrganizationName)
	m.Description = types.StringValue(p.Provider.Description)
	m.Website = types.StringValue(p.Website)
	m.Rating = types.Float64Value(p.Provider.Rating)
	m.TotalReviews = types.Int64Value(int64(p.Provider.TotalReviews))
	m.PlatformDriver = types.StringValue(lower(p.Provider.PlatformDriver, "PLATFORM_DRIVER_"))
	m.Capabilities = strList(ctx, p.Provider.Capabilities, &resp.Diagnostics)
	m.HasIPv6 = types.BoolValue(p.HasIPv6)
	m.Cities = strList(ctx, cities, &resp.Diagnostics)
	m.Plans = objList(catalogPlanAttrTypes, items, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_marketplace_providers ---

// NewProvidersDataSource returns the noderings_marketplace_providers data source.
func NewProvidersDataSource() datasource.DataSource { return &providersDataSource{} }

type providersDataSource struct{ client *client.Client }

var providerAttrTypes = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "description": types.StringType, "rating": types.Float64Type,
	"total_reviews": types.Int64Type, "platform_driver": types.StringType, "capabilities": types.ListType{ElemType: types.StringType},
}

type providersModel struct {
	AssetType types.String `tfsdk:"asset_type"`
	Search    types.String `tfsdk:"search"`
	Providers types.List   `tfsdk:"providers"`
}

func (d *providersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_providers"
}

func (d *providersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The marketplace's provider directory, sorted by name.",
		Attributes: map[string]schema.Attribute{
			"asset_type": schema.StringAttribute{Optional: true, MarkdownDescription: "Only providers selling `virtual_machine` or `game_server`.",
				Validators: []validator.String{stringvalidator.OneOf("virtual_machine", "game_server")}},
			"search": schema.StringAttribute{Optional: true, MarkdownDescription: "Only providers whose name contains this text."},
			"providers": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Providers.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": computedStr("Provider ID."), "name": computedStr("Name."), "description": computedStr("Description."),
				"rating":          schema.Float64Attribute{Computed: true, MarkdownDescription: "Average rating (0-5)."},
				"total_reviews":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of reviews."},
				"platform_driver": computedStr("Platform."),
				"capabilities":    schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Platform features."},
			}}},
		},
	}
}

func (d *providersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *providersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m providersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	providers, err := d.client.ListProviderDirectory(ctx, assetTypes[m.AssetType.ValueString()], m.Search.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error listing marketplace providers", err.Error())
		return
	}
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].OrganizationName != providers[j].OrganizationName {
			return providers[i].OrganizationName < providers[j].OrganizationName
		}
		return providers[i].OrganizationID < providers[j].OrganizationID
	})
	items := make([]map[string]attr.Value, 0, len(providers))
	for _, p := range providers {
		items = append(items, map[string]attr.Value{
			"id": types.StringValue(p.OrganizationID), "name": types.StringValue(p.OrganizationName), "description": types.StringValue(p.Description),
			"rating": types.Float64Value(p.Rating), "total_reviews": types.Int64Value(int64(p.TotalReviews)),
			"platform_driver": types.StringValue(lower(p.PlatformDriver, "PLATFORM_DRIVER_")), "capabilities": strList(ctx, p.Capabilities, &resp.Diagnostics),
		})
	}
	m.Providers = objList(providerAttrTypes, items, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_marketplace_plan_codes ---

// NewPlanCodesDataSource returns the noderings_marketplace_plan_codes data source.
func NewPlanCodesDataSource() datasource.DataSource { return &planCodesDataSource{} }

type planCodesDataSource struct{ client *client.Client }

var planCodeAttrTypes = map[string]attr.Type{
	"plan_code": types.StringType, "billing_type": types.StringType, "compute_type": types.StringType, "compute_category": types.StringType,
}

type planCodesModel struct {
	Country     types.String `tfsdk:"country"`
	City        types.String `tfsdk:"city"`
	BillingType types.String `tfsdk:"billing_type"`
	AssetType   types.String `tfsdk:"asset_type"`
	PlanCodes   types.List   `tfsdk:"plan_codes"`
}

func (d *planCodesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_plan_codes"
}

func (d *planCodesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Plan codes for sale on the marketplace, sorted by code and billing type. A plan code (for example `SC-GC-2C-4G-80`: " +
			"general compute, 2 vCPUs, 4 GB memory, 80 GB disk) means the same size at every provider.",
		Attributes: map[string]schema.Attribute{
			"country": schema.StringAttribute{Optional: true, MarkdownDescription: "Only codes sold in this country. Needs `city`.",
				Validators: []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("city"))}},
			"city": schema.StringAttribute{Optional: true, MarkdownDescription: "Only codes sold in this city. Needs `country`.",
				Validators: []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("country"))}},
			"billing_type": billingTypeAttr("Only `usage`, `subscription` or `free` plans."),
			"asset_type": schema.StringAttribute{Optional: true, MarkdownDescription: "Only plans for `virtual_machine` or `game_server`.",
				Validators: []validator.String{stringvalidator.OneOf("virtual_machine", "game_server")}},
			"plan_codes": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Plan codes.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"plan_code": computedStr("Plan code."), "billing_type": computedStr("`usage`, `subscription` or `free`."),
				"compute_type": computedStr("Compute type, for example `general_compute`."), "compute_category": computedStr("`shared_compute` or `dedicated_compute`."),
			}}},
		},
	}
}

func (d *planCodesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *planCodesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m planCodesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	codes, err := d.client.ListAvailablePlans(ctx, m.Country.ValueString(), m.City.ValueString(), billingTypes[m.BillingType.ValueString()], assetTypes[m.AssetType.ValueString()])
	if err != nil {
		resp.Diagnostics.AddError("Error listing plan codes", err.Error())
		return
	}
	sort.Slice(codes, func(i, j int) bool {
		if codes[i].PlanCode != codes[j].PlanCode {
			return codes[i].PlanCode < codes[j].PlanCode
		}
		return codes[i].BillingType < codes[j].BillingType
	})
	items := make([]map[string]attr.Value, 0, len(codes))
	for _, c := range codes {
		items = append(items, map[string]attr.Value{
			"plan_code": types.StringValue(c.PlanCode), "billing_type": types.StringValue(lower(c.BillingType, "BILLING_TYPE_")),
			"compute_type": types.StringValue(lower(c.ComputeType, "COMPUTE_TYPE_")), "compute_category": types.StringValue(lower(c.ComputeCategory, "COMPUTE_CATEGORY_")),
		})
	}
	m.PlanCodes = objList(planCodeAttrTypes, items, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_marketplace_plan ---

// NewPlanDataSource returns the noderings_marketplace_plan data source.
func NewPlanDataSource() datasource.DataSource { return &planDataSource{} }

type planDataSource struct{ client *client.Client }

type planModel struct {
	PlanID        types.String  `tfsdk:"plan_id"`
	PlanCode      types.String  `tfsdk:"plan_code"`
	RegionID      types.String  `tfsdk:"region_id"`
	RegionCountry types.String  `tfsdk:"region_country"`
	RegionCity    types.String  `tfsdk:"region_city"`
	ProviderID    types.String  `tfsdk:"provider_id"`
	ProviderName  types.String  `tfsdk:"provider_name"`
	BillingType   types.String  `tfsdk:"billing_type"`
	VCPUs         types.Int64   `tfsdk:"vcpus"`
	RAMGB         types.Int64   `tfsdk:"ram_gb"`
	DiskGB        types.Int64   `tfsdk:"disk_gb"`
	Rating        types.Float64 `tfsdk:"rating"`
	Prices        types.List    `tfsdk:"prices"`
	Regions       types.List    `tfsdk:"regions"`
}

func (d *planDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_plan"
}

func (d *planDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up one marketplace plan by ID. Fails if the plan is no longer for sale.",
		Attributes: map[string]schema.Attribute{
			"plan_id":        schema.StringAttribute{Required: true, MarkdownDescription: "Plan ID.", Validators: []validator.String{framework.UUID()}},
			"plan_code":      computedStr("Plan code."),
			"region_id":      computedStr("Region ID."),
			"region_country": computedStr("Country of the region."),
			"region_city":    computedStr("City of the region."),
			"provider_id":    computedStr("Provider ID."),
			"provider_name":  computedStr("Provider name."),
			"billing_type":   computedStr("`usage`, `subscription` or `free`."),
			"vcpus":          schema.Int64Attribute{Computed: true, MarkdownDescription: "vCPUs."},
			"ram_gb":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Memory in GB."},
			"disk_gb":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Disk in GB."},
			"rating":         schema.Float64Attribute{Computed: true, MarkdownDescription: "Average rating of the provider (0-5)."},
			"prices":         pricesSchema(),
			"regions":        regionsSchema(),
		},
	}
}

func (d *planDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *planDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m planModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := d.client.GetMarketplacePlan(ctx, m.PlanID.ValueString())
	if err != nil {
		detail := err.Error()
		if client.IsNotFound(err) {
			detail += "\n\nThe plan is not for sale (any more). Look up a current one with data.noderings_marketplace_offer."
		}
		resp.Diagnostics.AddError("Error reading marketplace plan", detail)
		return
	}
	m.PlanCode = types.StringValue(p.PlanCode)
	m.RegionID = types.StringValue(p.RegionID)
	m.RegionCountry = types.StringValue(p.RegionCountry)
	m.RegionCity = types.StringValue(p.RegionCity)
	m.ProviderID = types.StringValue(p.OrganizationID)
	m.ProviderName = types.StringValue(p.OrganizationName)
	m.BillingType = types.StringValue(lower(p.BillingType, "BILLING_TYPE_"))
	m.VCPUs = types.Int64Value(int64(p.VCPUs))
	m.RAMGB = types.Int64Value(int64(p.RAMGB))
	m.DiskGB = types.Int64Value(int64(p.DiskGB))
	m.Rating = types.Float64Value(p.Rating)
	m.Prices = pricesValue(p.PlanPrices, &resp.Diagnostics)
	m.Regions = regionsValue(p.PlanRegions, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_marketplace_addon ---

// NewAddonDataSource returns the noderings_marketplace_addon data source.
func NewAddonDataSource() datasource.DataSource { return &addonDataSource{} }

type addonDataSource struct{ client *client.Client }

var addonTypes = map[string]string{"backup": "ADDON_TYPE_BACKUP", "additional_ip": "ADDON_TYPE_ADDITIONAL_IP"}

var addonPriceAttrTypes = map[string]attr.Type{
	"kind": types.StringType, "unit_amount": types.StringType, "unit_name": types.StringType, "currency": types.StringType,
	"billing_period": types.StringType, "pricing_model": types.StringType,
}

type addonModel struct {
	Type                types.String `tfsdk:"type"`
	VirtualMachineID    types.String `tfsdk:"virtual_machine_id"`
	ProviderID          types.String `tfsdk:"provider_id"`
	PlanCode            types.String `tfsdk:"plan_code"`
	BillingType         types.String `tfsdk:"billing_type"`
	Code                types.String `tfsdk:"code"`
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Description         types.String `tfsdk:"description"`
	CompatiblePlanCodes types.List   `tfsdk:"compatible_plan_codes"`
	Prices              types.List   `tfsdk:"prices"`
}

func (d *addonDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_addon"
}

func (d *addonDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Finds an add-on (backups or an additional IP) for a virtual machine or a provider. Add-ons that are not compatible " +
			"with the VM's plan code are skipped. Fails unless exactly one add-on matches; set `code` to choose.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{Required: true, MarkdownDescription: "`backup` or `additional_ip`.",
				Validators: []validator.String{stringvalidator.OneOf("backup", "additional_ip")}},
			"virtual_machine_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Add-ons for this VM. Backup add-ons are only offered on platforms that support backups.",
				Validators: []validator.String{framework.UUID()}},
			"provider_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Add-ons of this provider.", Validators: []validator.String{framework.UUID()}},
			"plan_code": schema.StringAttribute{Optional: true, Computed: true,
				MarkdownDescription: "Only add-ons compatible with this plan code. Defaults to the VM's plan code when `virtual_machine_id` is set."},
			"billing_type": billingTypeAttr("With `provider_id`, only add-ons for `usage`, `subscription` or `free` plans."),
			"code":         schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Add-on code, to choose between several matches."},
			"id":           computedStr("Add-on ID, for example for `noderings_virtual_machine_backup.addon_id`."),
			"name":         computedStr("Name."),
			"description":  computedStr("Description."),
			"compatible_plan_codes": schema.ListAttribute{Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Plan codes the add-on works with; empty means all."},
			"prices": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Prices.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"kind": computedStr("`one_time`, `subscription` or `usage_based`."), "unit_amount": computedStr("Price per unit."),
				"unit_name": computedStr("Unit."), "currency": computedStr("Currency."), "billing_period": computedStr("Billing period."),
				"pricing_model": computedStr("Pricing model."),
			}}},
		},
	}
}

func (d *addonDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("virtual_machine_id"), path.MatchRoot("provider_id"))}
}

func (d *addonDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *addonDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m addonModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planCode := m.PlanCode.ValueString()
	vmID := m.VirtualMachineID.ValueString()
	if vmID != "" && planCode == "" {
		vm, err := d.client.GetVirtualMachine(ctx, vmID)
		if err != nil {
			resp.Diagnostics.AddError("Error reading virtual machine", err.Error())
			return
		}
		planCode = vm.PlanCode
	}
	addons, err := d.client.ListAvailableAddons(ctx, vmID, m.ProviderID.ValueString(), addonTypes[m.Type.ValueString()], billingTypes[m.BillingType.ValueString()])
	if err != nil {
		resp.Diagnostics.AddError("Error listing add-ons", err.Error())
		return
	}
	var matches []client.Addon
	for _, a := range addons {
		// The API maps snapshot add-ons to backups, and does not filter by plan.
		t := a.AddonType
		if t == "ADDON_TYPE_SNAPSHOT" {
			t = "ADDON_TYPE_BACKUP"
		}
		if t != addonTypes[m.Type.ValueString()] {
			continue
		}
		if c := m.Code.ValueString(); c != "" && a.Code != c {
			continue
		}
		if planCode != "" && len(a.CompatiblePlanCodes) > 0 && !hasAll(a.CompatiblePlanCodes, []string{planCode}) {
			continue
		}
		matches = append(matches, a)
	}
	if len(matches) != 1 {
		codes := make([]string, 0, len(matches))
		for _, a := range matches {
			codes = append(codes, a.Code)
		}
		detail := fmt.Sprintf("No %s add-on is available", m.Type.ValueString())
		if len(matches) > 1 {
			detail = fmt.Sprintf("%d %s add-ons match (%s); set code to choose one", len(matches), m.Type.ValueString(), strings.Join(codes, ", "))
		} else if planCode != "" {
			detail += " for plan code " + planCode
		}
		resp.Diagnostics.AddError("Add-on lookup did not match exactly one add-on", detail+".")
		return
	}
	a := matches[0]
	prices := make([]map[string]attr.Value, 0, len(a.AddonPrices))
	for _, p := range a.AddonPrices {
		prices = append(prices, map[string]attr.Value{
			"kind": types.StringValue(lower(p.Kind, "ADDON_PRICE_KIND_")), "unit_amount": types.StringValue(p.UnitAmount),
			"unit_name": types.StringValue(p.UnitName), "currency": types.StringValue(p.Currency),
			"billing_period": types.StringValue(lower(p.BillingPeriod, "BILLING_PERIOD_")), "pricing_model": types.StringValue(lower(p.PricingModel, "PRICING_MODEL_")),
		})
	}
	m.PlanCode = types.StringValue(planCode)
	m.Code = types.StringValue(a.Code)
	m.ID = types.StringValue(a.ID)
	m.Name = types.StringValue(a.Name)
	m.Description = types.StringValue(a.Description)
	m.CompatiblePlanCodes = strList(ctx, a.CompatiblePlanCodes, &resp.Diagnostics)
	m.Prices = objList(addonPriceAttrTypes, prices, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_storefront ---

// NewStorefrontDataSource returns the noderings_storefront data source.
func NewStorefrontDataSource() datasource.DataSource { return &storefrontDataSource{} }

type storefrontDataSource struct{ client *client.Client }

var faqAttrTypes = map[string]attr.Type{"question": types.StringType, "answer": types.StringType}

type storefrontModel struct {
	Slug             types.String `tfsdk:"slug"`
	ProviderID       types.String `tfsdk:"provider_id"`
	OrganizationName types.String `tfsdk:"organization_name"`
	Tagline          types.String `tfsdk:"tagline"`
	AboutHTML        types.String `tfsdk:"about_html"`
	CoverImageURL    types.String `tfsdk:"cover_image_url"`
	LogoImageURL     types.String `tfsdk:"logo_image_url"`
	Highlights       types.List   `tfsdk:"highlights"`
	FeaturedPlanIDs  types.List   `tfsdk:"featured_plan_ids"`
	FAQs             types.List   `tfsdk:"faqs"`
	SocialURLs       types.List   `tfsdk:"social_urls"`
}

func (d *storefrontDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storefront"
}

func (d *storefrontDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	strs := func(desc string) schema.Attribute {
		return schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A provider's public storefront page. Use `noderings_marketplace_provider` for its catalog: plans shown on a storefront " +
			"are not checked for availability.",
		Attributes: map[string]schema.Attribute{
			"slug":              schema.StringAttribute{Required: true, MarkdownDescription: "Storefront slug, for example `acme-hosting`.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"provider_id":       computedStr("Provider ID."),
			"organization_name": computedStr("Provider name."),
			"tagline":           computedStr("Tagline."),
			"about_html":        computedStr("About text, as HTML."),
			"cover_image_url":   computedStr("Cover image."),
			"logo_image_url":    computedStr("Logo."),
			"highlights":        strs("Highlight keys and custom highlight lines."),
			"featured_plan_ids": strs("Plans the provider features."),
			"faqs": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Questions and answers.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"question": computedStr("Question."), "answer": computedStr("Answer."),
			}}},
			"social_urls": strs("Social media links."),
		},
	}
}

func (d *storefrontDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *storefrontDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m storefrontModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sf, err := d.client.GetPublicStorefront(ctx, m.Slug.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading storefront", err.Error())
		return
	}
	faqs := make([]map[string]attr.Value, 0, len(sf.FAQs))
	for _, f := range sf.FAQs {
		faqs = append(faqs, map[string]attr.Value{"question": types.StringValue(f.Question), "answer": types.StringValue(f.Answer)})
	}
	m.ProviderID = types.StringValue(sf.ProviderID)
	m.OrganizationName = types.StringValue(sf.OrganizationName)
	m.Tagline = types.StringValue(sf.Tagline)
	m.AboutHTML = types.StringValue(sf.AboutHTML)
	m.CoverImageURL = types.StringValue(sf.CoverImageURL)
	m.LogoImageURL = types.StringValue(sf.LogoImageURL)
	m.Highlights = strList(ctx, append(append([]string{}, sf.HighlightKeys...), sf.HighlightCustomLines...), &resp.Diagnostics)
	m.FeaturedPlanIDs = strList(ctx, sf.FeaturedPlanIDs, &resp.Diagnostics)
	m.FAQs = objList(faqAttrTypes, faqs, &resp.Diagnostics)
	m.SocialURLs = strList(ctx, sf.SocialURLs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
