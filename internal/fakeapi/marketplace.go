package fakeapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Extra marketplace fixtures used only by the compute tests.
const (
	// VMFreePlanCode is a free plan sold by Example Hosting in Frankfurt. The
	// API ignores idempotency keys on free plans.
	VMFreePlanCode  = "SC-GC-1C-1G-20"
	VMFreePlanFRAID = "7c3a0000-0000-4000-8000-000000000020"

	// BudgetProviderID is a cheaper VirtFusion provider that also sells
	// VMPlanCode (usage) in Frankfurt, without IPv6, firewall, backups or snapshots.
	BudgetProviderID       = "5a1e0000-0000-4000-8000-000000000002"
	BudgetProviderName     = "Budget Cloud"
	BudgetStorefrontSlug   = "budget-cloud"
	BudgetRegionFRAID      = "6b2f0000-0000-4000-8000-000000000010"
	BudgetVMPlanUsageFRAID = "7c3a0000-0000-4000-8000-000000000030"

	// LargeBackupAddonID is a backup add-on only compatible with a plan code
	// nobody sells, to check client-side compatibility filtering.
	LargeBackupAddonID = "9e5c0000-0000-4000-8000-000000000003"

	// WordPressAppID is the Instant App served by the fake.
	WordPressAppID = "7a9e0000-0000-4000-8000-000000000001"
)

type fakeRegion struct {
	id, country, city string
}

type fakePlan struct {
	id, code, name, billing, asset string
	provider                       string
	region                         fakeRegion
	vcpus, ramGB, diskGB           int
	price                          map[string]any
}

type fakeProvider struct {
	id, name, slug, driver string
	caps                   []string
	ipv6                   bool
	rating                 float64
	reviews                int
}

var (
	fraRegion       = fakeRegion{RegionFrankfurtID, "Germany", "Frankfurt"}
	amsRegion       = fakeRegion{RegionAmsterdamID, "Netherlands", "Amsterdam"}
	budgetFRARegion = fakeRegion{BudgetRegionFRAID, "Germany", "Frankfurt"}

	fakeProviders = []fakeProvider{
		{id: ProviderID, name: ProviderName, slug: StorefrontSlug, driver: "PLATFORM_DRIVER_PROXMOX",
			caps: []string{"guest_firewall", "instant_apps", "on_demand_backup", "snapshots"}, ipv6: true, rating: 4.6, reviews: 120},
		{id: BudgetProviderID, name: BudgetProviderName, slug: BudgetStorefrontSlug, driver: "PLATFORM_DRIVER_VIRTFUSION",
			caps: []string{"instant_apps"}, ipv6: false, rating: 3.9, reviews: 14},
	}

	fakePlans = []fakePlan{
		{id: VMPlanUsageFRAID, code: VMPlanCode, name: "General Compute 2", billing: "BILLING_TYPE_USAGE", asset: "ASSET_TYPE_VM", provider: ProviderID, region: fraRegion,
			vcpus: 2, ramGB: 4, diskGB: 80, price: hourly("0.0120")},
		{id: VMPlanUsageAMSID, code: VMPlanCode, name: "General Compute 2", billing: "BILLING_TYPE_USAGE", asset: "ASSET_TYPE_VM", provider: ProviderID, region: amsRegion,
			vcpus: 2, ramGB: 4, diskGB: 80, price: hourly("0.0130")},
		{id: VMPlanSubscriptionFRA, code: VMPlanCode, name: "General Compute 2 (monthly)", billing: "BILLING_TYPE_SUBSCRIPTION", asset: "ASSET_TYPE_VM", provider: ProviderID, region: fraRegion,
			vcpus: 2, ramGB: 4, diskGB: 80, price: monthly("6.00")},
		{id: VMFreePlanFRAID, code: VMFreePlanCode, name: "Free Tier", billing: "BILLING_TYPE_FREE", asset: "ASSET_TYPE_VM", provider: ProviderID, region: fraRegion,
			vcpus: 1, ramGB: 1, diskGB: 20, price: monthly("0.00")},
		{id: GamePlanID, code: GamePlanCode, name: "Game Server 4", billing: "BILLING_TYPE_USAGE", asset: "ASSET_TYPE_GAMESERVER", provider: ProviderID, region: fraRegion,
			vcpus: 4, ramGB: 8, diskGB: 50, price: hourly("0.0300")},
		{id: BudgetVMPlanUsageFRAID, code: VMPlanCode, name: "Budget 2", billing: "BILLING_TYPE_USAGE", asset: "ASSET_TYPE_VM", provider: BudgetProviderID, region: budgetFRARegion,
			vcpus: 2, ramGB: 4, diskGB: 80, price: hourly("0.0100")},
	}

	fakeAddons = []map[string]any{
		{"id": BackupAddonID, "code": "backup-daily", "name": "Daily backups", "description": "Daily backups, 7 kept.", "addonType": "ADDON_TYPE_BACKUP",
			"compatiblePlanCodes": []string{VMPlanCode, VMFreePlanCode}, "addonPrices": []any{addonPrice("2.00")}},
		{"id": LargeBackupAddonID, "code": "backup-large", "name": "Large backups", "description": "For large plans.", "addonType": "ADDON_TYPE_BACKUP",
			"compatiblePlanCodes": []string{"SC-GC-16C-64G-640"}, "addonPrices": []any{addonPrice("9.00")}},
		{"id": AdditionalIPAddonID, "code": "extra-ipv4", "name": "Additional IPv4", "description": "One more IPv4 address.", "addonType": "ADDON_TYPE_ADDITIONAL_IP",
			"compatiblePlanCodes": []string{}, "addonPrices": []any{addonPrice("2.50")}},
	}
)

func hourly(amount string) map[string]any {
	return map[string]any{"id": "price-" + amount, "pricingModel": "PRICING_MODEL_USAGE_BASED", "chargeTiming": "CHARGE_TIMING_IN_ARREARS",
		"currency": "EUR", "unitAmount": amount, "unitName": "hour", "billingPeriod": "BILLING_PERIOD_HOUR", "billingInterval": 1, "minimumCharge": "0.00"}
}

func monthly(amount string) map[string]any {
	return map[string]any{"id": "price-" + amount, "pricingModel": "PRICING_MODEL_FIXED", "chargeTiming": "CHARGE_TIMING_IN_ADVANCE",
		"currency": "EUR", "unitAmount": amount, "unitName": "month", "billingPeriod": "BILLING_PERIOD_MONTH", "billingInterval": 1, "minimumCharge": "0.00"}
}

func addonPrice(amount string) map[string]any {
	return map[string]any{"id": "addon-price-" + amount, "kind": "ADDON_PRICE_KIND_SUBSCRIPTION", "currency": "EUR", "unitAmount": amount,
		"unitName": "month", "billingPeriod": "BILLING_PERIOD_MONTH", "pricingModel": "PRICING_MODEL_FIXED"}
}

func findPlan(id string) *fakePlan {
	for i := range fakePlans {
		if fakePlans[i].id == id {
			return &fakePlans[i]
		}
	}
	return nil
}

func findProvider(id string) *fakeProvider {
	for i := range fakeProviders {
		if fakeProviders[i].id == id {
			return &fakeProviders[i]
		}
	}
	return nil
}

func (r fakeRegion) view() map[string]any {
	return map[string]any{"regionId": r.id, "country": r.country, "city": r.city, "latitude": 0, "longitude": 0}
}

func (p *fakeProvider) info() map[string]any {
	return map[string]any{"organizationId": p.id, "organizationName": p.name, "description": p.name + " runs servers in Europe.",
		"rating": p.rating, "totalReviews": p.reviews, "planPrices": []any{}, "planRegions": []any{}, "planId": "",
		"hasCapacityInFilteredRegion": true, "providerLogoUrl": "", "ipv6AvailableInFilteredRegion": p.ipv6, "ddosProvider": "",
		"platformDriver": p.driver, "capabilities": p.caps}
}

func (p *fakeProvider) details() map[string]any {
	plans := []any{}
	regions := map[string]bool{}
	for _, pl := range fakePlans {
		if pl.provider != p.id {
			continue
		}
		regions[pl.region.city] = true
		plans = append(plans, map[string]any{"planId": pl.id, "planCode": pl.code, "planName": pl.name, "planDescription": "",
			"billingType": pl.billing, "computeType": "COMPUTE_TYPE_GENERAL_COMPUTE", "computeCategory": "COMPUTE_CATEGORY_SHARED_COMPUTE",
			"regions": []any{pl.region.view()}, "planPrices": []any{pl.price}, "assetType": pl.asset})
	}
	// The real API returns plans in no particular order.
	for i, j := 0, len(plans)-1; i < j; i, j = i+1, j-1 {
		plans[i], plans[j] = plans[j], plans[i]
	}
	cities := []string{}
	for c := range regions {
		cities = append(cities, c)
	}
	sort.Strings(cities)
	return map[string]any{"provider": p.info(), "website": "https://" + p.slug + ".example.com", "availableRegions": cities,
		"ddosSolution": "", "avgTicketResponseSeconds": "3600", "availablePlans": plans, "hasIpv6": p.ipv6}
}

func init() {
	register(func(s *Server, w http.ResponseWriter, r *http.Request, p []string) bool {
		if r.Method != http.MethodGet {
			return false
		}
		q := r.URL.Query()
		switch {
		case len(p) == 2 && p[1] == "instantapps":
			s.listInstantApps(w, r)
		case len(p) == 4 && p[1] == "marketplace" && p[2] == "providers":
			if pr := findProvider(p[3]); pr != nil {
				writeJSON(w, 200, map[string]any{"provider": pr.details()})
				return true
			}
			notFound(w, "Provider")
		case len(p) == 3 && p[1] == "marketplace" && p[2] == "providers":
			listProviders(w, r)
		case len(p) == 3 && p[1] == "marketplace" && p[2] == "provider-directory":
			if q.Get("pageSize") == "" || q.Get("pageSize") == "0" {
				apiError(w, 400, 3, "page_size: value is required")
				return true
			}
			list := []any{}
			for i := len(fakeProviders) - 1; i >= 0; i-- {
				pr := &fakeProviders[i]
				if search := strings.ToLower(q.Get("search")); search != "" && !strings.Contains(strings.ToLower(pr.name), search) {
					continue
				}
				list = append(list, pr.info())
			}
			writeJSON(w, 200, map[string]any{"providers": list, "totalCount": len(list)})
		case len(p) == 3 && p[1] == "marketplace" && p[2] == "plans":
			listPlanCodes(w, r)
		case len(p) == 4 && p[1] == "marketplace" && p[2] == "plans":
			pl := findPlan(p[3])
			if pl == nil {
				notFound(w, "Plan")
				return true
			}
			pr := findProvider(pl.provider)
			writeJSON(w, 200, map[string]any{"planId": pl.id, "planCode": pl.code, "regionId": pl.region.id, "regionCity": pl.region.city,
				"regionCountry": pl.region.country, "organizationId": pr.id, "organizationName": pr.name, "providerLogoUrl": "",
				"billingType": pl.billing, "vcpus": pl.vcpus, "ramGb": pl.ramGB, "diskGb": pl.diskGB, "planPrices": []any{pl.price},
				"planRegions": []any{pl.region.view()}, "rating": pr.rating, "totalReviews": pr.reviews})
		case len(p) == 3 && p[1] == "marketplace" && p[2] == "addons":
			s.listAddons(w, r)
		case len(p) == 3 && p[1] == "public" && p[2] == "storefronts":
			list := []any{}
			for _, pr := range fakeProviders {
				list = append(list, storefront(&pr))
			}
			writeJSON(w, 200, map[string]any{"storefronts": list, "totalCount": len(list)})
		case len(p) == 4 && p[1] == "public" && p[2] == "storefronts":
			for _, pr := range fakeProviders {
				if pr.slug == p[3] {
					writeJSON(w, 200, map[string]any{"storefront": storefront(&pr)})
					return true
				}
			}
			notFound(w, "Storefront")
		default:
			return false
		}
		return true
	})
}

func storefront(p *fakeProvider) map[string]any {
	return map[string]any{"slug": p.slug, "providerId": p.id, "organizationName": p.name, "tagline": "Fast servers in Europe",
		"aboutHtml": "<p>About us.</p>", "coverImageUrl": "", "logoImageUrl": "", "highlightKeys": []string{"ddos_protection", "nvme"},
		"highlightCustom": "", "highlightCustomLines": []string{"24/7 support"}, "featuredPlanIds": []string{},
		"faqs": []any{map[string]any{"question": "Where are you?", "answer": "Frankfurt."}}, "catalog": p.details(),
		"reviews": []any{}, "socialUrls": []string{"https://social.example.com/" + p.slug}, "approvedAt": now()}
}

// listProviders mimics ListProviders: plan code, country and city are
// required, and without a billing type each provider shows one arbitrary plan.
func listProviders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("pageSize") == "" || q.Get("pageSize") == "0" {
		apiError(w, 400, 3, "page_size: value is required")
		return
	}
	code, country, city := q.Get("filterPlanCode"), q.Get("filterCountry"), q.Get("filterCity")
	if code == "" || country == "" || city == "" {
		apiError(w, 400, 3, "filter_plan_code, filter_country and filter_city are required")
		return
	}
	billing := q.Get("filterBillingType")
	list := []any{}
	for _, pr := range fakeProviders {
		if q.Get("filterIpv6AvailableOnly") == "true" && !pr.ipv6 {
			continue
		}
		for _, pl := range fakePlans {
			if pl.provider != pr.id || pl.code != code || !strings.EqualFold(pl.region.country, country) || !strings.EqualFold(pl.region.city, city) {
				continue
			}
			if billing != "" && pl.billing != billing {
				continue
			}
			info := pr.info()
			info["planId"] = pl.id
			info["planPrices"] = []any{pl.price}
			info["planRegions"] = []any{pl.region.view()}
			list = append(list, info)
			break
		}
	}
	writeJSON(w, 200, map[string]any{"providers": list, "totalCount": len(list), "page": 0, "pageSize": -1})
}

func listPlanCodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	country, city := q.Get("filterCountry"), q.Get("filterCity")
	seen := map[string]bool{}
	list := []any{}
	for i := len(fakePlans) - 1; i >= 0; i-- {
		pl := fakePlans[i]
		// Like the real API, country and city only filter when both are set.
		if country != "" && city != "" && (!strings.EqualFold(pl.region.country, country) || !strings.EqualFold(pl.region.city, city)) {
			continue
		}
		if b := q.Get("filterBillingType"); b != "" && pl.billing != b {
			continue
		}
		if a := q.Get("filterAssetType"); a != "" && pl.asset != a {
			continue
		}
		key := pl.code + "|" + pl.billing
		if seen[key] {
			continue
		}
		seen[key] = true
		compute := "COMPUTE_TYPE_GENERAL_COMPUTE"
		if pl.asset == "ASSET_TYPE_GAMESERVER" {
			compute = "COMPUTE_TYPE_GAME_SERVER"
		}
		list = append(list, map[string]any{"planCode": pl.code, "billingType": pl.billing, "computeType": compute, "computeCategory": "COMPUTE_CATEGORY_SHARED_COMPUTE"})
	}
	writeJSON(w, 200, map[string]any{"plans": list, "totalCount": len(list)})
}

// listAddons mimics ListAvailableAddons: vmId overrides the provider filter,
// backup add-ons are hidden for VMs without backup support, and there is no
// plan-compatibility filtering.
func (s *Server) listAddons(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("pageSize") == "" || q.Get("pageSize") == "0" {
		apiError(w, 400, 3, "page_size: value is required")
		return
	}
	providerID := q.Get("filterProviderId")
	backupOK := true
	if vmID := q.Get("vmId"); vmID != "" {
		vm := computeState(s).vms[vmID]
		if vm == nil {
			notFound(w, "Virtual machine")
			return
		}
		providerID = vm.plan.provider
		backupOK = hasCap(vm, "on_demand_backup")
	}
	list := []any{}
	if providerID == ProviderID {
		for _, a := range fakeAddons {
			t := fmt.Sprint(a["addonType"])
			if f := q.Get("filterAddonType"); f != "" && f != t {
				continue
			}
			if t == "ADDON_TYPE_BACKUP" && !backupOK {
				continue
			}
			list = append(list, a)
		}
	}
	writeJSON(w, 200, map[string]any{"addons": list, "totalCount": len(list)})
}

func wordpressApp() map[string]any {
	return map[string]any{"id": WordPressAppID, "slug": "wordpress", "name": "WordPress", "description": "Blog and CMS.", "version": "6.8",
		"family": "wordpress", "distro": "DISTRO_UBUNTU_24_04", "iconUrl": "", "categories": []string{"cms"}, "minVcpus": 1, "minRamGb": 1,
		"minDiskGb": 20, "sortOrder": 1, "documentationUrl": "https://wordpress.org/documentation/",
		"metadata": map[string]any{"ports": []any{80, 443}, "variables": []any{
			map[string]any{"name": "site_title", "label": "Site title", "type": "string", "required": true},
			map[string]any{"name": "admin_email", "label": "Admin email", "type": "email", "required": false, "default": "admin@example.com"},
		}}, "status": "STATUS_ACTIVE"}
}

func dockerApp() map[string]any {
	return map[string]any{"id": "7a9e0000-0000-4000-8000-000000000002", "slug": "docker", "name": "Docker", "description": "Container engine.",
		"version": "28", "family": "docker", "distro": "DISTRO_DEBIAN_13", "iconUrl": "", "categories": []string{"developer"}, "minVcpus": 1,
		"minRamGb": 2, "minDiskGb": 20, "sortOrder": 2, "documentationUrl": "https://docs.example.com/docker", "metadata": map[string]any{},
		"status": "STATUS_ACTIVE"}
}

func (s *Server) listInstantApps(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list := []any{}
	for _, app := range []map[string]any{wordpressApp(), dockerApp()} {
		if query := strings.ToLower(q.Get("query")); query != "" &&
			!strings.Contains(strings.ToLower(fmt.Sprint(app["name"])), query) && !strings.Contains(fmt.Sprint(app["slug"]), query) {
			continue
		}
		if d := q.Get("distro"); d != "" && app["distro"] != d {
			continue
		}
		cats, _ := app["categories"].([]string)
		ok := true
		for _, want := range q["categories"] {
			found := false
			for _, c := range cats {
				found = found || c == want
			}
			ok = ok && found
		}
		if ok {
			list = append(list, app)
		}
	}
	writeJSON(w, 200, map[string]any{"instantApps": list, "totalCount": len(list)})
}
