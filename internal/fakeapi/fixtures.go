package fakeapi

// Marketplace fixtures shared by the compute and game-server fakes, so tests
// can resolve an offer and then create a VM or game server from it.
const (
	ProviderID   = "5a1e0000-0000-4000-8000-000000000001"
	ProviderName = "Example Hosting"
	// StorefrontSlug is the provider's public storefront.
	StorefrontSlug = "example-hosting"

	RegionFrankfurtID = "6b2f0000-0000-4000-8000-000000000001"
	RegionAmsterdamID = "6b2f0000-0000-4000-8000-000000000002"

	// VMPlanCode is sold as usage (hourly) in Frankfurt and Amsterdam, and as a
	// subscription (monthly) in Frankfurt. Each plan_id covers one region.
	VMPlanCode            = "SC-GC-2C-4G-80"
	VMPlanUsageFRAID      = "7c3a0000-0000-4000-8000-000000000001"
	VMPlanUsageAMSID      = "7c3a0000-0000-4000-8000-000000000002"
	VMPlanSubscriptionFRA = "7c3a0000-0000-4000-8000-000000000003"

	// GamePlanCode is a usage-billed game-server plan in Frankfurt.
	GamePlanCode = "SC-GS-4C-8G-50"
	GamePlanID   = "7c3a0000-0000-4000-8000-000000000010"
	// GameTemplateID is the only orderable game on GamePlanID.
	GameTemplateID   = "8d4b0000-0000-4000-8000-000000000001"
	GameTemplateName = "Minecraft · Paper"

	// Add-ons available for VMs on this provider.
	BackupAddonID       = "9e5c0000-0000-4000-8000-000000000001"
	AdditionalIPAddonID = "9e5c0000-0000-4000-8000-000000000002"
)
