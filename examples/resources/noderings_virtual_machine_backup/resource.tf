data "noderings_marketplace_addon" "backup" {
  type               = "backup"
  virtual_machine_id = noderings_virtual_machine.web.id
}

resource "noderings_virtual_machine_backup" "web" {
  virtual_machine_id = noderings_virtual_machine.web.id
  addon_id           = data.noderings_marketplace_addon.backup.id
}
