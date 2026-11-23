data "noderings_virtual_machine_snapshots" "web" {
  virtual_machine_id = noderings_virtual_machine.web.id
}

output "snapshots" {
  value = data.noderings_virtual_machine_snapshots.web.snapshots[*].name
}
