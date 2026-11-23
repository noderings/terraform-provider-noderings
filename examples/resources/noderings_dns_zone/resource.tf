resource "noderings_dns_zone" "example" {
  name                = "example.com"
  deletion_protection = true
}

# Set these nameservers at your registrar.
output "nameservers" {
  value = noderings_dns_zone.example.nameservers
}
