// Package firewall implements firewall address groups, rulesets and the
// ordered rules of a ruleset.
package firewall

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Resources lists this package's resources.
func Resources() []func() resource.Resource {
	return []func() resource.Resource{NewAddressGroupResource, NewRuleSetResource, NewRuleSetRulesResource}
}

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{NewAddressGroupDataSource, NewRuleSetDataSource, NewRuleSetsDataSource}
}

// --- Per-ruleset lock ---

// The API renumbers rule positions without locking, so concurrent writes to
// one ruleset can interleave. Every rule write in this provider holds the
// ruleset's lock.
var rulesetLocks sync.Map

func lockRuleSet(id string) func() {
	m, _ := rulesetLocks.LoadOrStore(id, &sync.Mutex{})
	mu, _ := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// --- CIDRs ---

// parsePrefix parses a CIDR or a bare address (taken as a single host).
func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			return netip.Prefix{}, fmt.Errorf("IPv4-mapped IPv6 addresses are not supported")
		}
		return p, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("zones are not supported")
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// canonicalCIDR returns the network address with its prefix length, with
// IPv6 compressed: "10.0.0.5/24" becomes "10.0.0.0/24".
func canonicalCIDR(s string) (string, error) {
	p, err := parsePrefix(s)
	if err != nil {
		return "", err
	}
	return p.Masked().String(), nil
}

// canonicalSet returns the sorted canonical form of a CIDR list. Unparseable
// entries are kept as written so they compare unequal.
func canonicalSet(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if c, err := canonicalCIDR(s); err == nil {
			out = append(out, c)
		} else {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cidrValidator checks a CIDR and warns when the API will store it differently.
type cidrValidator struct{ allowBare bool }

func (v cidrValidator) Description(context.Context) string {
	return "must be a CIDR such as 192.0.2.0/24 or 2001:db8::/32"
}

func (v cidrValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v cidrValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if !v.allowBare && !strings.Contains(s, "/") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR",
			fmt.Sprintf("%q is not a CIDR. Write it with a prefix length, for example %s/32 for a single IPv4 address.", s, s))
		return
	}
	p, err := parsePrefix(s)
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR",
			fmt.Sprintf("%q is not a valid IP address or CIDR (%s). Use a form such as 192.0.2.0/24 or 2001:db8::/32.", s, err))
		return
	}
	if v.allowBare {
		return
	}
	if c := p.Masked().String(); c != s {
		resp.Diagnostics.AddAttributeWarning(req.Path, "CIDR is not in canonical form",
			fmt.Sprintf("NodeRings stores %q as %q. Write %q to match what the API returns.", s, c, c))
	}
}

// --- Data source helpers ---

func setOfStrings(ctx context.Context, in []string, diags *diag.Diagnostics) types.Set {
	if in == nil {
		in = []string{}
	}
	s, d := types.SetValueFrom(ctx, types.StringType, in)
	diags.Append(d...)
	return s
}

// isAnyCIDR reports whether s is 0.0.0.0/0 or ::/0 (any address).
func isAnyCIDR(s string) bool {
	p, err := parsePrefix(s)
	return err == nil && p.Bits() == 0
}
