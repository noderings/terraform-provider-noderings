package firewall

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = &ruleSetResource{}
	_ resource.ResourceWithImportState = &ruleSetResource{}
)

// NewRuleSetResource returns the noderings_firewall_ruleset resource.
func NewRuleSetResource() resource.Resource { return &ruleSetResource{} }

type ruleSetResource struct{ client *client.Client }

type ruleSetModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	ForceDestroy        types.Bool   `tfsdk:"force_destroy"`
	RuleCount           types.Int64  `tfsdk:"rule_count"`
	AttachedVMCount     types.Int64  `tfsdk:"attached_vm_count"`
	AttachedVMHostnames types.List   `tfsdk:"attached_vm_hostnames"`
	CreatedAt           types.String `tfsdk:"created_at"`
}

func (r *ruleSetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_ruleset"
}

func (r *ruleSetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A firewall ruleset: a named, ordered list of rules that can be attached to virtual machines. " +
			"Manage its rules with `noderings_firewall_ruleset_rules`. A new ruleset is empty, and an empty ruleset filters nothing: " +
			"the default policy is to accept traffic in both directions. Only virtual machines on Proxmox support firewall rulesets.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Ruleset ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name, 1 to 63 characters, unique in the organization.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 63), framework.SafeString()},
			},
			"force_destroy": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Delete the ruleset even while virtual machines use it. The API then detaches it from those virtual machines, " +
					"and the rules last applied to them may stay in effect until they get another ruleset. " +
					"With the default `false`, Terraform refuses to delete an attached ruleset; detach it first.",
			},
			"rule_count":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of rules, ingress and egress together."},
			"attached_vm_count": schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of virtual machines the ruleset is attached to."},
			"attached_vm_hostnames": schema.ListAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Hostnames of the virtual machines the ruleset is attached to, sorted.",
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
		},
	}
}

func (r *ruleSetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func setRuleSet(ctx context.Context, m *ruleSetModel, rs *client.FirewallRuleSet, diags *diag.Diagnostics) {
	m.ID = types.StringValue(rs.ID)
	m.Name = types.StringValue(rs.Name)
	m.RuleCount = types.Int64Value(rs.RuleCount)
	m.AttachedVMCount = types.Int64Value(rs.AttachedVMCount)
	hosts := append([]string{}, rs.AttachedVMHostnames...)
	sort.Strings(hosts)
	m.AttachedVMHostnames = framework.Strings(ctx, hosts, diags)
	m.CreatedAt = framework.Time(rs.Timestamps.CreateTime)
}

func ruleSetWriteError(err error, name string) string {
	detail := err.Error()
	if client.IsAlreadyExists(err) {
		detail += "\n\nThe organization already has a firewall ruleset named " + name +
			". Choose another name, or import the existing ruleset with: terraform import <address> <ruleset id>"
	}
	return detail
}

func (r *ruleSetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ruleSetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rs, err := r.client.CreateFirewallRuleSet(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating firewall ruleset", ruleSetWriteError(err, plan.Name.ValueString()))
		return
	}
	setRuleSet(ctx, &plan, rs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ruleSetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state ruleSetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rs, err := r.client.GetFirewallRuleSet(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall ruleset", err.Error())
		return
	}
	if state.ForceDestroy.IsNull() {
		state.ForceDestroy = types.BoolValue(false)
	}
	setRuleSet(ctx, &state, rs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *ruleSetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ruleSetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		rs  *client.FirewallRuleSet
		err error
	)
	if plan.Name.ValueString() != state.Name.ValueString() {
		rs, err = r.client.UpdateFirewallRuleSet(ctx, state.ID.ValueString(), plan.Name.ValueString())
	} else {
		rs, err = r.client.GetFirewallRuleSet(ctx, state.ID.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating firewall ruleset", ruleSetWriteError(err, plan.Name.ValueString()))
		return
	}
	setRuleSet(ctx, &plan, rs, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *ruleSetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ruleSetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rs, err := r.client.GetFirewallRuleSet(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall ruleset", err.Error())
		return
	}
	force := state.ForceDestroy.ValueBool()
	if rs.AttachedVMCount > 0 && !force {
		resp.Diagnostics.AddError("Firewall ruleset is attached to virtual machines",
			fmt.Sprintf("Ruleset %s is attached to %d virtual machine(s): %s. Detach it first (remove the noderings_firewall_ruleset_attachment resources that use it), "+
				"or set force_destroy = true and apply before destroying. Deleting an attached ruleset detaches it, but the rules last applied may stay in effect on those virtual machines.",
				rs.Name, rs.AttachedVMCount, strings.Join(rs.AttachedVMHostnames, ", ")))
		return
	}
	unlock := lockRuleSet(rs.ID)
	defer unlock()
	if err := r.client.DeleteFirewallRuleSet(ctx, rs.ID, force); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting firewall ruleset", err.Error())
	}
}

func (r *ruleSetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- noderings_firewall_ruleset data source ---

// NewRuleSetDataSource returns the noderings_firewall_ruleset data source.
func NewRuleSetDataSource() datasource.DataSource { return &ruleSetDataSource{} }

type ruleSetDataSource struct{ client *client.Client }

type ruleSetDataModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	RuleCount           types.Int64  `tfsdk:"rule_count"`
	AttachedVMCount     types.Int64  `tfsdk:"attached_vm_count"`
	AttachedVMHostnames types.List   `tfsdk:"attached_vm_hostnames"`
	CreatedAt           types.String `tfsdk:"created_at"`
}

func ruleSetDataAttributes(lookup bool) map[string]dsschema.Attribute {
	return map[string]dsschema.Attribute{
		"id":                    dsschema.StringAttribute{Optional: lookup, Computed: true, MarkdownDescription: "Ruleset ID."},
		"name":                  dsschema.StringAttribute{Optional: lookup, Computed: true, MarkdownDescription: "Name (exact, case-sensitive)."},
		"rule_count":            dsschema.Int64Attribute{Computed: true, MarkdownDescription: "Number of rules, ingress and egress together."},
		"attached_vm_count":     dsschema.Int64Attribute{Computed: true, MarkdownDescription: "Number of virtual machines the ruleset is attached to."},
		"attached_vm_hostnames": dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Hostnames of the attached virtual machines, sorted."},
		"created_at":            dsschema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
	}
}

func ruleSetData(ctx context.Context, rs *client.FirewallRuleSet, diags *diag.Diagnostics) ruleSetDataModel {
	var m ruleSetModel
	setRuleSet(ctx, &m, rs, diags)
	return ruleSetDataModel{ID: m.ID, Name: m.Name, RuleCount: m.RuleCount, AttachedVMCount: m.AttachedVMCount,
		AttachedVMHostnames: m.AttachedVMHostnames, CreatedAt: m.CreatedAt}
}

func (d *ruleSetDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_ruleset"
}

func (d *ruleSetDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Looks up a firewall ruleset by ID or exact name.",
		Attributes:          ruleSetDataAttributes(true),
	}
}

func (d *ruleSetDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *ruleSetDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *ruleSetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ruleSetDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		rs  *client.FirewallRuleSet
		err error
	)
	if id := cfg.ID.ValueString(); id != "" {
		rs, err = d.client.GetFirewallRuleSet(ctx, id)
	} else {
		rs, err = d.client.FindFirewallRuleSet(ctx, cfg.Name.ValueString())
	}
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Firewall ruleset not found", err.Error())
			return
		}
		resp.Diagnostics.AddError("Error reading firewall ruleset", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, ruleSetData(ctx, rs, &resp.Diagnostics))...)
}

// --- noderings_firewall_rulesets data source ---

// NewRuleSetsDataSource returns the noderings_firewall_rulesets data source.
func NewRuleSetsDataSource() datasource.DataSource { return &ruleSetsDataSource{} }

type ruleSetsDataSource struct{ client *client.Client }

type ruleSetsDataModel struct {
	Attached types.Bool         `tfsdk:"attached"`
	RuleSets []ruleSetDataModel `tfsdk:"rulesets"`
}

func (d *ruleSetsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_rulesets"
}

func (d *ruleSetsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Lists the organization's firewall rulesets, sorted by name.",
		Attributes: map[string]dsschema.Attribute{
			"attached": dsschema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "`true` lists only rulesets attached to at least one virtual machine, `false` only rulesets attached to none. " +
					"Omit it to list all.",
			},
			"rulesets": dsschema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The rulesets.",
				NestedObject:        dsschema.NestedAttributeObject{Attributes: ruleSetDataAttributes(false)},
			},
		},
	}
}

func (d *ruleSetsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *ruleSetsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ruleSetsDataModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("attached"), &cfg.Attached)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filter := client.FirewallRuleSetsAll
	if !cfg.Attached.IsNull() {
		filter = client.FirewallRuleSetsUnattached
		if cfg.Attached.ValueBool() {
			filter = client.FirewallRuleSetsAttached
		}
	}
	sets, err := d.client.ListFirewallRuleSets(ctx, "", filter)
	if err != nil {
		resp.Diagnostics.AddError("Error listing firewall rulesets", err.Error())
		return
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].Name < sets[j].Name })
	cfg.RuleSets = make([]ruleSetDataModel, 0, len(sets))
	for i := range sets {
		cfg.RuleSets = append(cfg.RuleSets, ruleSetData(ctx, &sets[i], &resp.Diagnostics))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
