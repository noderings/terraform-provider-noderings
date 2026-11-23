data "noderings_virtual_machine" "db" {
  hostname = "db-1"
  tags     = ["prod"]
}

output "db_ip" {
  value = data.noderings_virtual_machine.db.ipv4
}
