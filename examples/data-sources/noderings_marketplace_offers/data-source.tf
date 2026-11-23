data "noderings_marketplace_offers" "all" {
  plan_code    = "SC-GC-2C-4G-80"
  country      = "Germany"
  city         = "Frankfurt"
  billing_type = "usage"
  sort         = "rating"
}

output "offers" {
  value = [for o in data.noderings_marketplace_offers.all.offers : "${o.provider_name}: ${o.prices[0].unit_amount} ${o.prices[0].currency}"]
}
