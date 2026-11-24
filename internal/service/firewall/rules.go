package firewall

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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

// Protocols lists every protocol the API accepts, as FIREWALL_PROTOCOL_<upper>.
var Protocols = []string{
	"tcp", "udp", "sctp", "ipv6", "icmp", "icmpv6", "esp", "ah", "ipsec", "gre", "ipip", "vxlan", "l2tp", "geneve", "any",
	"igmp", "ggp", "ipencap", "st", "egp", "igp", "pup", "hmp", "xns_idp", "rdp", "iso_tp4", "dccp", "xtp", "ddp", "idpr_cmtp",
	"ipv6_route", "ipv6_frag", "idrp", "rsvp", "skip", "ipv6_icmp", "ipv6_nonxt", "ipv6_opts", "vmtp", "eigrp", "ospf", "ax_25",
	"etherip", "encap", "pim", "ipcomp", "vrrp", "isis", "fc", "mobility_header", "udplite", "mpls_in_ip", "hip", "shim6", "wesp", "rohc",
}

// portProtocols are the protocols that carry ports.
var portProtocols = []string{"tcp", "udp", "sctp", "dccp", "udplite"}

const maxRules = 50

var (
	_ resource.Resource                   = &rulesResource{}
	_ resource.ResourceWithImportState    = &rulesResource{}
	_ resource.ResourceWithValidateConfig = &rulesResource{}
	_ resource.ResourceWithModifyPlan     = &rulesResource{}
)

// NewRuleSetRulesResource returns the noderings_firewall_ruleset_rules resource.
func NewRuleSetRulesResource() resource.Resource { return &rulesResource{} }

type rulesResource struct{ client *client.Client }

type rulesModel struct {
	ID            types.String `tfsdk:"id"`
	RuleSetID     types.String `tfsdk:"ruleset_id"`
	AdoptExisting types.Bool   `tfsdk:"adopt_existing"`
	Rules         types.List   `tfsdk:"rule"`
}

type ruleModel struct {
	ID               types.String   `tfsdk:"id"`
	Position         types.Int64    `tfsdk:"position"`
	Direction        types.String   `tfsdk:"direction"`
	Action           types.String   `tfsdk:"action"`
	Protocol         types.String   `tfsdk:"protocol"`
	SourcePorts      types.String   `tfsdk:"source_ports"`
	DestinationPorts types.String   `tfsdk:"destination_ports"`
	Source           *endpointModel `tfsdk:"source"`
	Destination      *endpointModel `tfsdk:"destination"`
}

type endpointModel struct {
	CIDRs           types.Set `tfsdk:"cidrs"`
	AddressGroupIDs types.Set `tfsdk:"address_group_ids"`
	VMTags          types.Set `tfsdk:"vm_tags"`
}

var endpointType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"cidrs":             types.SetType{ElemType: types.StringType},
	"address_group_ids": types.SetType{ElemType: types.StringType},
	"vm_tags":           types.SetType{ElemType: types.StringType},
}}

var ruleType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"id":                types.StringType,
	"position":          types.Int64Type,
	"direction":         types.StringType,
	"action":            types.StringType,
	"protocol":          types.StringType,
	"source_ports":      types.StringType,
	"destination_ports": types.StringType,
	"source":            endpointType,
	"destination":       endpointType,
}}

func (r *rulesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_ruleset_rules"
}

func endpointBlock(side string) schema.SingleNestedBlock {
	return schema.SingleNestedBlock{
		MarkdownDescription: fmt.Sprintf("Which addresses the rule matches as the %s. Set at least one of the arguments. "+
			"Entries of different kinds are combined: the rule matches any of them. Omit the block to match any %s address.", side, side),
		Attributes: map[string]schema.Attribute{
			"cidrs": schema.SetAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "IPv4 or IPv6 addresses or CIDRs. `0.0.0.0/0` (any IPv4) and `::/0` (any IPv6) must be the only entry on their side, " +
					"with no other CIDRs, address groups or tags; to match both families, omit the block or write two rules.",
				Validators: []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(cidrValidator{allowBare: true})},
			},
			"address_group_ids": schema.SetAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "IDs of `noderings_firewall_address_group` resources. If every group and tag on this side " +
					"stops resolving to an address (for example the virtual machine behind a hostname group was deleted), the platform skips the rule.",
				Validators: []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(framework.UUID())},
			},
			"vm_tags": schema.SetAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Virtual machine tags, 1 to 100 characters each; they match the addresses of every virtual machine with the tag. " +
					"Each tag must be on at least one existing virtual machine when the rule is written, so add `depends_on` for the " +
					"virtual machines that carry it. If no virtual machine has the tag any more, the platform skips the rule.",
				Validators: []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 100), framework.SafeString())},
			},
		},
	}
}

func (r *rulesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	ports := func(side string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional: true,
			MarkdownDescription: fmt.Sprintf("%s port: one port such as `443` or one range such as `8000-8100`. Lists are not supported; "+
				"write one rule per port or range. Only for the protocols `%s`. Omit for any port.", side, strings.Join(portProtocols, "`, `")),
			Validators: []validator.String{portsValidator{}},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "The rules of one firewall ruleset, as one ordered list. This resource is authoritative: it creates, updates, " +
			"reorders and deletes rules so that the ruleset holds exactly the `rule` blocks in the configuration, in that order, " +
			"and rules added outside Terraform show up as changes to remove. Use one `noderings_firewall_ruleset_rules` per ruleset.\n\n" +
			"Rules are evaluated top to bottom, ingress and egress in one list, and the first matching rule decides. " +
			"Traffic that matches no rule is accepted, so for a default-deny policy end the list with `drop` rules for `0.0.0.0/0` and `::/0`.\n\n" +
			"Changes reach the virtual machines the ruleset is attached to within about a minute; the API does not report when they are applied.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Same as `ruleset_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ruleset_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the `noderings_firewall_ruleset`. Changing it replaces this resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{framework.UUID()},
			},
			"adopt_existing": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "When the ruleset already has rules at create time, take them over: rules that match a `rule` block " +
					"are kept, the rest are deleted. With the default `false`, creating this resource fails if the ruleset is not empty, " +
					"so an existing rule list is never replaced by accident. You can also import the rules instead.",
			},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				MarkdownDescription: fmt.Sprintf("A rule. Order matters: the first block is evaluated first. At most %d rules per ruleset.", maxRules),
				Validators:          []validator.List{listvalidator.SizeAtMost(maxRules)},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"id":       schema.StringAttribute{Computed: true, MarkdownDescription: "Rule ID."},
						"position": schema.Int64Attribute{Computed: true, MarkdownDescription: "Position in the ruleset, starting at 0 (evaluated first)."},
						"direction": schema.StringAttribute{
							Required: true, MarkdownDescription: "`ingress` (traffic to the virtual machine) or `egress` (traffic from it).",
							Validators: []validator.String{stringvalidator.OneOf("ingress", "egress")},
						},
						"action": schema.StringAttribute{
							Required: true, MarkdownDescription: "`accept` or `drop`.",
							Validators: []validator.String{stringvalidator.OneOf("accept", "drop")},
						},
						"protocol": schema.StringAttribute{
							Required: true,
							MarkdownDescription: "IP protocol in lower case: `any`, `tcp`, `udp`, `icmp`, `ipv6_icmp` (ICMPv6) and others. One of `" +
								strings.Join(Protocols, "`, `") + "`.",
							Validators: []validator.String{stringvalidator.OneOf(Protocols...)},
						},
						"source_ports":      ports("Source"),
						"destination_ports": ports("Destination"),
					},
					Blocks: map[string]schema.Block{
						"source":      endpointBlock("source"),
						"destination": endpointBlock("destination"),
					},
				},
			},
		},
	}
}

func (r *rulesResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

// --- Validation ---

var portsRE = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)

type portsValidator struct{}

func (portsValidator) Description(context.Context) string {
	return "must be one port such as 443 or one range such as 8000-8100"
}

func (v portsValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (portsValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	m := portsRE.FindStringSubmatch(s)
	ok := m != nil
	if ok {
		lo, _ := strconv.Atoi(m[1])
		hi := lo
		if m[2] != "" {
			hi, _ = strconv.Atoi(m[2])
		}
		ok = lo >= 1 && hi <= 65535 && lo <= hi
	}
	if !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid port",
			fmt.Sprintf("%q is not a port. Use one port from 1 to 65535 such as 443, or one range such as 8000-8100 with the lower port first. "+
				"Lists such as 80,443 are not supported; write one rule per port or range.", s))
	}
}

func knownStrings(s types.Set) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	for _, e := range s.Elements() {
		if v, ok := e.(types.String); ok && !v.IsNull() && !v.IsUnknown() {
			out = append(out, v.ValueString())
		}
	}
	return out
}

// setCount returns how many entries a set has; an unknown set counts as
// one, since it must have at least one.
func setCount(s types.Set) int {
	if s.IsNull() {
		return 0
	}
	if s.IsUnknown() {
		return 1
	}
	return len(s.Elements())
}

func (e *endpointModel) count() int {
	if e == nil {
		return 0
	}
	return setCount(e.CIDRs) + setCount(e.AddressGroupIDs) + setCount(e.VMTags)
}

func (r *rulesResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var list types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("rule"), &list)...)
	if resp.Diagnostics.HasError() || list.IsNull() || list.IsUnknown() {
		return
	}
	var rules []ruleModel
	if d := list.ElementsAs(ctx, &rules, false); d.HasError() {
		return
	}
	for i, rule := range rules {
		p := path.Root("rule").AtListIndex(i)
		validateRule(rule, p, &resp.Diagnostics)
	}
}

func validateRule(rule ruleModel, p path.Path, diags *diag.Diagnostics) {
	if rule.Source == nil && rule.Destination == nil {
		diags.AddAttributeError(p, "Rule needs a source or destination",
			"Add a source or destination block. To match any IPv4 address, use cidrs = [\"0.0.0.0/0\"]; for IPv6 use a second rule with [\"::/0\"].")
	}
	for _, side := range []struct {
		name string
		e    *endpointModel
	}{{"source", rule.Source}, {"destination", rule.Destination}} {
		if side.e == nil {
			continue
		}
		sp := p.AtName(side.name)
		if side.e.count() == 0 {
			diags.AddAttributeError(sp, "Empty "+side.name+" block",
				"Set cidrs, address_group_ids or vm_tags in the "+side.name+" block, or remove the block.")
			continue
		}
		cidrs := knownStrings(side.e.CIDRs)
		var anys []string
		for _, c := range cidrs {
			if isAnyCIDR(c) {
				anys = append(anys, c)
			}
		}
		if len(anys) > 0 && side.e.count() > 1 {
			diags.AddAttributeError(sp.AtName("cidrs"), "Any-address CIDR must be alone",
				fmt.Sprintf("%s matches every address, so it must be the only entry in the %s block, with no other CIDRs, address groups or tags. "+
					"To match both IPv4 and IPv6, write two rules (one with 0.0.0.0/0 and one with ::/0), or omit the %s block "+
					"when the other side is set.", strings.Join(anys, " and "), side.name, side.name))
		}
		for _, c := range anys {
			if c != "0.0.0.0/0" && c != "::/0" {
				diags.AddAttributeError(sp.AtName("cidrs"), "Write any-address CIDRs canonically",
					fmt.Sprintf("Write %q as 0.0.0.0/0 (IPv4) or ::/0 (IPv6).", c))
			}
		}
	}
	if rule.Protocol.IsNull() || rule.Protocol.IsUnknown() || slices.Contains(portProtocols, rule.Protocol.ValueString()) {
		return
	}
	for _, port := range []struct {
		name string
		v    types.String
	}{{"source_ports", rule.SourcePorts}, {"destination_ports", rule.DestinationPorts}} {
		if !port.v.IsNull() {
			diags.AddAttributeError(p.AtName(port.name), "Ports need a port protocol",
				fmt.Sprintf("Protocol %q has no ports. Ports are only supported with %s; remove %s or change the protocol.",
					rule.Protocol.ValueString(), strings.Join(portProtocols, ", "), port.name))
		}
	}
}

// --- Conversion ---

func setStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	sort.Strings(out)
	return out
}

func endpointInput(ctx context.Context, e *endpointModel, diags *diag.Diagnostics) *client.FirewallRuleEndpoint {
	if e == nil {
		return nil
	}
	out := &client.FirewallRuleEndpoint{}
	if v := setStrings(ctx, e.CIDRs, diags); len(v) > 0 {
		out.IPs = &client.FirewallRuleIPs{IPs: v}
	}
	if v := setStrings(ctx, e.AddressGroupIDs, diags); len(v) > 0 {
		out.AddressGroupIDs = &client.FirewallRuleAddressGroups{AddressGroupIDs: v}
	}
	if v := setStrings(ctx, e.VMTags, diags); len(v) > 0 {
		out.VMTags = &client.FirewallRuleVMTags{Tags: v}
	}
	if out.IPs == nil && out.AddressGroupIDs == nil && out.VMTags == nil {
		return nil
	}
	return out
}

func ruleInput(ctx context.Context, m ruleModel, diags *diag.Diagnostics) client.FirewallRuleInput {
	return client.FirewallRuleInput{
		Action:           "FIREWALL_ACTION_" + strings.ToUpper(m.Action.ValueString()),
		Protocol:         "FIREWALL_PROTOCOL_" + strings.ToUpper(m.Protocol.ValueString()),
		Direction:        "FIREWALL_DIRECTION_" + strings.ToUpper(m.Direction.ValueString()),
		Source:           endpointInput(ctx, m.Source, diags),
		Destination:      endpointInput(ctx, m.Destination, diags),
		SourcePorts:      m.SourcePorts.ValueString(),
		DestinationPorts: m.DestinationPorts.ValueString(),
	}
}

// known reports whether every argument of a planned rule is known.
func (m ruleModel) known() bool {
	for _, v := range []types.String{m.Direction, m.Action, m.Protocol, m.SourcePorts, m.DestinationPorts} {
		if v.IsUnknown() {
			return false
		}
	}
	for _, e := range []*endpointModel{m.Source, m.Destination} {
		if e != nil && (e.CIDRs.IsUnknown() || e.AddressGroupIDs.IsUnknown() || e.VMTags.IsUnknown()) {
			return false
		}
	}
	return true
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}

// normalEndpoint returns a comparable form of an endpoint: sorted lists, and
// nil for empty parts and empty endpoints.
func normalEndpoint(e *client.FirewallRuleEndpoint) *client.FirewallRuleEndpoint {
	if e == nil {
		return nil
	}
	out := &client.FirewallRuleEndpoint{}
	if e.IPs != nil && len(e.IPs.IPs) > 0 {
		out.IPs = &client.FirewallRuleIPs{IPs: sortedCopy(e.IPs.IPs)}
	}
	if e.AddressGroupIDs != nil && len(e.AddressGroupIDs.AddressGroupIDs) > 0 {
		ids := make([]string, len(e.AddressGroupIDs.AddressGroupIDs))
		for i, id := range e.AddressGroupIDs.AddressGroupIDs {
			ids[i] = strings.ToLower(id)
		}
		out.AddressGroupIDs = &client.FirewallRuleAddressGroups{AddressGroupIDs: sortedCopy(ids)}
	}
	if e.VMTags != nil && len(e.VMTags.Tags) > 0 {
		out.VMTags = &client.FirewallRuleVMTags{Tags: sortedCopy(e.VMTags.Tags)}
	}
	if out.IPs == nil && out.AddressGroupIDs == nil && out.VMTags == nil {
		return nil
	}
	return out
}

func endpointKey(e *client.FirewallRuleEndpoint) string {
	e = normalEndpoint(e)
	if e == nil {
		return ""
	}
	var parts []string
	if e.IPs != nil {
		parts = append(parts, "ips="+strings.Join(e.IPs.IPs, ","))
	}
	if e.AddressGroupIDs != nil {
		parts = append(parts, "groups="+strings.Join(e.AddressGroupIDs.AddressGroupIDs, ","))
	}
	if e.VMTags != nil {
		parts = append(parts, "tags="+strings.Join(e.VMTags.Tags, ","))
	}
	return strings.Join(parts, ";")
}

// sameRule compares two rule bodies, ignoring list order.
func sameRule(a, b client.FirewallRuleInput) bool {
	return a.Action == b.Action && a.Protocol == b.Protocol && a.Direction == b.Direction &&
		a.SourcePorts == b.SourcePorts && a.DestinationPorts == b.DestinationPorts &&
		endpointKey(a.Source) == endpointKey(b.Source) && endpointKey(a.Destination) == endpointKey(b.Destination)
}

func optString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func optSet(ctx context.Context, in []string, diags *diag.Diagnostics) types.Set {
	if len(in) == 0 {
		return types.SetNull(types.StringType)
	}
	return setOfStrings(ctx, in, diags)
}

func endpointModelFrom(ctx context.Context, e *client.FirewallRuleEndpoint, diags *diag.Diagnostics) *endpointModel {
	e = normalEndpoint(e)
	if e == nil {
		return nil
	}
	m := &endpointModel{CIDRs: types.SetNull(types.StringType), AddressGroupIDs: types.SetNull(types.StringType), VMTags: types.SetNull(types.StringType)}
	if e.IPs != nil {
		m.CIDRs = optSet(ctx, e.IPs.IPs, diags)
	}
	if e.AddressGroupIDs != nil {
		m.AddressGroupIDs = optSet(ctx, e.AddressGroupIDs.AddressGroupIDs, diags)
	}
	if e.VMTags != nil {
		m.VMTags = optSet(ctx, e.VMTags.Tags, diags)
	}
	return m
}

func ruleModelFrom(ctx context.Context, r client.FirewallRule, diags *diag.Diagnostics) ruleModel {
	return ruleModel{
		ID:               types.StringValue(r.ID),
		Position:         types.Int64Value(int64(r.Position)),
		Direction:        types.StringValue(framework.TrimEnum(r.Rule.Direction, "FIREWALL_DIRECTION_")),
		Action:           types.StringValue(framework.TrimEnum(r.Rule.Action, "FIREWALL_ACTION_")),
		Protocol:         types.StringValue(framework.TrimEnum(r.Rule.Protocol, "FIREWALL_PROTOCOL_")),
		SourcePorts:      optString(r.Rule.SourcePorts),
		DestinationPorts: optString(r.Rule.DestinationPorts),
		Source:           endpointModelFrom(ctx, r.Rule.Source, diags),
		Destination:      endpointModelFrom(ctx, r.Rule.Destination, diags),
	}
}

func rulesList(ctx context.Context, rules []client.FirewallRule, diags *diag.Diagnostics) types.List {
	models := make([]ruleModel, 0, len(rules))
	for _, r := range rules {
		models = append(models, ruleModelFrom(ctx, r, diags))
	}
	l, d := types.ListValueFrom(ctx, ruleType, models)
	diags.Append(d...)
	return l
}

func sortRules(rules []client.FirewallRule) {
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Position < rules[j].Position })
}

// --- Plan ---

// ModifyPlan keeps the IDs of rules that stay. A planned rule takes the ID of
// an unchanged rule with the same body anywhere in the list (a move), or
// otherwise of the rule at the same index (an in-place update). Rules left
// without an ID are created.
func (r *rulesResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var planList types.List
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("rule"), &planList)...)
	if resp.Diagnostics.HasError() || planList.IsUnknown() || planList.IsNull() {
		return
	}
	var planned []ruleModel
	if d := planList.ElementsAs(ctx, &planned, false); d.HasError() {
		return
	}
	var current []ruleModel
	var planRuleSet, stateRuleSet types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("ruleset_id"), &planRuleSet)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("ruleset_id"), &stateRuleSet)...)
	}
	// Rules carry over only within the same ruleset; a new ruleset_id replaces the resource.
	if !req.State.Raw.IsNull() && planRuleSet.Equal(stateRuleSet) {
		var stateList types.List
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("rule"), &stateList)...)
		if !stateList.IsNull() && !stateList.IsUnknown() {
			resp.Diagnostics.Append(stateList.ElementsAs(ctx, &current, false)...)
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}

	var diags diag.Diagnostics // conversion of known values cannot fail
	claimed := make([]bool, len(current))
	matched := make([]bool, len(planned))
	for i := range planned {
		if !planned[i].known() {
			continue
		}
		want := ruleInput(ctx, planned[i], &diags)
		for j := range current {
			if !claimed[j] && sameRule(want, ruleInput(ctx, current[j], &diags)) {
				planned[i].ID = current[j].ID
				claimed[j], matched[i] = true, true
				break
			}
		}
	}
	for i := range planned {
		if !matched[i] {
			if i < len(current) && !claimed[i] {
				planned[i].ID = current[i].ID
				claimed[i] = true
			} else {
				planned[i].ID = types.StringUnknown()
			}
		}
		planned[i].Position = types.Int64Value(int64(i))
	}
	l, d := types.ListValueFrom(ctx, ruleType, planned)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("rule"), l)...)
}

// --- Reconcile ---

// reconcile makes the ruleset's rules equal desired, in order. ids holds the
// ID planned for each desired rule, or "" for rules to create. The caller
// holds the ruleset lock.
func (r *rulesResource) reconcile(ctx context.Context, rulesetID string, desired []client.FirewallRuleInput, ids []string, creating, adopt bool) ([]client.FirewallRule, error) {
	current, err := r.client.ListFirewallRules(ctx, rulesetID)
	if err != nil {
		return nil, err
	}
	sortRules(current)
	if creating && len(current) > 0 && !adopt {
		return nil, fmt.Errorf("the ruleset already has %d rule(s) that this resource does not manage. "+
			"Import them with: terraform import <address> %s, or set adopt_existing = true to take them over "+
			"(rules that match no rule block are deleted). If another noderings_firewall_ruleset_rules manages this ruleset, use only one", len(current), rulesetID)
	}
	byID := map[string]*client.FirewallRule{}
	for i := range current {
		byID[current[i].ID] = &current[i]
	}
	claimed := map[string]bool{}
	existing := make([]bool, len(ids))
	for i, id := range ids {
		if id == "" {
			continue
		}
		if byID[id] == nil {
			return nil, fmt.Errorf("rule %s (rule block %d) was deleted outside Terraform during this run; run terraform apply again", id, i)
		}
		claimed[id], existing[i] = true, true
	}
	// Reuse unclaimed rules with the same body rather than recreating them.
	for i, id := range ids {
		if id != "" {
			continue
		}
		for _, c := range current {
			if !claimed[c.ID] && sameRule(c.Rule, desired[i]) {
				ids[i], claimed[c.ID], existing[i] = c.ID, true, true
				break
			}
		}
	}

	// 1. Delete rules that are not wanted.
	var order []string
	for _, c := range current {
		if claimed[c.ID] {
			order = append(order, c.ID)
			continue
		}
		if err := r.client.DeleteFirewallRule(ctx, c.ID); err != nil && !client.IsNotFound(err) {
			return nil, fmt.Errorf("deleting rule %s: %w", c.ID, err)
		}
	}
	// 2. Update kept rules whose body changed (full replacement).
	for i, id := range ids {
		if existing[i] && !sameRule(byID[id].Rule, desired[i]) {
			if _, err := r.client.UpdateFirewallRule(ctx, id, desired[i]); err != nil {
				return nil, fmt.Errorf("updating rule %s (rule block %d): %w", id, i, err)
			}
		}
	}
	// 3. Create new rules bottom-up. Each lands at position 0, so creating
	// the last one first leaves the new rules in the right relative order.
	for i := len(ids) - 1; i >= 0; i-- {
		if ids[i] != "" {
			continue
		}
		created, err := r.client.CreateFirewallRule(ctx, rulesetID, desired[i])
		if err != nil {
			return nil, fmt.Errorf("creating rule block %d: %w", i, err)
		}
		ids[i] = created.ID
		order = append([]string{created.ID}, order...)
	}
	// 4. Fix the order: put each desired rule before whatever is at its index.
	for i := range ids {
		if order[i] == ids[i] {
			continue
		}
		if _, err := r.client.MoveFirewallRule(ctx, ids[i], order[i], client.FirewallMoveBefore); err != nil {
			return nil, fmt.Errorf("moving rule %s to position %d: %w", ids[i], i, err)
		}
		k := slices.Index(order, ids[i])
		order = slices.Insert(slices.Delete(order, k, k+1), i, ids[i])
	}
	// 5. Re-read and check.
	final, err := r.client.ListFirewallRules(ctx, rulesetID)
	if err != nil {
		return nil, err
	}
	sortRules(final)
	got := make([]string, len(final))
	for i, f := range final {
		got[i] = f.ID
	}
	if !slices.Equal(got, ids) {
		return nil, fmt.Errorf("the ruleset's rules changed while Terraform was writing them (another tool or user edited the ruleset). " +
			"Run terraform apply again")
	}
	return final, nil
}

func (r *rulesResource) apply(ctx context.Context, plan *rulesModel, creating bool, diags *diag.Diagnostics) {
	var planned []ruleModel
	if !plan.Rules.IsNull() && !plan.Rules.IsUnknown() {
		diags.Append(plan.Rules.ElementsAs(ctx, &planned, false)...)
	}
	desired := make([]client.FirewallRuleInput, len(planned))
	ids := make([]string, len(planned))
	for i, m := range planned {
		desired[i] = ruleInput(ctx, m, diags)
		if !m.ID.IsUnknown() && !m.ID.IsNull() && !creating {
			ids[i] = m.ID.ValueString()
		}
	}
	if diags.HasError() {
		return
	}
	rulesetID := plan.RuleSetID.ValueString()
	unlock := lockRuleSet(rulesetID)
	defer unlock()
	final, err := r.reconcile(ctx, rulesetID, desired, ids, creating, plan.AdoptExisting.ValueBool())
	if err != nil {
		diags.AddError("Error writing firewall rules", err.Error())
		return
	}
	plan.ID = types.StringValue(rulesetID)
	plan.Rules = rulesList(ctx, final, diags)
}

func (r *rulesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rulesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, true, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *rulesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan rulesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, false, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *rulesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state rulesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rulesetID := state.RuleSetID.ValueString()
	if _, err := r.client.GetFirewallRuleSet(ctx, rulesetID); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading firewall ruleset", err.Error())
		return
	}
	rules, err := r.client.ListFirewallRules(ctx, rulesetID)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall rules", err.Error())
		return
	}
	sortRules(rules)
	state.ID = types.StringValue(rulesetID)
	if state.AdoptExisting.IsNull() {
		state.AdoptExisting = types.BoolValue(false)
	}
	state.Rules = rulesList(ctx, rules, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Delete removes every rule; the ruleset itself stays.
func (r *rulesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rulesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rulesetID := state.RuleSetID.ValueString()
	unlock := lockRuleSet(rulesetID)
	defer unlock()
	rules, err := r.client.ListFirewallRules(ctx, rulesetID)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall rules", err.Error())
		return
	}
	for _, rule := range rules {
		if err := r.client.DeleteFirewallRule(ctx, rule.ID); err != nil && !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Error deleting firewall rule", fmt.Sprintf("Deleting rule %s: %s", rule.ID, err))
			return
		}
	}
}

func (r *rulesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ruleset_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("adopt_existing"), false)...)
}
