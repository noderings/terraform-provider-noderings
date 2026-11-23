package client

import (
	"context"
	"net/http"
	"net/url"
)

// PlanPrice is one price of a marketplace plan.
type PlanPrice struct {
	ID              string `json:"id"`
	PricingModel    string `json:"pricingModel"`
	ChargeTiming    string `json:"chargeTiming"`
	Currency        string `json:"currency"`
	UnitAmount      string `json:"unitAmount"`
	UnitName        string `json:"unitName"`
	BillingPeriod   string `json:"billingPeriod"`
	BillingInterval int32  `json:"billingInterval"`
	MinimumCharge   string `json:"minimumCharge"`
}

// PlanRegion is a region a plan is sold in.
type PlanRegion struct {
	RegionID string `json:"regionId"`
	Country  string `json:"country"`
	City     string `json:"city"`
}

// ProviderInfo is a hosting provider as listed on the marketplace. PlanID,
// PlanPrices and PlanRegions are only set by ListProviders.
type ProviderInfo struct {
	OrganizationID                string       `json:"organizationId"`
	OrganizationName              string       `json:"organizationName"`
	Description                   string       `json:"description"`
	Rating                        float64      `json:"rating"`
	TotalReviews                  int32        `json:"totalReviews"`
	PlanPrices                    []PlanPrice  `json:"planPrices"`
	PlanRegions                   []PlanRegion `json:"planRegions"`
	PlanID                        string       `json:"planId"`
	ProviderLogoURL               string       `json:"providerLogoUrl"`
	IPv6AvailableInFilteredRegion bool         `json:"ipv6AvailableInFilteredRegion"`
	PlatformDriver                string       `json:"platformDriver"`
	Capabilities                  []string     `json:"capabilities"`
}

// ProviderCatalogPlan is one plan in a provider's catalog.
type ProviderCatalogPlan struct {
	PlanID          string       `json:"planId"`
	PlanCode        string       `json:"planCode"`
	PlanName        string       `json:"planName"`
	PlanDescription string       `json:"planDescription"`
	BillingType     string       `json:"billingType"`
	ComputeType     string       `json:"computeType"`
	ComputeCategory string       `json:"computeCategory"`
	Regions         []PlanRegion `json:"regions"`
	PlanPrices      []PlanPrice  `json:"planPrices"`
	AssetType       string       `json:"assetType"`
}

// ProviderDetails is a provider with its full catalog.
type ProviderDetails struct {
	Provider         ProviderInfo          `json:"provider"`
	Website          string                `json:"website"`
	AvailableRegions []string              `json:"availableRegions"`
	AvailablePlans   []ProviderCatalogPlan `json:"availablePlans"`
	HasIPv6          bool                  `json:"hasIpv6"`
}

// AvailablePlanCode is a plan code for sale somewhere.
type AvailablePlanCode struct {
	PlanCode        string `json:"planCode"`
	BillingType     string `json:"billingType"`
	ComputeType     string `json:"computeType"`
	ComputeCategory string `json:"computeCategory"`
}

// MarketplacePlan is one sellable plan.
type MarketplacePlan struct {
	PlanID           string       `json:"planId"`
	PlanCode         string       `json:"planCode"`
	RegionID         string       `json:"regionId"`
	RegionCity       string       `json:"regionCity"`
	RegionCountry    string       `json:"regionCountry"`
	OrganizationID   string       `json:"organizationId"`
	OrganizationName string       `json:"organizationName"`
	BillingType      string       `json:"billingType"`
	VCPUs            int32        `json:"vcpus"`
	RAMGB            int32        `json:"ramGb"`
	DiskGB           int32        `json:"diskGb"`
	PlanPrices       []PlanPrice  `json:"planPrices"`
	PlanRegions      []PlanRegion `json:"planRegions"`
	Rating           float64      `json:"rating"`
	TotalReviews     int32        `json:"totalReviews"`
}

// AddonPrice is one price of an add-on.
type AddonPrice struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Currency      string `json:"currency"`
	UnitAmount    string `json:"unitAmount"`
	UnitName      string `json:"unitName"`
	BillingPeriod string `json:"billingPeriod"`
	PricingModel  string `json:"pricingModel"`
}

// Addon is an optional extra for a VM, such as backups.
type Addon struct {
	ID                  string       `json:"id"`
	Code                string       `json:"code"`
	Name                string       `json:"name"`
	Description         string       `json:"description"`
	AddonType           string       `json:"addonType"`
	CompatiblePlanCodes []string     `json:"compatiblePlanCodes"`
	AddonPrices         []AddonPrice `json:"addonPrices"`
}

// StorefrontFAQ is one question on a storefront.
type StorefrontFAQ struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// PublicStorefront is a provider's public storefront page.
type PublicStorefront struct {
	Slug                 string          `json:"slug"`
	ProviderID           string          `json:"providerId"`
	OrganizationName     string          `json:"organizationName"`
	Tagline              string          `json:"tagline"`
	AboutHTML            string          `json:"aboutHtml"`
	CoverImageURL        string          `json:"coverImageUrl"`
	LogoImageURL         string          `json:"logoImageUrl"`
	HighlightKeys        []string        `json:"highlightKeys"`
	HighlightCustomLines []string        `json:"highlightCustomLines"`
	FeaturedPlanIDs      []string        `json:"featuredPlanIds"`
	FAQs                 []StorefrontFAQ `json:"faqs"`
	Catalog              ProviderDetails `json:"catalog"`
	SocialURLs           []string        `json:"socialUrls"`
}

// InstantAppListing is an Instant App as returned by the list call, with its metadata.
type InstantAppListing struct {
	InstantApp
	Metadata map[string]any `json:"metadata"`
}

// ListProvidersFilter selects offers for ListProviders.
type ListProvidersFilter struct {
	PlanCode    string
	Country     string
	City        string
	BillingType string // API enum, optional
	IPv6Only    bool
}

// ListProviders returns the providers selling a plan code in a city. Without
// a billing type the API returns one arbitrary plan per provider.
func (c *Client) ListProviders(ctx context.Context, f ListProvidersFilter) ([]ProviderInfo, error) {
	q := pageAll()
	q.Set("filterPlanCode", f.PlanCode)
	q.Set("filterCountry", f.Country)
	q.Set("filterCity", f.City)
	if f.BillingType != "" {
		q.Set("filterBillingType", f.BillingType)
	}
	if f.IPv6Only {
		q.Set("filterIpv6AvailableOnly", "true")
	}
	var out struct {
		Providers []ProviderInfo `json:"providers"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListProviders", path: "/v1/marketplace/providers", query: q, retryUnavailable: true}, &out)
	return out.Providers, err
}

// GetProvider returns a provider with every plan and region it sells.
func (c *Client) GetProvider(ctx context.Context, id string) (*ProviderDetails, error) {
	var out struct {
		Provider *ProviderDetails `json:"provider"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetProvider", path: "/v1/marketplace/providers/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.Provider == nil {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: "provider not found"}
	}
	return out.Provider, nil
}

// ListProviderDirectory returns the marketplace's provider directory.
// assetType is an API enum or empty.
func (c *Client) ListProviderDirectory(ctx context.Context, assetType, search string) ([]ProviderInfo, error) {
	q := pageAll()
	if assetType != "" {
		q.Set("filterAssetType", assetType)
	}
	if search != "" {
		q.Set("search", search)
	}
	var out struct {
		Providers []ProviderInfo `json:"providers"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListMarketplaceProviderDirectory", path: "/v1/marketplace/provider-directory", query: q, retryUnavailable: true}, &out)
	return out.Providers, err
}

// ListAvailablePlans returns the plan codes for sale. Country and city only
// apply when both are set. All filters take API enums.
func (c *Client) ListAvailablePlans(ctx context.Context, country, city, billingType, assetType string) ([]AvailablePlanCode, error) {
	q := url.Values{}
	for k, v := range map[string]string{"filterCountry": country, "filterCity": city, "filterBillingType": billingType, "filterAssetType": assetType} {
		if v != "" {
			q.Set(k, v)
		}
	}
	var out struct {
		Plans []AvailablePlanCode `json:"plans"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListAvailablePlans", path: "/v1/marketplace/plans", query: q, retryUnavailable: true}, &out)
	return out.Plans, err
}

// GetMarketplacePlan returns one plan. It is NotFound once the plan is no longer for sale.
func (c *Client) GetMarketplacePlan(ctx context.Context, planID string) (*MarketplacePlan, error) {
	var out MarketplacePlan
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetMarketplacePlan", path: "/v1/marketplace/plans/" + url.PathEscape(planID), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAvailableAddons lists add-ons for a VM (vmID) or a provider. addonType
// is an API enum or empty. The API does not filter by plan compatibility.
func (c *Client) ListAvailableAddons(ctx context.Context, vmID, providerID, addonType, billingType string) ([]Addon, error) {
	q := pageAll()
	for k, v := range map[string]string{"vmId": vmID, "filterProviderId": providerID, "filterAddonType": addonType, "filterBillingType": billingType} {
		if v != "" {
			q.Set(k, v)
		}
	}
	var out struct {
		Addons []Addon `json:"addons"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListAvailableAddons", path: "/v1/marketplace/addons", query: q, retryUnavailable: true}, &out)
	return out.Addons, err
}

// GetPublicStorefront returns a provider's storefront by slug.
func (c *Client) GetPublicStorefront(ctx context.Context, slug string) (*PublicStorefront, error) {
	var out struct {
		Storefront *PublicStorefront `json:"storefront"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetPublicStorefront", path: "/v1/public/storefronts/" + url.PathEscape(slug), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.Storefront == nil {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: "storefront not found"}
	}
	return out.Storefront, nil
}

// ListInstantApps lists active Instant Apps. query matches name or slug,
// categories must all match, and distro is an API enum; all are optional.
func (c *Client) ListInstantApps(ctx context.Context, query string, categories []string, distro string) ([]InstantAppListing, error) {
	q := pageAll()
	if query != "" {
		q.Set("query", query)
	}
	for _, cat := range categories {
		q.Add("categories", cat)
	}
	if distro != "" {
		q.Set("distro", distro)
	}
	var out struct {
		InstantApps []InstantAppListing `json:"instantApps"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListInstantApps", path: "/v1/instantapps", query: q, retryUnavailable: true}, &out)
	return out.InstantApps, err
}
