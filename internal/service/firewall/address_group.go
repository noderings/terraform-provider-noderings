package firewall

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var (
	_ resource.Resource                   = &addressGroupResource{}
	_ resource.ResourceWithImportState    = &addressGroupResource{}
	_ resource.ResourceWithValidateConfig = &addressGroupResource{}
)

// NewAddressGroupResource returns the noderings_firewall_address_group resource.
func NewAddressGroupResource() resource.Resource { return &addressGroupResource{} }

type addressGroupResource struct{ client *client.Client }

type addressGroupModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	CIDRs     types.Set    `tfsdk:"cidrs"`
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

// fqdnRE matches names that look like a fully qualified hostname.
var fqdnRE = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

const addressBookNote = "Creating a virtual machine creates an address group named exactly like the VM's hostname, " +
	"and later overwrites its CIDRs. Do not give a managed group the hostname of a virtual machine."

func (r *addressGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_address_group"
}

func (r *addressGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A named list of CIDRs that firewall rules can use as a source or destination. " +
			"Changes reach the virtual machines that use the group within about a minute.\n\n" + addressBookNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Address group ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name, 1 to 63 characters, unique in the organization. " + addressBookNote,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 63), framework.SafeString()},
			},
			"cidrs": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				MarkdownDescription: "IPv4 and IPv6 CIDRs, for example `203.0.113.0/24` or `2001:db8::/48`. " +
					"The API stores each one as its network address with IPv6 compressed (`10.0.0.5/24` becomes `10.0.0.0/24`); " +
					"write them that way to match what other tools show. Use `0.0.0.0/0` or `::/0` for every address.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(cidrValidator{}),
				},
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339).", PlanModifiers: keep},
			"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last update time (RFC 3339)."},
		},
	}
}

func (r *addressGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func (r *addressGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m addressGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Name.IsNull() && !m.Name.IsUnknown() && fqdnRE.MatchString(m.Name.ValueString()) {
		resp.Diagnostics.AddAttributeWarning(path.Root("name"), "Address group name looks like a hostname",
			addressBookNote+" If a virtual machine is ever named "+m.Name.ValueString()+", this group's CIDRs will be replaced.")
	}
	if m.CIDRs.IsNull() || m.CIDRs.IsUnknown() {
		return
	}
	seen := map[string]string{}
	for _, e := range m.CIDRs.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsUnknown() || s.IsNull() {
			continue
		}
		c, err := canonicalCIDR(s.ValueString())
		if err != nil {
			continue
		}
		if prev, dup := seen[c]; dup {
			resp.Diagnostics.AddAttributeError(path.Root("cidrs"), "Duplicate CIDR",
				fmt.Sprintf("%q and %q are the same network (%s). List it once.", prev, s.ValueString(), c))
			return
		}
		seen[c] = s.ValueString()
	}
}

func prefixesFrom(ctx context.Context, set types.Set, diags *diag.Diagnostics) []client.FirewallIPPrefix {
	var cidrs []string
	diags.Append(set.ElementsAs(ctx, &cidrs, false)...)
	sort.Strings(cidrs)
	out := make([]client.FirewallIPPrefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := parsePrefix(c)
		if err != nil {
			diags.AddAttributeError(path.Root("cidrs"), "Invalid CIDR", err.Error())
			continue
		}
		p = p.Masked()
		out = append(out, client.FirewallIPPrefix{IP: p.Addr().String(), Prefix: client.Int64(p.Bits())})
	}
	return out
}

func groupCIDRs(g *client.FirewallAddressGroup) []string {
	out := make([]string, 0, len(g.Prefixes))
	for _, p := range g.Prefixes {
		s := fmt.Sprintf("%s/%d", p.IP, p.Prefix)
		if c, err := canonicalCIDR(s); err == nil {
			s = c
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// setAddressGroup copies API values into the model. CIDRs written in a
// non-canonical form are kept as written when they name the same networks.
func setAddressGroup(ctx context.Context, m *addressGroupModel, g *client.FirewallAddressGroup, diags *diag.Diagnostics) {
	m.ID = types.StringValue(g.ID)
	m.Name = types.StringValue(g.Name)
	api := groupCIDRs(g)
	keep := false
	if !m.CIDRs.IsNull() && !m.CIDRs.IsUnknown() {
		var cur []string
		diags.Append(m.CIDRs.ElementsAs(ctx, &cur, false)...)
		keep = sameStrings(canonicalSet(cur), api)
	}
	if !keep {
		m.CIDRs = setOfStrings(ctx, api, diags)
	}
	m.CreatedAt = framework.Time(g.Timestamps.CreateTime)
	m.UpdatedAt = framework.Time(g.Timestamps.UpdateTime)
}

func (r *addressGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan addressGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	prefixes := prefixesFrom(ctx, plan.CIDRs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.CreateFirewallAddressGroup(ctx, plan.Name.ValueString(), prefixes)
	if err != nil {
		resp.Diagnostics.AddError("Error creating firewall address group", groupWriteError(err, plan.Name.ValueString()))
		return
	}
	setAddressGroup(ctx, &plan, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func groupWriteError(err error, name string) string {
	detail := err.Error()
	if client.IsAlreadyExists(err) {
		detail += "\n\nThe organization already has an address group named " + name +
			". Choose another name, or import the existing group with: terraform import <address> <group id>. " + addressBookNote
	}
	return detail
}

func (r *addressGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state addressGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetFirewallAddressGroup(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall address group", err.Error())
		return
	}
	setAddressGroup(ctx, &state, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *addressGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan addressGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	prefixes := prefixesFrom(ctx, plan.CIDRs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.UpdateFirewallAddressGroup(ctx, plan.ID.ValueString(), plan.Name.ValueString(), prefixes)
	if err != nil {
		resp.Diagnostics.AddError("Error updating firewall address group", groupWriteError(err, plan.Name.ValueString()))
		return
	}
	setAddressGroup(ctx, &plan, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *addressGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state addressGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteFirewallAddressGroup(ctx, state.ID.ValueString())
	if err == nil || client.IsNotFound(err) {
		return
	}
	detail := err.Error()
	if client.IsFailedPrecondition(err) {
		if names := r.rulesetsUsing(ctx, state.ID.ValueString()); len(names) > 0 {
			detail += "\n\nFirewall rules in these rulesets still use the group: " + strings.Join(names, ", ") +
				". Remove those rules first, or make the rules depend on this group so Terraform orders the changes."
		} else {
			detail += "\n\nA firewall rule still uses the group. Remove that rule first."
		}
	}
	resp.Diagnostics.AddError("Error deleting firewall address group", detail)
}

// rulesetsUsing returns the names of rulesets with a rule that references the group.
func (r *addressGroupResource) rulesetsUsing(ctx context.Context, groupID string) []string {
	sets, err := r.client.ListFirewallRuleSets(ctx, "", client.FirewallRuleSetsAll)
	if err != nil {
		return nil
	}
	var names []string
	for _, rs := range sets {
		rules, err := r.client.ListFirewallRules(ctx, rs.ID)
		if err != nil {
			continue
		}
		for _, rule := range rules {
			if endpointHasGroup(rule.Rule.Source, groupID) || endpointHasGroup(rule.Rule.Destination, groupID) {
				names = append(names, rs.Name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

func endpointHasGroup(e *client.FirewallRuleEndpoint, id string) bool {
	if e == nil || e.AddressGroupIDs == nil {
		return false
	}
	for _, g := range e.AddressGroupIDs.AddressGroupIDs {
		if strings.EqualFold(g, id) {
			return true
		}
	}
	return false
}

func (r *addressGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- noderings_firewall_address_group data source ---

// NewAddressGroupDataSource returns the noderings_firewall_address_group data source.
func NewAddressGroupDataSource() datasource.DataSource { return &addressGroupDataSource{} }

type addressGroupDataSource struct{ client *client.Client }

func (d *addressGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_address_group"
}

func (d *addressGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Looks up a firewall address group by ID or exact name. " +
			"This includes the address groups the platform creates for each virtual machine, named like its hostname.",
		Attributes: map[string]dsschema.Attribute{
			"id":         dsschema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Address group ID."},
			"name":       dsschema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Exact name (case-sensitive)."},
			"cidrs":      dsschema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "CIDRs in canonical form."},
			"created_at": dsschema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
			"updated_at": dsschema.StringAttribute{Computed: true, MarkdownDescription: "Last update time (RFC 3339)."},
		},
	}
}

func (d *addressGroupDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *addressGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *addressGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg addressGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		g   *client.FirewallAddressGroup
		err error
	)
	if id := cfg.ID.ValueString(); id != "" {
		g, err = d.client.GetFirewallAddressGroup(ctx, id)
	} else {
		g, err = d.client.FindFirewallAddressGroup(ctx, cfg.Name.ValueString())
	}
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Firewall address group not found", err.Error())
			return
		}
		resp.Diagnostics.AddError("Error reading firewall address group", err.Error())
		return
	}
	out := addressGroupModel{CIDRs: types.SetNull(types.StringType)}
	setAddressGroup(ctx, &out, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}
