// Package billing implements read-only billing lookups: credit balance,
// subscriptions and plan entitlements.
package billing

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// Resources lists this package's resources. Billing has none.
func Resources() []func() resource.Resource { return nil }

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewCreditBalanceDataSource, NewSubscriptionDataSource, NewEntitlementsDataSource}
}

// amount parses a decimal amount sent as a string. Empty means zero.
func amount(s string, diags *diag.Diagnostics) types.Float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return types.Float64Value(0)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		diags.AddError("Unexpected amount", fmt.Sprintf("The API returned %q, which is not a number. Please report this issue to the provider developers.", s))
		return types.Float64Null()
	}
	return types.Float64Value(v)
}

// --- noderings_credit_balance ---

// NewCreditBalanceDataSource returns the noderings_credit_balance data source.
func NewCreditBalanceDataSource() datasource.DataSource { return &creditBalanceDataSource{} }

type creditBalanceDataSource struct{ client *client.Client }

type creditBalanceModel struct {
	Balance                 types.Float64 `tfsdk:"balance"`
	Reserved                types.Float64 `tfsdk:"reserved"`
	Available               types.Float64 `tfsdk:"available"`
	Promotional             types.Float64 `tfsdk:"promotional"`
	PromotionalExpiringSoon types.Float64 `tfsdk:"promotional_expiring_soon"`
	Frozen                  types.Bool    `tfsdk:"frozen"`
	FrozenReason            types.String  `tfsdk:"frozen_reason"`
	Currency                types.String  `tfsdk:"currency"`
}

func (d *creditBalanceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credit_balance"
}

func (d *creditBalanceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	money := func(desc string) schema.Float64Attribute {
		return schema.Float64Attribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organization's prepaid credit wallet, which pays for usage-billed (hourly) resources. " +
			"Use it in a `check` block or a precondition to stop before ordering more than the credit covers.",
		Attributes: map[string]schema.Attribute{
			"balance":                   money("Total credit."),
			"reserved":                  money("Credit held for running usage-billed resources."),
			"available":                 money("Credit that can pay for new resources: the balance minus what is reserved."),
			"promotional":               money("Promotional credit included in the balance."),
			"promotional_expiring_soon": money("Promotional credit that expires soon."),
			"frozen":                    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the wallet is frozen. A frozen wallet cannot pay for new usage-billed resources."},
			"frozen_reason":             schema.StringAttribute{Computed: true, MarkdownDescription: "Why the wallet is frozen, if it is."},
			"currency":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Currency of the amounts, for example `USD`."},
		},
	}
}

func (d *creditBalanceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *creditBalanceDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	b, err := d.client.GetCreditBalance(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading credit balance", fmt.Sprintf("%s\n\nThe token's role needs the CreditService::GetCreditBalance permission.", err))
		return
	}
	m := creditBalanceModel{
		Balance:                 amount(b.Balance, &resp.Diagnostics),
		Reserved:                amount(b.Reserved, &resp.Diagnostics),
		Available:               amount(b.Available, &resp.Diagnostics),
		Promotional:             amount(b.PromotionalAvailable, &resp.Diagnostics),
		PromotionalExpiringSoon: amount(b.PromotionalExpiringSoon, &resp.Diagnostics),
		Frozen:                  types.BoolValue(b.Frozen),
		FrozenReason:            types.StringValue(b.FrozenReason),
		Currency:                types.StringValue(b.Currency),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_subscription ---

// NewSubscriptionDataSource returns the noderings_subscription data source.
func NewSubscriptionDataSource() datasource.DataSource { return &subscriptionDataSource{} }

type subscriptionDataSource struct{ client *client.Client }

type subscriptionModel struct {
	VirtualMachineID   types.String  `tfsdk:"virtual_machine_id"`
	ID                 types.String  `tfsdk:"id"`
	Status             types.String  `tfsdk:"status"`
	BillingType        types.String  `tfsdk:"billing_type"`
	BillingPeriod      types.String  `tfsdk:"billing_period"`
	RecurringAmount    types.Float64 `tfsdk:"recurring_amount"`
	Currency           types.String  `tfsdk:"currency"`
	HasUsageCharges    types.Bool    `tfsdk:"has_usage_charges"`
	PlanID             types.String  `tfsdk:"plan_id"`
	PlanCode           types.String  `tfsdk:"plan_code"`
	PlanName           types.String  `tfsdk:"plan_name"`
	Hostname           types.String  `tfsdk:"hostname"`
	StartedAt          types.String  `tfsdk:"started_at"`
	CurrentPeriodStart types.String  `tfsdk:"current_period_start"`
	CurrentPeriodEnd   types.String  `tfsdk:"current_period_end"`
	CancelAtPeriodEnd  types.Bool    `tfsdk:"cancel_at_period_end"`
}

func (d *subscriptionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subscription"
}

func (d *subscriptionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "The active subscription that bills a virtual machine: its price, billing period and renewal date.",
		Attributes: map[string]schema.Attribute{
			"virtual_machine_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "ID of the virtual machine.",
				Validators: []validator.String{framework.UUID()},
			},
			"id":                   str("Subscription ID."),
			"status":               str("`trialing`, `active`, `past_due`, `canceled` or `paused`."),
			"billing_type":         str("`subscription` (fixed price), `usage` (billed hourly from credit) or `free`."),
			"billing_period":       str("Billing period, for example `month` or `hour`."),
			"recurring_amount":     schema.Float64Attribute{Computed: true, MarkdownDescription: "Price per billing period."},
			"currency":             str("Currency of `recurring_amount`, for example `USD`."),
			"has_usage_charges":    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether usage, such as bandwidth overage, is billed on top."},
			"plan_id":              str("ID of the plan."),
			"plan_code":            str("Plan code."),
			"plan_name":            str("Plan name."),
			"hostname":             str("Hostname of the virtual machine."),
			"started_at":           str("Start time (RFC 3339)."),
			"current_period_start": str("Start of the current billing period (RFC 3339)."),
			"current_period_end":   str("End of the current billing period (RFC 3339): the next renewal."),
			"cancel_at_period_end": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the subscription ends instead of renewing at `current_period_end`."},
		},
	}
}

func (d *subscriptionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *subscriptionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m subscriptionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	subs, err := d.client.ListSubscriptions(ctx, true)
	if err != nil {
		resp.Diagnostics.AddError("Error listing subscriptions", err.Error())
		return
	}
	vmID := m.VirtualMachineID.ValueString()
	for i := range subs {
		s := &subs[i]
		if !strings.EqualFold(s.ResourceVMID, vmID) {
			continue
		}
		m.ID = types.StringValue(s.ID)
		m.Status = types.StringValue(framework.TrimEnum(s.SubscriptionStatus, "SUBSCRIPTION_STATUS_"))
		m.BillingType = types.StringValue(framework.TrimEnum(s.BillingType, "BILLING_TYPE_"))
		m.BillingPeriod = types.StringValue(framework.TrimEnum(s.PlanBillingPeriod, "BILLING_PERIOD_"))
		m.RecurringAmount = amount(s.RecurringAmount, &resp.Diagnostics)
		m.Currency = types.StringValue(s.Currency)
		m.HasUsageCharges = types.BoolValue(s.HasUsageCharges)
		m.PlanID = types.StringValue(s.Plan)
		m.PlanCode = types.StringValue(s.PlanCode)
		m.PlanName = types.StringValue(s.PlanName)
		m.Hostname = types.StringValue(s.ResourceHostname)
		m.StartedAt = framework.Time(s.StartDate)
		m.CurrentPeriodStart = framework.Time(s.CurrentPeriodStart)
		m.CurrentPeriodEnd = framework.Time(s.CurrentPeriodEnd)
		m.CancelAtPeriodEnd = types.BoolValue(s.CancelAtPeriodEnd)
		resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
		return
	}
	resp.Diagnostics.AddError("Subscription not found",
		fmt.Sprintf("No active subscription bills virtual machine %s. Check the ID; free virtual machines and those being deleted may have none.", vmID))
}

// --- noderings_entitlements ---

// NewEntitlementsDataSource returns the noderings_entitlements data source.
func NewEntitlementsDataSource() datasource.DataSource { return &entitlementsDataSource{} }

type entitlementsDataSource struct{ client *client.Client }

type entitlementsModel struct {
	Rules    types.List `tfsdk:"rules"`
	Limits   types.Map  `tfsdk:"limits"`
	Usage    types.Map  `tfsdk:"usage"`
	Features types.Map  `tfsdk:"features"`
}

var ruleAttrTypes = map[string]attr.Type{
	"key": types.StringType, "effect": types.StringType, "enabled": types.BoolType, "limit": types.Int64Type, "usage": types.Int64Type,
}

func (d *entitlementsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_entitlements"
}

func (d *entitlementsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The organization's plan rules: quotas such as the maximum number of virtual machines or DNS zones, with current usage, and feature switches. " +
			"Use it to fail a plan early when an apply would exceed a quota.",
		Attributes: map[string]schema.Attribute{
			"rules": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Every rule, sorted by key.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"key":     schema.StringAttribute{Computed: true, MarkdownDescription: "Rule key, as the API reports it."},
					"effect":  schema.StringAttribute{Computed: true, MarkdownDescription: "`limit` (a quota), `enable` or `entitle` (a feature switch)."},
					"enabled": schema.BoolAttribute{Computed: true, MarkdownDescription: "For feature switches, whether the feature is on."},
					"limit":   schema.Int64Attribute{Computed: true, MarkdownDescription: "For quotas, the maximum allowed."},
					"usage":   schema.Int64Attribute{Computed: true, MarkdownDescription: "For quotas, how much is used now."},
				}},
			},
			"limits":   schema.MapAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Quota limits keyed by rule key."},
			"usage":    schema.MapAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Current usage of each quota, keyed by rule key."},
			"features": schema.MapAttribute{Computed: true, ElementType: types.BoolType, MarkdownDescription: "Feature switches keyed by rule key."},
		},
	}
}

func (d *entitlementsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

func (d *entitlementsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	rules, err := d.client.GetEntitlements(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading entitlements", err.Error())
		return
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].RuleKey < rules[j].RuleKey })
	objs := make([]attr.Value, 0, len(rules))
	limits, usage, features := map[string]int64{}, map[string]int64{}, map[string]bool{}
	for _, r := range rules {
		effect := framework.TrimEnum(r.Effect, "RULE_EFFECT_")
		if effect == "limit" {
			limits[r.RuleKey] = int64(r.Int64Value)
			usage[r.RuleKey] = int64(r.CurrentUsage)
		} else {
			features[r.RuleKey] = r.BoolValue
		}
		obj, diags := types.ObjectValue(ruleAttrTypes, map[string]attr.Value{
			"key": types.StringValue(r.RuleKey), "effect": types.StringValue(effect), "enabled": types.BoolValue(r.BoolValue),
			"limit": types.Int64Value(int64(r.Int64Value)), "usage": types.Int64Value(int64(r.CurrentUsage)),
		})
		resp.Diagnostics.Append(diags...)
		objs = append(objs, obj)
	}
	var m entitlementsModel
	var diags diag.Diagnostics
	m.Rules, diags = types.ListValue(types.ObjectType{AttrTypes: ruleAttrTypes}, objs)
	resp.Diagnostics.Append(diags...)
	m.Limits, diags = types.MapValueFrom(ctx, types.Int64Type, limits)
	resp.Diagnostics.Append(diags...)
	m.Usage, diags = types.MapValueFrom(ctx, types.Int64Type, usage)
	resp.Diagnostics.Append(diags...)
	m.Features, diags = types.MapValueFrom(ctx, types.BoolType, features)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
