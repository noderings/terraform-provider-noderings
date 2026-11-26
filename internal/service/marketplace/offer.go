// Package marketplace implements read-only lookups of the NodeRings
// marketplace: offers, providers, plans, add-ons and storefronts.
package marketplace

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewOfferDataSource, NewOffersDataSource, NewProviderDataSource, NewProvidersDataSource,
		NewPlanCodesDataSource, NewPlanDataSource, NewAddonDataSource, NewStorefrontDataSource,
	}
}

// --- shared helpers ---

var billingTypes = map[string]string{"usage": "BILLING_TYPE_USAGE", "subscription": "BILLING_TYPE_SUBSCRIPTION", "free": "BILLING_TYPE_FREE"}

var assetTypes = map[string]string{"virtual_machine": "ASSET_TYPE_VM", "game_server": "ASSET_TYPE_GAMESERVER"}

func lower(v, prefix string) string { return strings.ToLower(strings.TrimPrefix(v, prefix)) }

func billingTypeAttr(desc string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, MarkdownDescription: desc, Validators: []validator.String{stringvalidator.OneOf("usage", "subscription", "free")}}
}

var priceAttrTypes = map[string]attr.Type{
	"billing_period": types.StringType, "pricing_model": types.StringType, "unit_amount": types.StringType,
	"unit_name": types.StringType, "currency": types.StringType, "minimum_charge": types.StringType,
}

func pricesSchema() schema.ListNestedAttribute {
	str := func(d string) schema.Attribute { return schema.StringAttribute{Computed: true, MarkdownDescription: d} }
	return schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Prices.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
		"billing_period": str("`hour`, `day`, `week`, `month`, `quarter` or `year`."),
		"pricing_model":  str("`fixed` or `usage_based` (and other models for special plans)."),
		"unit_amount":    str("Price per unit, as a decimal string."),
		"unit_name":      str("Unit, for example `hour`."),
		"currency":       str("Currency code."),
		"minimum_charge": str("Minimum charge, as a decimal string."),
	}}}
}

func pricesValue(prices []client.PlanPrice, diags *diag.Diagnostics) types.List {
	items := make([]attr.Value, 0, len(prices))
	for _, p := range prices {
		o, d := types.ObjectValue(priceAttrTypes, map[string]attr.Value{
			"billing_period": types.StringValue(lower(p.BillingPeriod, "BILLING_PERIOD_")),
			"pricing_model":  types.StringValue(lower(p.PricingModel, "PRICING_MODEL_")),
			"unit_amount":    types.StringValue(p.UnitAmount), "unit_name": types.StringValue(p.UnitName),
			"currency": types.StringValue(p.Currency), "minimum_charge": types.StringValue(p.MinimumCharge),
		})
		diags.Append(d...)
		items = append(items, o)
	}
	l, d := types.ListValue(types.ObjectType{AttrTypes: priceAttrTypes}, items)
	diags.Append(d...)
	return l
}

func strList(ctx context.Context, in []string, diags *diag.Diagnostics) types.List {
	return framework.Strings(ctx, in, diags)
}

// lowestPrice is the cheapest unit amount, used to sort offers.
func lowestPrice(prices []client.PlanPrice) float64 {
	best := -1.0
	for _, p := range prices {
		if v, err := strconv.ParseFloat(p.UnitAmount, 64); err == nil && (best < 0 || v < best) {
			best = v
		}
	}
	if best < 0 {
		return 1e18
	}
	return best
}

func hasAll(have, want []string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// offer is one candidate for noderings_marketplace_offer(s).
type offer struct {
	providerID, providerName, planID, regionID, driver, billingType string
	caps                                                            []string
	ipv6                                                            bool
	rating                                                          float64
	reviews                                                         int32
	prices                                                          []client.PlanPrice
}

type offerQuery struct {
	PlanCode            types.String `tfsdk:"plan_code"`
	Country             types.String `tfsdk:"country"`
	City                types.String `tfsdk:"city"`
	BillingType         types.String `tfsdk:"billing_type"`
	AssetType           types.String `tfsdk:"asset_type"`
	ProviderID          types.String `tfsdk:"provider_id"`
	StorefrontSlug      types.String `tfsdk:"storefront_slug"`
	ProviderName        types.String `tfsdk:"provider_name"`
	RequireIPv6         types.Bool   `tfsdk:"require_ipv6"`
	RequireCapabilities types.Set    `tfsdk:"require_capabilities"`
	Sort                types.String `tfsdk:"sort"`
}

func queryAttrs(single bool) map[string]schema.Attribute {
	providerDesc := "Only offers from this provider. Conflicts with `storefront_slug` and `provider_name`."
	nameDesc := "Only offers from the provider with this name, for example `Acme Hosting` (case-insensitive, surrounding spaces ignored). " +
		"Fails if more than one provider has the name; give `provider_id` or `storefront_slug` then. Conflicts with `provider_id` and `storefront_slug`."
	if single {
		providerDesc += " Afterwards, the chosen provider."
		nameDesc += " Afterwards, the chosen provider's name."
	}
	return map[string]schema.Attribute{
		"plan_code": schema.StringAttribute{Required: true, MarkdownDescription: "Platform plan code, for example `SC-GC-2C-4G-80`. See `data.noderings_marketplace_plan_codes`.",
			Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
		"country": schema.StringAttribute{Required: true, MarkdownDescription: "Country, for example `Germany` (case-insensitive). See `data.noderings_marketplace_regions`.",
			Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
		"city": schema.StringAttribute{Required: true, MarkdownDescription: "City, for example `Frankfurt` (case-insensitive).",
			Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
		"billing_type": schema.StringAttribute{Optional: true, Computed: single,
			MarkdownDescription: "`usage` (billed hourly from credit), `subscription` (billed monthly in advance) or `free`. Strongly recommended: without it each provider offers one arbitrary plan for the code.",
			Validators:          []validator.String{stringvalidator.OneOf("usage", "subscription", "free")}},
		"asset_type": schema.StringAttribute{Optional: true, MarkdownDescription: "`virtual_machine` (default) or `game_server`. Only checked when the provider is known (`provider_id` or `storefront_slug`).",
			Validators: []validator.String{stringvalidator.OneOf("virtual_machine", "game_server")}},
		"provider_id": schema.StringAttribute{Optional: true, Computed: single, MarkdownDescription: providerDesc, Validators: []validator.String{framework.UUID()}},
		"storefront_slug": schema.StringAttribute{Optional: true, MarkdownDescription: "Only offers from the provider with this public storefront, for example `acme-hosting`. Conflicts with `provider_id` and `provider_name`.",
			Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
		"provider_name": schema.StringAttribute{Optional: true, Computed: single, MarkdownDescription: nameDesc,
			Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be blank")}},
		"require_ipv6": schema.BoolAttribute{Optional: true, MarkdownDescription: "Only offers with IPv6 in the city."},
		"require_capabilities": schema.SetAttribute{Optional: true, ElementType: types.StringType,
			MarkdownDescription: "Only providers whose platform supports all of these, for example `guest_firewall`, `on_demand_backup`, `snapshots` or `instant_apps`.",
			Validators:          []validator.Set{setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))}},
		"sort": schema.StringAttribute{Optional: true, MarkdownDescription: "`price` (cheapest first, the default) or `rating` (best rated first).",
			Validators: []validator.String{stringvalidator.OneOf("price", "rating")}},
	}
}

func queryValidators() []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.Conflicting(path.MatchRoot("provider_id"), path.MatchRoot("storefront_slug"), path.MatchRoot("provider_name"))}
}

// findOffers returns the offers matching q, sorted, and how many matched the
// location before the client-side filters.
func findOffers(ctx context.Context, c *client.Client, q offerQuery, diags *diag.Diagnostics) ([]offer, int, error) {
	code, country, city := q.PlanCode.ValueString(), q.Country.ValueString(), q.City.ValueString()
	billing := billingTypes[q.BillingType.ValueString()]
	asset := assetTypes[q.AssetType.ValueString()]
	if asset == "" {
		asset = "ASSET_TYPE_VM"
	}
	providerID := q.ProviderID.ValueString()
	if q.ProviderID.IsUnknown() {
		providerID = ""
	}
	if slug := q.StorefrontSlug.ValueString(); slug != "" {
		sf, err := c.GetPublicStorefront(ctx, slug)
		if err != nil {
			return nil, 0, fmt.Errorf("looking up storefront %q: %w", slug, err)
		}
		providerID = sf.ProviderID
	}
	var all []offer
	if providerID != "" {
		p, err := c.GetProvider(ctx, providerID)
		if err != nil {
			return nil, 0, fmt.Errorf("reading provider %s: %w", providerID, err)
		}
		for _, pl := range p.AvailablePlans {
			if pl.PlanCode != code || (billing != "" && pl.BillingType != billing) || (pl.AssetType != "" && pl.AssetType != asset) {
				continue
			}
			for _, r := range pl.Regions {
				if strings.EqualFold(r.Country, country) && strings.EqualFold(r.City, city) {
					all = append(all, offer{providerID: p.Provider.OrganizationID, providerName: p.Provider.OrganizationName, planID: pl.PlanID,
						regionID: r.RegionID, driver: p.Provider.PlatformDriver, billingType: pl.BillingType, caps: p.Provider.Capabilities,
						ipv6: p.HasIPv6, rating: p.Provider.Rating, reviews: p.Provider.TotalReviews, prices: pl.PlanPrices})
				}
			}
		}
	} else {
		providers, err := c.ListProviders(ctx, client.ListProvidersFilter{PlanCode: code, Country: country, City: city, BillingType: billing, IPv6Only: q.RequireIPv6.ValueBool()})
		if err != nil {
			return nil, 0, fmt.Errorf("listing offers: %w", err)
		}
		for _, p := range providers {
			o := offer{providerID: p.OrganizationID, providerName: p.OrganizationName, planID: p.PlanID, driver: p.PlatformDriver, billingType: billing,
				caps: p.Capabilities, ipv6: p.IPv6AvailableInFilteredRegion, rating: p.Rating, reviews: p.TotalReviews, prices: p.PlanPrices}
			for _, r := range p.PlanRegions {
				if strings.EqualFold(r.City, city) && strings.EqualFold(r.Country, country) {
					o.regionID = r.RegionID
				}
			}
			if o.regionID == "" && len(p.PlanRegions) > 0 {
				o.regionID = p.PlanRegions[0].RegionID
			}
			all = append(all, o)
		}
	}
	if name := configuredProviderName(q); name != "" {
		named, err := filterByProviderName(all, name)
		if err != nil {
			return nil, 0, err
		}
		all = named
	}
	located := len(all)
	caps := []string{}
	if !q.RequireCapabilities.IsNull() && !q.RequireCapabilities.IsUnknown() {
		diags.Append(q.RequireCapabilities.ElementsAs(ctx, &caps, false)...)
	}
	var out []offer
	for _, o := range all {
		if (q.RequireIPv6.ValueBool() && !o.ipv6) || !hasAll(o.caps, caps) {
			continue
		}
		out = append(out, o)
	}
	byRating := q.Sort.ValueString() == "rating"
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if byRating && a.rating != b.rating {
			return a.rating > b.rating
		}
		if pa, pb := lowestPrice(a.prices), lowestPrice(b.prices); pa != pb {
			return pa < pb
		}
		if !byRating && a.rating != b.rating {
			return a.rating > b.rating
		}
		if a.providerName != b.providerName {
			return a.providerName < b.providerName
		}
		return a.planID < b.planID
	})
	return out, located, nil
}

// configuredProviderName is the trimmed provider_name filter, or "" for none.
func configuredProviderName(q offerQuery) string {
	if q.ProviderName.IsNull() || q.ProviderName.IsUnknown() {
		return ""
	}
	return strings.TrimSpace(q.ProviderName.ValueString())
}

// filterByProviderName keeps the offers from the provider called name,
// ignoring case and surrounding spaces. It fails when offers from more than
// one provider have that name, rather than picking one of them.
func filterByProviderName(offers []offer, name string) ([]offer, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	var out []offer
	ids := map[string]bool{}
	for _, o := range offers {
		if strings.ToLower(strings.TrimSpace(o.providerName)) == want {
			out = append(out, o)
			ids[o.providerID] = true
		}
	}
	if len(ids) > 1 {
		return nil, fmt.Errorf("more than one provider is named %s; give its provider_id or storefront_slug instead", strings.TrimSpace(name))
	}
	return out, nil
}

// citiesSelling lists "City (Country)" for every place the plan code is sold,
// for error messages. Errors are ignored: this only improves a message.
func citiesSelling(ctx context.Context, c *client.Client, code, asset string) []string {
	providers, err := c.ListProviderDirectory(ctx, asset, "")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for i, p := range providers {
		if i >= 50 {
			break
		}
		d, err := c.GetProvider(ctx, p.OrganizationID)
		if err != nil {
			continue
		}
		for _, pl := range d.AvailablePlans {
			if pl.PlanCode != code {
				continue
			}
			for _, r := range pl.Regions {
				k := r.City + " (" + r.Country + ")"
				if !seen[k] {
					seen[k] = true
					out = append(out, k)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func noOfferError(ctx context.Context, c *client.Client, q offerQuery, located int) string {
	code := q.PlanCode.ValueString()
	msg := fmt.Sprintf("No offer for plan code %s in %s, %s", code, q.City.ValueString(), q.Country.ValueString())
	if name := configuredProviderName(q); name != "" {
		msg += " from provider " + name
	}
	if b := q.BillingType.ValueString(); b != "" {
		msg += " with billing type " + b
	}
	msg += "."
	if located > 0 {
		msg += fmt.Sprintf(" %d offer(s) matched the location but not require_ipv6 or require_capabilities.", located)
	}
	asset := assetTypes[q.AssetType.ValueString()]
	if asset == "" {
		asset = "ASSET_TYPE_VM"
	}
	if cities := citiesSelling(ctx, c, code, asset); len(cities) > 0 {
		msg += " The plan code is sold in: " + strings.Join(cities, ", ") + "."
	} else {
		msg += " The plan code is not sold anywhere right now; see data.noderings_marketplace_plan_codes for codes that are."
	}
	return msg
}

// --- noderings_marketplace_offer ---

// NewOfferDataSource returns the noderings_marketplace_offer data source.
func NewOfferDataSource() datasource.DataSource { return &offerDataSource{} }

type offerDataSource struct{ client *client.Client }

type offerModel struct {
	offerQuery
	PlanID         types.String  `tfsdk:"plan_id"`
	RegionID       types.String  `tfsdk:"region_id"`
	PlatformDriver types.String  `tfsdk:"platform_driver"`
	Capabilities   types.List    `tfsdk:"capabilities"`
	IPv6Available  types.Bool    `tfsdk:"ipv6_available"`
	Rating         types.Float64 `tfsdk:"rating"`
	Prices         types.List    `tfsdk:"prices"`
	VCPUs          types.Int64   `tfsdk:"vcpus"`
	RAMGB          types.Int64   `tfsdk:"ram_gb"`
	DiskGB         types.Int64   `tfsdk:"disk_gb"`
}

func (d *offerDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_offer"
}

func (d *offerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := queryAttrs(true)
	attrs["plan_id"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Plan ID to use as `plan_id` of a VM or game server."}
	attrs["region_id"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Region ID to use as `region_id`."}
	attrs["platform_driver"] = schema.StringAttribute{Computed: true, MarkdownDescription: "Platform, for example `proxmox`, `virtfusion` or `solusvm`."}
	attrs["capabilities"] = schema.ListAttribute{Computed: true, ElementType: types.StringType,
		MarkdownDescription: "Features the platform supports, for example `guest_firewall` (firewall), `on_demand_backup` (backups), `snapshots` and `instant_apps`."}
	attrs["ipv6_available"] = schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether IPv6 is available in the city."}
	attrs["rating"] = schema.Float64Attribute{Computed: true, MarkdownDescription: "Average customer rating of the provider (0-5)."}
	attrs["prices"] = pricesSchema()
	attrs["vcpus"] = schema.Int64Attribute{Computed: true, MarkdownDescription: "vCPUs."}
	attrs["ram_gb"] = schema.Int64Attribute{Computed: true, MarkdownDescription: "Memory in GB."}
	attrs["disk_gb"] = schema.Int64Attribute{Computed: true, MarkdownDescription: "Disk in GB."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Resolves a plan code and a city into the `plan_id` and `region_id` needed to create a virtual machine or game server, " +
			"choosing the cheapest (or best rated) matching offer. Plan and region IDs belong to one provider and can change, so look them up " +
			"with this data source instead of hard-coding them. An offer can disappear between plan and apply when a provider runs out of capacity.",
		Attributes: attrs,
	}
}

func (d *offerDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return queryValidators()
}

func (d *offerDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *offerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m offerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	offers, located, err := findOffers(ctx, d.client, m.offerQuery, &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Error looking up marketplace offers", err.Error())
		return
	}
	if len(offers) == 0 {
		resp.Diagnostics.AddError("No matching marketplace offer", noOfferError(ctx, d.client, m.offerQuery, located))
		return
	}
	o := offers[0]
	// Confirm the plan is still for sale and read its size.
	plan, err := d.client.GetMarketplacePlan(ctx, o.planID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading marketplace plan", fmt.Sprintf("Offer %s from %s: %v", o.planID, o.providerName, err))
		return
	}
	m.PlanID = types.StringValue(plan.PlanID)
	m.RegionID = types.StringValue(o.regionID)
	m.ProviderID = types.StringValue(o.providerID)
	if m.ProviderName.IsNull() {
		// Keep a configured name as written; it matched without regard to case.
		m.ProviderName = types.StringValue(o.providerName)
	}
	m.BillingType = types.StringValue(lower(plan.BillingType, "BILLING_TYPE_"))
	m.PlatformDriver = types.StringValue(lower(o.driver, "PLATFORM_DRIVER_"))
	m.Capabilities = strList(ctx, o.caps, &resp.Diagnostics)
	m.IPv6Available = types.BoolValue(o.ipv6)
	m.Rating = types.Float64Value(o.rating)
	prices := plan.PlanPrices
	if len(prices) == 0 {
		prices = o.prices
	}
	m.Prices = pricesValue(prices, &resp.Diagnostics)
	m.VCPUs = types.Int64Value(int64(plan.VCPUs))
	m.RAMGB = types.Int64Value(int64(plan.RAMGB))
	m.DiskGB = types.Int64Value(int64(plan.DiskGB))
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_marketplace_offers ---

// NewOffersDataSource returns the noderings_marketplace_offers data source.
func NewOffersDataSource() datasource.DataSource { return &offersDataSource{} }

type offersDataSource struct{ client *client.Client }

type offersModel struct {
	offerQuery
	Offers types.List `tfsdk:"offers"`
}

var offerAttrTypes = map[string]attr.Type{
	"provider_id": types.StringType, "provider_name": types.StringType, "plan_id": types.StringType, "region_id": types.StringType,
	"platform_driver": types.StringType, "capabilities": types.ListType{ElemType: types.StringType}, "ipv6_available": types.BoolType,
	"rating": types.Float64Type, "total_reviews": types.Int64Type, "prices": types.ListType{ElemType: types.ObjectType{AttrTypes: priceAttrTypes}},
}

func (d *offersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_offers"
}

func (d *offersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := queryAttrs(false)
	str := func(desc string) schema.Attribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	attrs["offers"] = schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Matching offers, best first.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
		"provider_id": str("Provider ID."), "provider_name": str("Provider name."), "plan_id": str("Plan ID."), "region_id": str("Region ID."),
		"platform_driver": str("Platform."), "capabilities": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Platform features."},
		"ipv6_available": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether IPv6 is available."},
		"rating":         schema.Float64Attribute{Computed: true, MarkdownDescription: "Average rating (0-5)."},
		"total_reviews":  schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of reviews."},
		"prices":         pricesSchema(),
	}}}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every offer for a plan code in a city, sorted by price or rating, for choosing in configuration. " +
			"Use `noderings_marketplace_offer` to pick one automatically.",
		Attributes: attrs,
	}
}

func (d *offersDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return queryValidators()
}

func (d *offersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *offersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m offersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	offers, _, err := findOffers(ctx, d.client, m.offerQuery, &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Error looking up marketplace offers", err.Error())
		return
	}
	items := make([]attr.Value, 0, len(offers))
	for _, o := range offers {
		obj, diags := types.ObjectValue(offerAttrTypes, map[string]attr.Value{
			"provider_id": types.StringValue(o.providerID), "provider_name": types.StringValue(o.providerName),
			"plan_id": types.StringValue(o.planID), "region_id": types.StringValue(o.regionID),
			"platform_driver": types.StringValue(lower(o.driver, "PLATFORM_DRIVER_")), "capabilities": strList(ctx, o.caps, &resp.Diagnostics),
			"ipv6_available": types.BoolValue(o.ipv6), "rating": types.Float64Value(o.rating), "total_reviews": types.Int64Value(int64(o.reviews)),
			"prices": pricesValue(o.prices, &resp.Diagnostics),
		})
		resp.Diagnostics.Append(diags...)
		items = append(items, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: offerAttrTypes}, items)
	resp.Diagnostics.Append(diags...)
	m.Offers = list
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
