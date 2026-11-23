data "noderings_marketplace_providers" "vm" {
  asset_type = "virtual_machine"
}

output "providers" {
  value = data.noderings_marketplace_providers.vm.providers[*].name
}
