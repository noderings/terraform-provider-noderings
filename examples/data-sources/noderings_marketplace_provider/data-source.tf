data "noderings_marketplace_provider" "acme" {
  storefront_slug = "acme-hosting"
}

output "plan_codes" {
  value = distinct(data.noderings_marketplace_provider.acme.plans[*].plan_code)
}
