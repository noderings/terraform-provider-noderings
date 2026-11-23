resource "noderings_virtual_machine_snapshot" "before_upgrade" {
  virtual_machine_id = noderings_virtual_machine.web.id
  description        = "Before the 2.0 upgrade"
}
