package client

import (
	"context"
	"net/http"
	"net/url"
)

// FirewallIPPrefix is one CIDR, split into address and prefix length.
type FirewallIPPrefix struct {
	IP     string `json:"ip"`
	Prefix Int64  `json:"prefix"`
}

// FirewallAddressGroup is a named list of CIDRs that firewall rules can reference.
type FirewallAddressGroup struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Prefixes   []FirewallIPPrefix `json:"prefixes"`
	Timestamps Timestamps         `json:"timestamps"`
}

// FirewallRuleSet is a named, ordered list of firewall rules.
type FirewallRuleSet struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	RuleCount           int64      `json:"ruleCount"`
	AttachedVMCount     int64      `json:"attachedVmCount"`
	AttachedVMHostnames []string   `json:"attachedVmHostnames"`
	Timestamps          Timestamps `json:"timestamps"`
}

// FirewallRuleIPs lists addresses or CIDRs.
type FirewallRuleIPs struct {
	IPs []string `json:"ips"`
}

// FirewallRuleAddressGroups lists address group IDs.
type FirewallRuleAddressGroups struct {
	AddressGroupIDs []string `json:"addressGroupIds"`
}

// FirewallRuleVMTags lists virtual machine tags.
type FirewallRuleVMTags struct {
	Tags []string `json:"tags"`
}

// FirewallRuleEndpoint is the source or destination of a rule.
type FirewallRuleEndpoint struct {
	IPs             *FirewallRuleIPs           `json:"ips,omitempty"`
	AddressGroupIDs *FirewallRuleAddressGroups `json:"addressGroupIds,omitempty"`
	VMTags          *FirewallRuleVMTags        `json:"vmTags,omitempty"`
}

// FirewallRuleInput is the body of a rule. Enums use the API names, for
// example FIREWALL_PROTOCOL_TCP.
type FirewallRuleInput struct {
	Action           string                `json:"action"`
	Protocol         string                `json:"protocol"`
	Direction        string                `json:"direction"`
	Source           *FirewallRuleEndpoint `json:"source,omitempty"`
	Destination      *FirewallRuleEndpoint `json:"destination,omitempty"`
	SourcePorts      string                `json:"sourcePorts,omitempty"`
	DestinationPorts string                `json:"destinationPorts,omitempty"`
}

// FirewallRule is a rule in a ruleset. Position 0 is evaluated first.
type FirewallRule struct {
	ID         string            `json:"id"`
	RulesetID  string            `json:"rulesetId"`
	Rule       FirewallRuleInput `json:"rule"`
	Position   Int64             `json:"position"`
	Timestamps Timestamps        `json:"timestamps"`
}

// Firewall rule move positions.
const (
	FirewallMoveBefore = "FIREWALL_RULE_POSITION_BEFORE"
	FirewallMoveAfter  = "FIREWALL_RULE_POSITION_AFTER"
)

// Ruleset attachment filters for ListFirewallRuleSets.
const (
	FirewallRuleSetsAll        = ""
	FirewallRuleSetsAttached   = "FIREWALL_RULESET_ATTACHMENT_FILTER_ATTACHED"
	FirewallRuleSetsUnattached = "FIREWALL_RULESET_ATTACHMENT_FILTER_UNATTACHED"
)

// --- Address groups ---

// CreateFirewallAddressGroup creates an address group.
func (c *Client) CreateFirewallAddressGroup(ctx context.Context, name string, prefixes []FirewallIPPrefix) (*FirewallAddressGroup, error) {
	var out struct {
		Group *FirewallAddressGroup `json:"group"`
	}
	body := map[string]any{"group": map[string]any{"name": name, "prefixes": prefixes}}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateFirewallAddressGroup", path: "/v1/firewall/address-groups", body: body}, &out); err != nil {
		return nil, err
	}
	if out.Group != nil && out.Group.ID != "" {
		return out.Group, nil
	}
	// Older API versions return only {success}; names are unique, so look it up.
	return c.FindFirewallAddressGroup(ctx, name)
}

// GetFirewallAddressGroup returns one address group.
func (c *Client) GetFirewallAddressGroup(ctx context.Context, id string) (*FirewallAddressGroup, error) {
	var out FirewallAddressGroup
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetFirewallAddressGroup", path: "/v1/firewall/address-groups/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, notFoundErr("firewall address group not found")
	}
	return &out, nil
}

// ListFirewallAddressGroups returns the organization's address groups. name is
// an optional case-insensitive substring filter.
func (c *Client) ListFirewallAddressGroups(ctx context.Context, name string) ([]FirewallAddressGroup, error) {
	var out struct {
		Groups []FirewallAddressGroup `json:"groups"`
	}
	q := pageAll()
	if name != "" {
		q.Set("name", name)
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListFirewallAddressGroups", path: "/v1/firewall/address-groups", query: q, retryUnavailable: true}, &out)
	return out.Groups, err
}

// FindFirewallAddressGroup returns the address group with exactly this name.
func (c *Client) FindFirewallAddressGroup(ctx context.Context, name string) (*FirewallAddressGroup, error) {
	groups, err := c.ListFirewallAddressGroups(ctx, name)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i], nil
		}
	}
	return nil, notFoundErr("no firewall address group is named " + name)
}

// UpdateFirewallAddressGroup replaces an address group's name and prefixes.
func (c *Client) UpdateFirewallAddressGroup(ctx context.Context, id, name string, prefixes []FirewallIPPrefix) (*FirewallAddressGroup, error) {
	var out FirewallAddressGroup
	body := map[string]any{"group": map[string]any{"name": name, "prefixes": prefixes}}
	if err := c.do(ctx, request{method: http.MethodPatch, op: "UpdateFirewallAddressGroup", path: "/v1/firewall/address-groups/" + url.PathEscape(id), body: body}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return c.GetFirewallAddressGroup(ctx, id)
	}
	return &out, nil
}

// DeleteFirewallAddressGroup deletes an address group. It fails with
// FailedPrecondition while a rule references the group.
func (c *Client) DeleteFirewallAddressGroup(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteFirewallAddressGroup", path: "/v1/firewall/address-groups/" + url.PathEscape(id)}, nil)
}

// --- Rulesets ---

// CreateFirewallRuleSet creates an empty ruleset.
func (c *Client) CreateFirewallRuleSet(ctx context.Context, name string) (*FirewallRuleSet, error) {
	var out struct {
		RuleSet *FirewallRuleSet `json:"ruleset"`
	}
	body := map[string]any{"ruleset": map[string]any{"name": name}}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateFirewallRuleSet", path: "/v1/firewall/rulesets", body: body}, &out); err != nil {
		return nil, err
	}
	if out.RuleSet != nil && out.RuleSet.ID != "" {
		return out.RuleSet, nil
	}
	return c.FindFirewallRuleSet(ctx, name)
}

// GetFirewallRuleSet returns one ruleset.
func (c *Client) GetFirewallRuleSet(ctx context.Context, id string) (*FirewallRuleSet, error) {
	var out FirewallRuleSet
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetFirewallRuleSet", path: "/v1/firewall/rulesets/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, notFoundErr("firewall ruleset not found")
	}
	return &out, nil
}

// ListFirewallRuleSets returns the organization's rulesets. name is an
// optional case-insensitive substring filter; attachment is one of the
// FirewallRuleSets* constants.
func (c *Client) ListFirewallRuleSets(ctx context.Context, name, attachment string) ([]FirewallRuleSet, error) {
	var out struct {
		RuleSets []FirewallRuleSet `json:"rulesets"`
	}
	q := pageAll()
	if name != "" {
		q.Set("name", name)
	}
	if attachment != "" {
		q.Set("filterAttachment", attachment)
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListFirewallRuleSets", path: "/v1/firewall/rulesets", query: q, retryUnavailable: true}, &out)
	return out.RuleSets, err
}

// FindFirewallRuleSet returns the ruleset with exactly this name.
func (c *Client) FindFirewallRuleSet(ctx context.Context, name string) (*FirewallRuleSet, error) {
	sets, err := c.ListFirewallRuleSets(ctx, name, "")
	if err != nil {
		return nil, err
	}
	for i := range sets {
		if sets[i].Name == name {
			return &sets[i], nil
		}
	}
	return nil, notFoundErr("no firewall ruleset is named " + name)
}

// UpdateFirewallRuleSet renames a ruleset.
func (c *Client) UpdateFirewallRuleSet(ctx context.Context, id, name string) (*FirewallRuleSet, error) {
	var out FirewallRuleSet
	body := map[string]any{"ruleset": map[string]any{"name": name}}
	if err := c.do(ctx, request{method: http.MethodPatch, op: "UpdateFirewallRuleSet", path: "/v1/firewall/rulesets/" + url.PathEscape(id), body: body}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return c.GetFirewallRuleSet(ctx, id)
	}
	return &out, nil
}

// DeleteFirewallRuleSet deletes a ruleset and its rules. The API detaches any
// virtual machines that use it.
func (c *Client) DeleteFirewallRuleSet(ctx context.Context, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteFirewallRuleSet", path: "/v1/firewall/rulesets/" + url.PathEscape(id), query: q}, nil)
}

// --- Rules ---

// ListFirewallRules returns a ruleset's rules ordered by position.
func (c *Client) ListFirewallRules(ctx context.Context, rulesetID string) ([]FirewallRule, error) {
	var out struct {
		Rules []FirewallRule `json:"rules"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListFirewallRules", path: "/v1/firewall/rulesets/" + url.PathEscape(rulesetID) + "/rules", query: pageAll(), retryUnavailable: true}, &out)
	return out.Rules, err
}

// CreateFirewallRule adds a rule. The API always inserts it at position 0.
func (c *Client) CreateFirewallRule(ctx context.Context, rulesetID string, in FirewallRuleInput) (*FirewallRule, error) {
	var out struct {
		Rule *FirewallRule `json:"rule"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateFirewallRule", path: "/v1/firewall/rulesets/" + url.PathEscape(rulesetID) + "/rules",
		body: map[string]any{"rule": in}}, &out); err != nil {
		return nil, err
	}
	if out.Rule != nil && out.Rule.ID != "" {
		return out.Rule, nil
	}
	// Older API versions return only {success}. The new rule is at position 0;
	// callers hold a per-ruleset lock, so nothing else wrote in between.
	rules, err := c.ListFirewallRules(ctx, rulesetID)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].Position == 0 {
			return &rules[i], nil
		}
	}
	return nil, notFoundErr("the created firewall rule was not found")
}

// UpdateFirewallRule replaces a rule's body. Its position does not change.
func (c *Client) UpdateFirewallRule(ctx context.Context, id string, in FirewallRuleInput) (*FirewallRule, error) {
	var out FirewallRule
	if err := c.do(ctx, request{method: http.MethodPatch, op: "UpdateFirewallRule", path: "/v1/firewall/rules/" + url.PathEscape(id),
		body: map[string]any{"rule": in}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MoveFirewallRule moves a rule before or after another rule of the same ruleset.
func (c *Client) MoveFirewallRule(ctx context.Context, id, targetID, position string) (*FirewallRule, error) {
	var out FirewallRule
	if err := c.do(ctx, request{method: http.MethodPost, op: "MoveFirewallRule", path: "/v1/firewall/rules/" + url.PathEscape(id) + "/move",
		body: map[string]any{"targetRuleId": targetID, "position": position}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteFirewallRule deletes a rule.
func (c *Client) DeleteFirewallRule(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteFirewallRule", path: "/v1/firewall/rules/" + url.PathEscape(id)}, nil)
}
