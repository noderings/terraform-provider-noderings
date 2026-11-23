package fakeapi

import (
	"net"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Firewall: address groups, rulesets and their ordered rules.
//
// Like the real API: group and ruleset names are unique (AlreadyExists),
// create calls return the object, rule positions are dense 0..n-1 across
// both directions, a new rule always goes to position 0, a group that a rule
// references cannot be deleted, and list calls ignore paging but reject a
// missing or zero pageSize.

type fwGroup struct {
	ID       string
	Name     string
	Prefixes []*net.IPNet
	Created  string
	Updated  string
}

type fwRuleSet struct {
	ID        string
	Name      string
	Rules     []*fwRule // ordered by position
	Hostnames []string  // attached virtual machines
	Created   string
	Updated   string
}

type fwRule struct {
	ID      string
	Body    fwRuleInput
	Created string
	Updated string
}

type fwEndpoint struct {
	IPs *struct {
		IPs []string `json:"ips"`
	} `json:"ips,omitempty"`
	AddressGroupIDs *struct {
		AddressGroupIDs []string `json:"addressGroupIds"`
	} `json:"addressGroupIds,omitempty"`
	VMTags *struct {
		Tags []string `json:"tags"`
	} `json:"vmTags,omitempty"`
}

type fwRuleInput struct {
	Action           string      `json:"action"`
	Protocol         string      `json:"protocol"`
	Direction        string      `json:"direction"`
	Source           *fwEndpoint `json:"source,omitempty"`
	Destination      *fwEndpoint `json:"destination,omitempty"`
	SourcePorts      string      `json:"sourcePorts,omitempty"`
	DestinationPorts string      `json:"destinationPorts,omitempty"`
}

type firewallState struct {
	groups   map[string]*fwGroup
	rulesets map[string]*fwRuleSet
}

func fw(s *Server) *firewallState {
	return state(s, "firewall", func() *firewallState {
		return &firewallState{groups: map[string]*fwGroup{}, rulesets: map[string]*fwRuleSet{}}
	})
}

func init() {
	register(func(s *Server, w http.ResponseWriter, r *http.Request, p []string) bool {
		if len(p) < 3 || p[0] != "v1" || p[1] != "firewall" {
			return false
		}
		st := fw(s)
		switch p[2] {
		case "address-groups":
			st.addressGroups(s, w, r, p)
		case "rulesets":
			st.ruleSets(s, w, r, p)
		case "rules":
			st.rules(w, r, p)
		default:
			return false
		}
		return true
	})
}

// listPageSizeOK mimics the API's validation: pageSize is required and non-zero.
func listPageSizeOK(w http.ResponseWriter, r *http.Request) bool {
	if ps := r.URL.Query().Get("pageSize"); ps == "" || ps == "0" {
		apiError(w, 400, 3, "invalid ListRequest.PageSize: value must not be 0")
		return false
	}
	return true
}

// --- Address groups ---

func (g *fwGroup) view() map[string]any {
	prefixes := []any{}
	for _, n := range g.Prefixes {
		ones, _ := n.Mask.Size()
		prefixes = append(prefixes, map[string]any{"ip": n.IP.String(), "prefix": strconv.Itoa(ones)})
	}
	return map[string]any{"id": g.ID, "name": g.Name, "prefixes": prefixes,
		"timestamps": map[string]any{"createTime": g.Created, "updateTime": g.Updated}}
}

type fwGroupInput struct {
	Group *struct {
		Name     string `json:"name"`
		Prefixes []struct {
			IP     string  `json:"ip"`
			Prefix flexInt `json:"prefix"`
		} `json:"prefixes"`
	} `json:"group"`
}

// parseGroup validates a group body and returns its canonical prefixes.
func parseGroup(w http.ResponseWriter, in fwGroupInput) (string, []*net.IPNet, bool) {
	if in.Group == nil {
		apiError(w, 400, 3, "invalid request: group is required")
		return "", nil, false
	}
	name := strings.TrimSpace(in.Group.Name)
	if name == "" {
		apiError(w, 400, 3, "Address group name is required.")
		return "", nil, false
	}
	if len(name) > 63 {
		// The real API reports this database constraint as an internal error.
		apiError(w, 500, 13, "Failed to save address group.")
		return "", nil, false
	}
	if len(in.Group.Prefixes) == 0 {
		apiError(w, 400, 3, "At least one prefix is required.")
		return "", nil, false
	}
	var out []*net.IPNet
	seen := map[string]bool{}
	for _, p := range in.Group.Prefixes {
		ip := net.ParseIP(p.IP)
		bits := 32
		if ip != nil && ip.To4() == nil {
			bits = 128
		}
		if ip == nil || p.Prefix < 0 || int(p.Prefix) > bits {
			apiError(w, 400, 3, "Invalid prefix "+p.IP+"/"+strconv.Itoa(int(p.Prefix))+".")
			return "", nil, false
		}
		_, n, _ := net.ParseCIDR(p.IP + "/" + strconv.Itoa(int(p.Prefix)))
		if seen[n.String()] {
			apiError(w, 400, 3, "Duplicate prefix "+n.String()+".")
			return "", nil, false
		}
		seen[n.String()] = true
		out = append(out, n)
	}
	return name, out, true
}

func (st *firewallState) groupNameTaken(name, except string) bool {
	for _, g := range st.groups {
		if g.Name == name && g.ID != except {
			return true
		}
	}
	return false
}

func (st *firewallState) addressGroups(s *Server, w http.ResponseWriter, r *http.Request, p []string) {
	switch {
	case len(p) == 3 && r.Method == http.MethodPost:
		var in fwGroupInput
		if !decode(w, r, &in) {
			return
		}
		name, prefixes, ok := parseGroup(w, in)
		if !ok {
			return
		}
		if st.groupNameTaken(name, "") {
			apiError(w, 409, 6, "Address group with this name already exists.")
			return
		}
		g := &fwGroup{ID: s.id(), Name: name, Prefixes: prefixes, Created: now(), Updated: now()}
		st.groups[g.ID] = g
		writeJSON(w, 200, map[string]any{"success": true, "group": g.view()})
	case len(p) == 3 && r.Method == http.MethodGet:
		if !listPageSizeOK(w, r) {
			return
		}
		filter := strings.ToLower(r.URL.Query().Get("name"))
		list := []any{}
		for _, g := range sortedGroups(st.groups) {
			if filter == "" || strings.Contains(strings.ToLower(g.Name), filter) {
				list = append(list, g.view())
			}
		}
		writeJSON(w, 200, map[string]any{"groups": list, "totalCount": len(list)})
	case len(p) == 4:
		g := st.groups[p[3]]
		if g == nil {
			notFound(w, "Address group")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, g.view())
		case http.MethodPatch:
			var in fwGroupInput
			if !decode(w, r, &in) {
				return
			}
			name, prefixes, ok := parseGroup(w, in)
			if !ok {
				return
			}
			if name != g.Name && st.groupNameTaken(name, g.ID) {
				apiError(w, 409, 6, "Address group with this name already exists.")
				return
			}
			g.Name, g.Prefixes, g.Updated = name, prefixes, now()
			writeJSON(w, 200, g.view())
		case http.MethodDelete:
			for _, rs := range st.rulesets {
				for _, rule := range rs.Rules {
					if rule.Body.references(g.ID) {
						apiError(w, 400, 9, "Address group is in use by firewall rules.")
						return
					}
				}
			}
			delete(st.groups, g.ID)
			writeJSON(w, 200, map[string]any{"success": true})
		default:
			apiError(w, 404, 5, "Not Found")
		}
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func sortedGroups(m map[string]*fwGroup) []*fwGroup {
	out := make([]*fwGroup, 0, len(m))
	for _, g := range m {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// --- Rulesets ---

func (rs *fwRuleSet) view() map[string]any {
	hosts := append([]string{}, rs.Hostnames...)
	sort.Strings(hosts)
	return map[string]any{"id": rs.ID, "name": rs.Name, "ruleCount": len(rs.Rules), "attachedVmCount": len(hosts),
		"attachedVmHostnames": hosts, "timestamps": map[string]any{"createTime": rs.Created, "updateTime": rs.Updated}}
}

type fwRuleSetInput struct {
	RuleSet *struct {
		Name string `json:"name"`
	} `json:"ruleset"`
}

func (st *firewallState) ruleSetNameTaken(name, except string) bool {
	for _, rs := range st.rulesets {
		if rs.Name == name && rs.ID != except {
			return true
		}
	}
	return false
}

func (st *firewallState) ruleSets(s *Server, w http.ResponseWriter, r *http.Request, p []string) {
	switch {
	case len(p) == 3 && r.Method == http.MethodPost:
		var in fwRuleSetInput
		if !decode(w, r, &in) {
			return
		}
		if in.RuleSet == nil {
			apiError(w, 400, 3, "invalid request: ruleset is required")
			return
		}
		id := s.id()
		name := in.RuleSet.Name
		if name == "" {
			name = "firewallruleset-" + id
		}
		if len(name) > 63 {
			apiError(w, 500, 13, "Failed to create firewall ruleset.")
			return
		}
		if st.ruleSetNameTaken(name, "") {
			apiError(w, 409, 6, "Firewall ruleset with this name already exists.")
			return
		}
		rs := &fwRuleSet{ID: id, Name: name, Created: now(), Updated: now()}
		st.rulesets[id] = rs
		writeJSON(w, 200, map[string]any{"success": true, "ruleset": rs.view()})
	case len(p) == 3 && r.Method == http.MethodGet:
		if !listPageSizeOK(w, r) {
			return
		}
		q := r.URL.Query()
		filter := strings.ToLower(q.Get("name"))
		list := []any{}
		for _, rs := range st.rulesets {
			if filter != "" && !strings.Contains(strings.ToLower(rs.Name), filter) {
				continue
			}
			switch q.Get("filterAttachment") {
			case "FIREWALL_RULESET_ATTACHMENT_FILTER_ATTACHED":
				if len(rs.Hostnames) == 0 {
					continue
				}
			case "FIREWALL_RULESET_ATTACHMENT_FILTER_UNATTACHED":
				if len(rs.Hostnames) > 0 {
					continue
				}
			}
			list = append(list, rs.view())
		}
		writeJSON(w, 200, map[string]any{"rulesets": list, "totalCount": len(list)})
	case len(p) >= 4:
		rs := st.rulesets[p[3]]
		if rs == nil {
			notFound(w, "Firewall ruleset")
			return
		}
		if len(p) == 5 && p[4] == "rules" {
			st.ruleSetRules(s, w, r, rs)
			return
		}
		if len(p) != 4 {
			apiError(w, 404, 5, "Not Found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, rs.view())
		case http.MethodPatch:
			var in fwRuleSetInput
			if !decode(w, r, &in) {
				return
			}
			if in.RuleSet == nil || in.RuleSet.Name == "" || len(in.RuleSet.Name) > 63 {
				apiError(w, 500, 13, "Failed to update firewall ruleset.")
				return
			}
			if in.RuleSet.Name != rs.Name && st.ruleSetNameTaken(in.RuleSet.Name, rs.ID) {
				apiError(w, 409, 6, "Firewall ruleset with this name already exists.")
				return
			}
			rs.Name, rs.Updated = in.RuleSet.Name, now()
			writeJSON(w, 200, rs.view())
		case http.MethodDelete:
			// The real API ignores force and silently detaches VMs.
			delete(st.rulesets, rs.ID)
			writeJSON(w, 200, map[string]any{"success": true})
		default:
			apiError(w, 404, 5, "Not Found")
		}
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

// --- Rules ---

func (rs *fwRuleSet) ruleView(i int) map[string]any {
	rule := rs.Rules[i]
	return map[string]any{"id": rule.ID, "rulesetId": rs.ID, "rule": rule.Body.view(), "position": strconv.Itoa(i),
		"timestamps": map[string]any{"createTime": rule.Created, "updateTime": rule.Updated}}
}

// view echoes the stored rule, omitting empty sub-objects like the API.
func (in fwRuleInput) view() map[string]any {
	v := map[string]any{"action": in.Action, "protocol": in.Protocol, "direction": in.Direction,
		"sourcePorts": in.SourcePorts, "destinationPorts": in.DestinationPorts}
	if e := in.Source.view(); e != nil {
		v["source"] = e
	}
	if e := in.Destination.view(); e != nil {
		v["destination"] = e
	}
	return v
}

func (e *fwEndpoint) view() map[string]any {
	if e == nil {
		return nil
	}
	v := map[string]any{}
	if e.IPs != nil && len(e.IPs.IPs) > 0 {
		v["ips"] = map[string]any{"ips": e.IPs.IPs}
	}
	if e.AddressGroupIDs != nil && len(e.AddressGroupIDs.AddressGroupIDs) > 0 {
		v["addressGroupIds"] = map[string]any{"addressGroupIds": e.AddressGroupIDs.AddressGroupIDs}
	}
	if e.VMTags != nil && len(e.VMTags.Tags) > 0 {
		v["vmTags"] = map[string]any{"tags": e.VMTags.Tags}
	}
	if len(v) == 0 {
		return nil
	}
	return v
}

func (e *fwEndpoint) ips() []string {
	if e == nil || e.IPs == nil {
		return nil
	}
	return e.IPs.IPs
}

func (e *fwEndpoint) groups() []string {
	if e == nil || e.AddressGroupIDs == nil {
		return nil
	}
	return e.AddressGroupIDs.AddressGroupIDs
}

func (e *fwEndpoint) tags() []string {
	if e == nil || e.VMTags == nil {
		return nil
	}
	return e.VMTags.Tags
}

func (e *fwEndpoint) empty() bool {
	return len(e.ips()) == 0 && len(e.groups()) == 0 && len(e.tags()) == 0
}

func (in fwRuleInput) references(groupID string) bool {
	return slices.Contains(in.Source.groups(), groupID) || slices.Contains(in.Destination.groups(), groupID)
}

var fwPortRE = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)

func validPorts(s string) bool {
	if s == "" {
		return true
	}
	m := fwPortRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	lo, _ := strconv.Atoi(m[1])
	hi := lo
	if m[2] != "" {
		hi, _ = strconv.Atoi(m[2])
	}
	return lo >= 1 && hi <= 65535 && lo <= hi
}

// validateRule applies the API's rule checks and reports the first failure.
func (st *firewallState) validateRule(w http.ResponseWriter, in fwRuleInput) bool {
	bad := func(msg string) bool { apiError(w, 400, 3, msg); return false }
	if in.Action != "FIREWALL_ACTION_ACCEPT" && in.Action != "FIREWALL_ACTION_DROP" {
		return bad("Invalid action.")
	}
	if in.Direction != "FIREWALL_DIRECTION_INGRESS" && in.Direction != "FIREWALL_DIRECTION_EGRESS" {
		return bad("Invalid direction.")
	}
	if !strings.HasPrefix(in.Protocol, "FIREWALL_PROTOCOL_") || in.Protocol == "FIREWALL_PROTOCOL_UNSPECIFIED" {
		return bad("Invalid protocol.")
	}
	if in.Source.empty() && in.Destination.empty() {
		return bad("Either source or destination must be set.")
	}
	for _, side := range []struct {
		name string
		e    *fwEndpoint
	}{{"source", in.Source}, {"destination", in.Destination}} {
		if side.e != nil && side.e.empty() {
			return bad("Invalid " + side.name + " address.")
		}
		ips := side.e.ips()
		anyCount := 0
		for _, ip := range ips {
			if net.ParseIP(ip) == nil {
				if _, _, err := net.ParseCIDR(ip); err != nil {
					return bad("Invalid " + side.name + " address.")
				}
			}
			if ip == "0.0.0.0/0" || ip == "::/0" {
				anyCount++
			}
		}
		total := len(ips) + len(side.e.groups()) + len(side.e.tags())
		if anyCount > 0 && total > 1 {
			return bad("Invalid " + side.name + " address.")
		}
		for _, id := range side.e.groups() {
			if st.groups[id] == nil {
				apiError(w, 404, 5, "Address group was not found.")
				return false
			}
		}
		for _, t := range side.e.tags() {
			if len(t) < 1 || len(t) > 100 {
				return bad("Invalid " + side.name + " address.")
			}
		}
	}
	if !validPorts(in.SourcePorts) || !validPorts(in.DestinationPorts) {
		return bad("Invalid port specification.")
	}
	if in.SourcePorts != "" || in.DestinationPorts != "" {
		switch in.Protocol {
		case "FIREWALL_PROTOCOL_TCP", "FIREWALL_PROTOCOL_UDP", "FIREWALL_PROTOCOL_SCTP", "FIREWALL_PROTOCOL_DCCP", "FIREWALL_PROTOCOL_UDPLITE":
		default:
			return bad("Ports are only supported for TCP, UDP, SCTP, DCCP and UDPLITE.")
		}
	}
	return true
}

func (st *firewallState) ruleSetRules(s *Server, w http.ResponseWriter, r *http.Request, rs *fwRuleSet) {
	switch r.Method {
	case http.MethodGet:
		if !listPageSizeOK(w, r) {
			return
		}
		dir := r.URL.Query().Get("direction")
		list := []any{}
		for i, rule := range rs.Rules {
			if dir == "" || dir == "FIREWALL_DIRECTION_UNSPECIFIED" || rule.Body.Direction == dir {
				list = append(list, rs.ruleView(i))
			}
		}
		writeJSON(w, 200, map[string]any{"rules": list, "totalCount": len(list)})
	case http.MethodPost:
		var in struct {
			Rule *fwRuleInput `json:"rule"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Rule == nil {
			apiError(w, 400, 3, "invalid request: rule is required")
			return
		}
		if !st.validateRule(w, *in.Rule) {
			return
		}
		rule := &fwRule{ID: s.id(), Body: *in.Rule, Created: now(), Updated: now()}
		rs.Rules = append([]*fwRule{rule}, rs.Rules...)
		writeJSON(w, 200, map[string]any{"success": true, "rule": rs.ruleView(0)})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (st *firewallState) findRule(id string) (*fwRuleSet, int) {
	for _, rs := range st.rulesets {
		for i, rule := range rs.Rules {
			if rule.ID == id {
				return rs, i
			}
		}
	}
	return nil, -1
}

func (st *firewallState) rules(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) < 4 {
		apiError(w, 404, 5, "Not Found")
		return
	}
	rs, i := st.findRule(p[3])
	if rs == nil {
		notFound(w, "Firewall rule")
		return
	}
	switch {
	case len(p) == 5 && p[4] == "move" && r.Method == http.MethodPost:
		var in struct {
			TargetRuleID string `json:"targetRuleId"`
			Position     string `json:"position"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.TargetRuleID == p[3] {
			apiError(w, 400, 3, "A rule cannot be moved relative to itself.")
			return
		}
		if in.Position != "FIREWALL_RULE_POSITION_BEFORE" && in.Position != "FIREWALL_RULE_POSITION_AFTER" {
			apiError(w, 400, 3, "Invalid position.")
			return
		}
		trs, _ := st.findRule(in.TargetRuleID)
		if trs == nil {
			notFound(w, "Target firewall rule")
			return
		}
		if trs != rs {
			apiError(w, 400, 3, "Both rules must belong to the same ruleset.")
			return
		}
		moved := rs.Rules[i]
		rest := slices.Delete(slices.Clone(rs.Rules), i, i+1)
		t := slices.IndexFunc(rest, func(x *fwRule) bool { return x.ID == in.TargetRuleID })
		if in.Position == "FIREWALL_RULE_POSITION_AFTER" {
			t++
		}
		rs.Rules = slices.Insert(rest, t, moved)
		writeJSON(w, 200, rs.ruleView(t))
	case len(p) == 4 && r.Method == http.MethodGet:
		writeJSON(w, 200, rs.ruleView(i))
	case len(p) == 4 && r.Method == http.MethodPatch:
		var in struct {
			Rule *fwRuleInput `json:"rule"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Rule == nil {
			apiError(w, 400, 3, "invalid request: rule is required")
			return
		}
		if !st.validateRule(w, *in.Rule) {
			return
		}
		rs.Rules[i].Body, rs.Rules[i].Updated = *in.Rule, now()
		writeJSON(w, 200, rs.ruleView(i))
	case len(p) == 4 && r.Method == http.MethodDelete:
		rs.Rules = slices.Delete(rs.Rules, i, i+1)
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

// --- Out-of-band helpers for tests ---

// SetFirewallRuleSetAttachedVMs sets the virtual machines attached to a
// ruleset, as if they were attached in the console.
func (s *Server) SetFirewallRuleSetAttachedVMs(rulesetID string, hostnames ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs := fw(s).rulesets[rulesetID]; rs != nil {
		rs.Hostnames = append([]string{}, hostnames...)
	}
}

// DeleteFirewallAddressGroupOutOfBand removes an address group.
func (s *Server) DeleteFirewallAddressGroupOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(fw(s).groups, id)
}

// SetFirewallAddressGroupOutOfBand replaces an address group's CIDRs.
func (s *Server) SetFirewallAddressGroupOutOfBand(id string, cidrs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := fw(s).groups[id]
	if g == nil {
		return
	}
	g.Prefixes = nil
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			g.Prefixes = append(g.Prefixes, n)
		}
	}
}

// DeleteFirewallRuleSetOutOfBand removes a ruleset and its rules.
func (s *Server) DeleteFirewallRuleSetOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(fw(s).rulesets, id)
}

// RenameFirewallRuleSetOutOfBand renames a ruleset.
func (s *Server) RenameFirewallRuleSetOutOfBand(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs := fw(s).rulesets[id]; rs != nil {
		rs.Name = name
	}
}

// AddFirewallRuleOutOfBand inserts an ingress rule at position 0, as the
// console would, and returns its ID. source is a CIDR or an address group ID.
func (s *Server) AddFirewallRuleOutOfBand(rulesetID, action, source string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := fw(s)
	rs := st.rulesets[rulesetID]
	if rs == nil {
		return ""
	}
	src := &fwEndpoint{}
	if st.groups[source] != nil {
		src.AddressGroupIDs = &struct {
			AddressGroupIDs []string `json:"addressGroupIds"`
		}{[]string{source}}
	} else {
		src.IPs = &struct {
			IPs []string `json:"ips"`
		}{[]string{source}}
	}
	rule := &fwRule{ID: s.id(), Created: now(), Updated: now(), Body: fwRuleInput{
		Action: "FIREWALL_ACTION_" + strings.ToUpper(action), Protocol: "FIREWALL_PROTOCOL_ANY",
		Direction: "FIREWALL_DIRECTION_INGRESS", Source: src}}
	rs.Rules = append([]*fwRule{rule}, rs.Rules...)
	return rule.ID
}

// DeleteFirewallRuleOutOfBand removes a rule.
func (s *Server) DeleteFirewallRuleOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs, i := fw(s).findRule(id); rs != nil {
		rs.Rules = slices.Delete(rs.Rules, i, i+1)
	}
}

// FirewallRuleIDs returns a ruleset's rule IDs in position order.
func (s *Server) FirewallRuleIDs(rulesetID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	if rs := fw(s).rulesets[rulesetID]; rs != nil {
		for _, r := range rs.Rules {
			ids = append(ids, r.ID)
		}
	}
	return ids
}
