resource "noderings_virtual_machine_additional_ip" "web" {
  count              = 2
  virtual_machine_id = noderings_virtual_machine.web.id
}

output "extra_ips" {
  value = noderings_virtual_machine_additional_ip.web[*].ip_address
}
