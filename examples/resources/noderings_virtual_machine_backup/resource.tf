# If the provider sells several backup add-ons (daily, weekly, ...), pick one
# with code. The error message lists the codes when it is missing.
data "noderings_marketplace_addon" "backup" {
  type               = "backup"
  code               = "DAILY-BACKUP"
  virtual_machine_id = noderings_virtual_machine.web.id
}

resource "noderings_virtual_machine_backup" "web" {
  virtual_machine_id = noderings_virtual_machine.web.id
  addon_id           = data.noderings_marketplace_addon.backup.id
}
