data "noderings_virtual_machine_backups" "web" {
  virtual_machine_id = noderings_virtual_machine_backup.web.virtual_machine_id
}

output "latest_backup" {
  value = try(data.noderings_virtual_machine_backups.web.backups[0].created_at, null)
}
