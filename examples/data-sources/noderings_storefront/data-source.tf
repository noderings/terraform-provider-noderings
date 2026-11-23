data "noderings_storefront" "acme" {
  slug = "acme-hosting"
}

output "tagline" {
  value = data.noderings_storefront.acme.tagline
}
