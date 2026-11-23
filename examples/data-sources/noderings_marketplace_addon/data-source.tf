# When the provider sells several backup add-ons, choose one with code.
data "noderings_marketplace_addon" "backup" {
  type               = "backup"
  code               = "DAILY-BACKUP"
  virtual_machine_id = noderings_virtual_machine.web.id
}

output "backup_price" {
  value = data.noderings_marketplace_addon.backup.prices
}
