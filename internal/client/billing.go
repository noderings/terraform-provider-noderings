package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CreditBalance is the organization's prepaid credit wallet. Amounts are decimal strings in USD.
type CreditBalance struct {
	Balance                 string `json:"balance"`
	Reserved                string `json:"reserved"`
	Available               string `json:"available"`
	Frozen                  bool   `json:"frozen"`
	FrozenReason            string `json:"frozenReason"`
	Currency                string `json:"currency"`
	PromotionalAvailable    string `json:"promotionalAvailable"`
	PromotionalExpiringSoon string `json:"promotionalExpiringSoon"`
}

// Subscription is the billing record behind one purchased asset.
type Subscription struct {
	ID                 string     `json:"id"`
	StartDate          *time.Time `json:"startDate,omitempty"`
	CurrentPeriodStart *time.Time `json:"currentPeriodStart,omitempty"`
	CurrentPeriodEnd   *time.Time `json:"currentPeriodEnd,omitempty"`
	CanceledAt         *time.Time `json:"canceledAt,omitempty"`
	SubscriptionStatus string     `json:"subscriptionStatus"`
	CancelAtPeriodEnd  bool       `json:"cancelAtPeriodEnd"`
	Plan               string     `json:"plan"`
	PlanName           string     `json:"planName"`
	PlanCode           string     `json:"planCode"`
	ResourceHostname   string     `json:"resourceHostname"`
	ResourceVMID       string     `json:"resourceVmId"`
	PlanBillingPeriod  string     `json:"planBillingPeriod"`
	RecurringAmount    string     `json:"recurringAmount"`
	Currency           string     `json:"currency"`
	BillingType        string     `json:"billingType"`
	HasUsageCharges    bool       `json:"hasUsageCharges"`
}

// EntitlementRule is one plan rule: a feature switch or a quota with its current usage.
type EntitlementRule struct {
	RuleKey      string `json:"ruleKey"`
	Effect       string `json:"effect"`
	BoolValue    bool   `json:"boolValue"`
	Int64Value   Int64  `json:"int64Value"`
	CurrentUsage Int64  `json:"currentUsage"`
}

// GetCreditBalance returns the organization's credit wallet.
func (c *Client) GetCreditBalance(ctx context.Context) (*CreditBalance, error) {
	var out CreditBalance
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetCreditBalance", path: "/v1/credits/balance", retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSubscriptions returns the organization's subscriptions, optionally only active ones.
func (c *Client) ListSubscriptions(ctx context.Context, onlyActive bool) ([]Subscription, error) {
	const size = 100
	var all []Subscription
	for page := 0; ; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(size)}}
		if onlyActive {
			q.Set("onlyActive", "true")
		}
		var out struct {
			Subscriptions []Subscription `json:"subscriptions"`
			TotalCount    int            `json:"totalCount"`
		}
		if err := c.do(ctx, request{method: http.MethodGet, op: "ListSubscriptions", path: "/v1/subscriptions", query: q, retryUnavailable: true}, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Subscriptions...)
		if len(out.Subscriptions) < size || len(all) >= out.TotalCount {
			return all, nil
		}
	}
}

// GetEntitlements returns the organization's plan rules and current usage.
func (c *Client) GetEntitlements(ctx context.Context) ([]EntitlementRule, error) {
	var out struct {
		Rules []EntitlementRule `json:"rules"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "GetSubscriptionStatus", path: "/v1/subscriptions:status", retryUnavailable: true}, &out)
	return out.Rules, err
}
