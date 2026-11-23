resource "noderings_dns_zone_dnssec" "example" {
  zone_id = noderings_dns_zone.example.id
}

# Add this as a DS record at your registrar.
output "ds_record" {
  value = noderings_dns_zone_dnssec.example.ds
}
