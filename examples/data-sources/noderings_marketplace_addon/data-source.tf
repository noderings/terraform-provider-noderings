data "noderings_marketplace_addon" "backup" {
  type               = "backup"
  virtual_machine_id = noderings_virtual_machine.web.id
}

output "backup_price" {
  value = data.noderings_marketplace_addon.backup.prices
}
