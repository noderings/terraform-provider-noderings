package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// --- noderings_instant_apps ---

var distroRE = regexp.MustCompile(`^[a-z]+_[0-9_]+$`)

// NewInstantAppsDataSource returns the noderings_instant_apps data source.
func NewInstantAppsDataSource() datasource.DataSource { return &instantAppsDataSource{} }

type instantAppsDataSource struct{ client *client.Client }

var appVariableAttrTypes = map[string]attr.Type{
	"name": types.StringType, "label": types.StringType, "type": types.StringType, "required": types.BoolType, "default": types.StringType,
}

var instantAppAttrTypes = map[string]attr.Type{
	"id": types.StringType, "slug": types.StringType, "name": types.StringType, "description": types.StringType,
	"version": types.StringType, "family": types.StringType, "distro": types.StringType,
	"categories": types.ListType{ElemType: types.StringType}, "min_vcpus": types.Int64Type, "min_ram_gb": types.Int64Type,
	"min_disk_gb": types.Int64Type, "documentation_url": types.StringType,
	"variables": types.ListType{ElemType: types.ObjectType{AttrTypes: appVariableAttrTypes}},
}

type instantAppsModel struct {
	Query       types.String `tfsdk:"query"`
	Categories  types.List   `tfsdk:"categories"`
	Distro      types.String `tfsdk:"distro"`
	InstantApps types.List   `tfsdk:"instant_apps"`
}

func (d *instantAppsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_instant_apps"
}

func (d *instantAppsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.Attribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	num := func(desc string) schema.Attribute {
		return schema.Int64Attribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Instant Apps (one-click application images), with the variables each accepts in `instant_app_variables` of `noderings_virtual_machine`.",
		Attributes: map[string]schema.Attribute{
			"query": schema.StringAttribute{Optional: true, MarkdownDescription: "Only apps whose name or slug contains this text (case-insensitive)."},
			"categories": schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Only apps in all of these categories.",
				Validators: []validator.List{listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))}},
			"distro": schema.StringAttribute{Optional: true, MarkdownDescription: "Only apps for this operating system, for example `ubuntu_24_04`.",
				Validators: []validator.String{stringvalidator.RegexMatches(distroRE, "must be a distro such as ubuntu_24_04")}},
			"instant_apps": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Matching apps, in catalog order.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": str("Instant App ID, for `instant_app_id`."), "slug": str("Slug."), "name": str("Name."), "description": str("Description."),
				"version": str("Application version."), "family": str("Application family."), "distro": str("Operating system the app installs on."),
				"categories":        schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Categories."},
				"min_vcpus":         num("Recommended minimum vCPUs."),
				"min_ram_gb":        num("Recommended minimum memory in GB."),
				"min_disk_gb":       num("Recommended minimum disk in GB."),
				"documentation_url": str("Documentation link."),
				"variables": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Variables the app accepts.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name": str("Name, the key in `instant_app_variables`."), "label": str("Label."), "type": str("Input type, for example `string`."),
					"required": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether a value is required."},
					"default":  str("Default value, if any."),
				}}},
			}}},
		},
	}
}

func (d *instantAppsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func appVariables(md map[string]any, diags *diag.Diagnostics) types.List {
	var items []attr.Value
	if list, ok := md["variables"].([]any); ok {
		for _, v := range list {
			vm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			s := func(k string) types.String {
				if x, ok := vm[k]; ok && x != nil {
					return types.StringValue(fmt.Sprint(x))
				}
				return types.StringNull()
			}
			req, _ := vm["required"].(bool)
			o, d := types.ObjectValue(appVariableAttrTypes, map[string]attr.Value{
				"name": s("name"), "label": s("label"), "type": s("type"), "required": types.BoolValue(req), "default": s("default"),
			})
			diags.Append(d...)
			items = append(items, o)
		}
	}
	l, d := types.ListValue(types.ObjectType{AttrTypes: appVariableAttrTypes}, items)
	diags.Append(d...)
	return l
}

func (d *instantAppsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m instantAppsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var cats []string
	if !m.Categories.IsNull() {
		resp.Diagnostics.Append(m.Categories.ElementsAs(ctx, &cats, false)...)
	}
	distro := ""
	if v := m.Distro.ValueString(); v != "" {
		distro = "DISTRO_" + strings.ToUpper(v)
	}
	apps, err := d.client.ListInstantApps(ctx, m.Query.ValueString(), cats, distro)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Instant Apps", err.Error())
		return
	}
	items := make([]attr.Value, 0, len(apps))
	for _, a := range apps {
		o, diags := types.ObjectValue(instantAppAttrTypes, map[string]attr.Value{
			"id": types.StringValue(a.ID), "slug": types.StringValue(a.Slug), "name": types.StringValue(a.Name),
			"description": types.StringValue(a.Description), "version": types.StringValue(a.Version), "family": types.StringValue(a.Family),
			"distro": types.StringValue(framework.TrimEnum(a.Distro, "DISTRO_")), "categories": framework.Strings(ctx, a.Categories, &resp.Diagnostics),
			"min_vcpus": types.Int64Value(int64(a.MinVCPUs)), "min_ram_gb": types.Int64Value(int64(a.MinRAMGB)),
			"min_disk_gb": types.Int64Value(int64(a.MinDiskGB)), "documentation_url": types.StringValue(a.DocumentationURL),
			"variables": appVariables(a.Metadata, &resp.Diagnostics),
		})
		resp.Diagnostics.Append(diags...)
		items = append(items, o)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: instantAppAttrTypes}, items)
	resp.Diagnostics.Append(diags...)
	m.InstantApps = list
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
