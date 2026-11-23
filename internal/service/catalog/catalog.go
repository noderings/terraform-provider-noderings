// Package catalog implements read-only lookups of what NodeRings sells.
package catalog

import (
	"context"
	"sort"

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

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewDistrosDataSource, NewInstantAppDataSource, NewMarketplaceRegionsDataSource, NewInstantAppsDataSource}
}

// --- noderings_distros ---

// NewDistrosDataSource returns the noderings_distros data source.
func NewDistrosDataSource() datasource.DataSource { return &distrosDataSource{} }

type distrosDataSource struct{ client *client.Client }

var distroAttrTypes = map[string]attr.Type{
	"distro":     types.StringType,
	"family":     types.StringType,
	"name":       types.StringType,
	"version":    types.StringType,
	"label":      types.StringType,
	"is_default": types.BoolType,
}

func (d *distrosDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_distros"
}

func (d *distrosDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Operating systems that virtual machines can be created with. Whether a given provider offers one is only known when a VM is created in its region.",
		Attributes: map[string]schema.Attribute{
			"distros": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Available operating systems.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"distro":     schema.StringAttribute{Computed: true, MarkdownDescription: "Value to use in `distro` arguments, for example `ubuntu_24_04`."},
					"family":     schema.StringAttribute{Computed: true, MarkdownDescription: "OS family, for example `ubuntu`."},
					"name":       schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"version":    schema.StringAttribute{Computed: true, MarkdownDescription: "Version."},
					"label":      schema.StringAttribute{Computed: true, MarkdownDescription: "Short label."},
					"is_default": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this is the default choice."},
				}},
			},
		},
	}
}

func (d *distrosDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *distrosDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	distros, err := d.client.ListDistros(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing distros", err.Error())
		return
	}
	items := make([]attr.Value, 0, len(distros))
	for _, x := range distros {
		obj, diags := types.ObjectValue(distroAttrTypes, map[string]attr.Value{
			"distro":     types.StringValue(framework.TrimEnum(x.Distro, "DISTRO_")),
			"family":     types.StringValue(x.Family),
			"name":       types.StringValue(x.Name),
			"version":    types.StringValue(x.Version),
			"label":      types.StringValue(x.Label),
			"is_default": types.BoolValue(x.IsDefault),
		})
		resp.Diagnostics.Append(diags...)
		items = append(items, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: distroAttrTypes}, items)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("distros"), list)...)
}

// --- noderings_instant_app ---

// NewInstantAppDataSource returns the noderings_instant_app data source.
func NewInstantAppDataSource() datasource.DataSource { return &instantAppDataSource{} }

type instantAppDataSource struct{ client *client.Client }

type instantAppModel struct {
	Slug             types.String `tfsdk:"slug"`
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Version          types.String `tfsdk:"version"`
	Family           types.String `tfsdk:"family"`
	Distro           types.String `tfsdk:"distro"`
	Categories       types.List   `tfsdk:"categories"`
	MinVCPUs         types.Int64  `tfsdk:"min_vcpus"`
	MinRAMGB         types.Int64  `tfsdk:"min_ram_gb"`
	MinDiskGB        types.Int64  `tfsdk:"min_disk_gb"`
	DocumentationURL types.String `tfsdk:"documentation_url"`
}

func (d *instantAppDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_instant_app"
}

func (d *instantAppDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an Instant App (a one-click application image) by its slug.",
		Attributes: map[string]schema.Attribute{
			"slug":              schema.StringAttribute{Required: true, MarkdownDescription: "Slug, for example `wordpress`.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"id":                schema.StringAttribute{Computed: true, MarkdownDescription: "Instant App ID."},
			"name":              schema.StringAttribute{Computed: true, MarkdownDescription: "Name."},
			"description":       schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
			"version":           schema.StringAttribute{Computed: true, MarkdownDescription: "Application version."},
			"family":            schema.StringAttribute{Computed: true, MarkdownDescription: "Application family."},
			"distro":            schema.StringAttribute{Computed: true, MarkdownDescription: "Operating system the app installs on, for example `ubuntu_24_04`."},
			"categories":        schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Categories."},
			"min_vcpus":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Recommended minimum vCPUs."},
			"min_ram_gb":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Recommended minimum memory in GB."},
			"min_disk_gb":       schema.Int64Attribute{Computed: true, MarkdownDescription: "Recommended minimum disk in GB."},
			"documentation_url": schema.StringAttribute{Computed: true, MarkdownDescription: "Documentation link."},
		},
	}
}

func (d *instantAppDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *instantAppDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m instantAppModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := d.client.GetInstantAppBySlug(ctx, m.Slug.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading Instant App", err.Error())
		return
	}
	m.ID = types.StringValue(app.ID)
	m.Name = types.StringValue(app.Name)
	m.Description = types.StringValue(app.Description)
	m.Version = types.StringValue(app.Version)
	m.Family = types.StringValue(app.Family)
	m.Distro = types.StringValue(framework.TrimEnum(app.Distro, "DISTRO_"))
	m.Categories = framework.Strings(ctx, app.Categories, &resp.Diagnostics)
	m.MinVCPUs = types.Int64Value(int64(app.MinVCPUs))
	m.MinRAMGB = types.Int64Value(int64(app.MinRAMGB))
	m.MinDiskGB = types.Int64Value(int64(app.MinDiskGB))
	m.DocumentationURL = types.StringValue(app.DocumentationURL)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_marketplace_regions ---

// NewMarketplaceRegionsDataSource returns the noderings_marketplace_regions data source.
func NewMarketplaceRegionsDataSource() datasource.DataSource { return &regionsDataSource{} }

type regionsDataSource struct{ client *client.Client }

var regionAttrTypes = map[string]attr.Type{"country": types.StringType, "city": types.StringType}

var assetTypes = map[string]string{"virtual_machine": "ASSET_TYPE_VM", "game_server": "ASSET_TYPE_GAMESERVER"}

func (d *regionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_marketplace_regions"
}

func (d *regionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Cities where something is for sale on the marketplace, sorted by country and city.",
		Attributes: map[string]schema.Attribute{
			"asset_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only cities selling this: `virtual_machine` or `game_server`. Omit for both.",
				Validators:          []validator.String{stringvalidator.OneOf("virtual_machine", "game_server")},
			},
			"regions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Cities with offers.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"country": schema.StringAttribute{Computed: true, MarkdownDescription: "Country name."},
					"city":    schema.StringAttribute{Computed: true, MarkdownDescription: "City name."},
				}},
			},
		},
	}
}

func (d *regionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *regionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var assetType types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("asset_type"), &assetType)...)
	if resp.Diagnostics.HasError() {
		return
	}
	regions, err := d.client.ListMarketplaceRegions(ctx, assetTypes[assetType.ValueString()])
	if err != nil {
		resp.Diagnostics.AddError("Error listing marketplace regions", err.Error())
		return
	}
	// The API returns regions in no particular order; sort for stable plans.
	sort.Slice(regions, func(i, j int) bool {
		if regions[i].Country != regions[j].Country {
			return regions[i].Country < regions[j].Country
		}
		return regions[i].City < regions[j].City
	})
	items := make([]attr.Value, 0, len(regions))
	for _, r := range regions {
		obj, diags := types.ObjectValue(regionAttrTypes, map[string]attr.Value{"country": types.StringValue(r.Country), "city": types.StringValue(r.City)})
		resp.Diagnostics.Append(diags...)
		items = append(items, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: regionAttrTypes}, items)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("asset_type"), assetType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("regions"), list)...)
}
