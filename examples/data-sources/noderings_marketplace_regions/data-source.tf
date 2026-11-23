data "noderings_marketplace_regions" "vm" {
  asset_type = "virtual_machine"
}

output "cities" {
  value = [for r in data.noderings_marketplace_regions.vm.regions : "${r.city}, ${r.country}"]
}
