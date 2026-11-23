data "noderings_virtual_machines" "web" {
  tags           = ["web"]
  power_state    = "running"
  hostname_regex = "^web-"
}

output "web_ips" {
  value = data.noderings_virtual_machines.web.virtual_machines[*].ipv4
}
