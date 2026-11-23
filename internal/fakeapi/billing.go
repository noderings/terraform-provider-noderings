package fakeapi

import (
	"net/http"
	"sort"
	"strconv"
)

// SubscribedVMID is a virtual machine the fake billing API has an active
// monthly subscription for.
const SubscribedVMID = "5e6f0000-0000-4000-8000-000000000001"

// SubscriptionOptions describes a subscription added with AddSubscription.
type SubscriptionOptions struct {
	VMID     string
	Hostname string
	// Usage makes it an hourly, usage-billed subscription; otherwise it is monthly.
	Usage bool
	// Canceled marks it canceled, so active-only listings skip it.
	Canceled bool
}

type billingState struct {
	credit map[string]any
	subs   map[string]map[string]any
	// limits overrides entitlement limits by rule key.
	limits map[string]int64
}

func billing(s *Server) *billingState {
	return state(s, "billing", func() *billingState {
		b := &billingState{
			credit: map[string]any{"balance": "125.50", "reserved": "5.50", "available": "120.00", "frozen": false, "frozenReason": "",
				"currency": "USD", "promotionalAvailable": "10.00", "promotionalExpiringSoon": "0.00"},
			subs:   map[string]map[string]any{},
			limits: map[string]int64{},
		}
		b.add(s, SubscriptionOptions{VMID: SubscribedVMID, Hostname: "web-1"})
		return b
	})
}

func (b *billingState) add(s *Server, o SubscriptionOptions) string {
	id := s.id()
	sub := map[string]any{
		"id": id, "startDate": "2026-11-01T00:00:00Z", "currentPeriodStart": "2026-11-01T00:00:00Z", "currentPeriodEnd": "2026-12-01T00:00:00Z",
		"subscriptionStatus": "SUBSCRIPTION_STATUS_ACTIVE", "cancelAtPeriodEnd": false, "quantity": 1, "organization": OrgID,
		"plan": VMPlanSubscriptionFRA, "planName": "General compute 2 vCPU", "planCode": VMPlanCode,
		"resourceHostname": o.Hostname, "resourceProviderName": ProviderName, "resourceVmId": o.VMID, "resourceRegionName": "Frankfurt",
		"planBillingPeriod": "BILLING_PERIOD_MONTH", "recurringAmount": "12.00", "currency": "USD",
		"billingType": "BILLING_TYPE_SUBSCRIPTION", "pricingModel": "PRICING_MODEL_FIXED", "hasUsageCharges": false, "addonSubscriptions": []any{},
	}
	if o.Usage {
		sub["plan"] = VMPlanUsageFRAID
		sub["planBillingPeriod"] = "BILLING_PERIOD_HOUR"
		sub["recurringAmount"] = "0.02"
		sub["billingType"] = "BILLING_TYPE_USAGE"
		sub["pricingModel"] = "PRICING_MODEL_USAGE_BASED"
		sub["hasUsageCharges"] = true
	}
	if o.Canceled {
		sub["subscriptionStatus"] = "SUBSCRIPTION_STATUS_CANCELED"
		sub["canceledAt"] = "2026-11-10T00:00:00Z"
	}
	b.subs[id] = sub
	return id
}

// AddSubscription adds a subscription for a virtual machine and returns its ID.
func (s *Server) AddSubscription(o SubscriptionOptions) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return billing(s).add(s, o)
}

// SetCreditAvailable changes the available credit, for example "3.25".
func (s *Server) SetCreditAvailable(amount string, frozen bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := billing(s)
	b.credit["available"] = amount
	b.credit["frozen"] = frozen
	if frozen {
		b.credit["frozenReason"] = "Payment overdue"
	} else {
		b.credit["frozenReason"] = ""
	}
}

// SetEntitlementLimit overrides one quota limit by rule key.
func (s *Server) SetEntitlementLimit(key string, limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	billing(s).limits[key] = limit
}

func (b *billingState) rules(s *Server) []any {
	limit := func(key string, def int64) int64 {
		if v, ok := b.limits[key]; ok {
			return v
		}
		return def
	}
	quota := func(key string, def int64, used int) map[string]any {
		return map[string]any{"ruleKey": key, "effect": "RULE_EFFECT_LIMIT", "boolValue": false,
			"int64Value": strconv.FormatInt(limit(key, def), 10), "currentUsage": strconv.Itoa(used)}
	}
	return []any{
		quota("client.max_virtual_machines", 10, 1),
		quota("client.max_dns_zones", 25, len(s.zones)),
		quota("client.max_ssh_keys", 50, len(s.keys)),
		map[string]any{"ruleKey": "client.backups", "effect": "RULE_EFFECT_ENABLE", "boolValue": true, "int64Value": "0", "currentUsage": "0"},
	}
}

func init() {
	register(func(s *Server, w http.ResponseWriter, r *http.Request, p []string) bool {
		if len(p) < 2 || p[0] != "v1" || r.Method != http.MethodGet {
			return false
		}
		switch {
		case len(p) == 3 && p[1] == "credits" && p[2] == "balance":
			writeJSON(w, 200, billing(s).credit)
		case len(p) == 2 && p[1] == "subscriptions:status":
			writeJSON(w, 200, map[string]any{"rules": billing(s).rules(s)})
		case len(p) == 2 && p[1] == "subscriptions":
			b := billing(s)
			q := r.URL.Query()
			ids := make([]string, 0, len(b.subs))
			for id, sub := range b.subs {
				if q.Get("onlyActive") == "true" && sub["subscriptionStatus"] != "SUBSCRIPTION_STATUS_ACTIVE" {
					continue
				}
				ids = append(ids, id)
			}
			sort.Strings(ids)
			size, _ := strconv.Atoi(q.Get("pageSize"))
			if size <= 0 {
				size = 10
			}
			page, _ := strconv.Atoi(q.Get("page"))
			start := min(page*size, len(ids))
			end := min(start+size, len(ids))
			list := []any{}
			for _, id := range ids[start:end] {
				list = append(list, b.subs[id])
			}
			writeJSON(w, 200, map[string]any{"subscriptions": list, "totalCount": len(ids)})
		default:
			return false
		}
		return true
	})
}
