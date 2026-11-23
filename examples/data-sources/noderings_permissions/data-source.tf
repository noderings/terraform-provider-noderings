data "noderings_permissions" "dns" {
  services = ["DnsZoneService", "DnsRecordService"]
}

output "dns_permissions" {
  value = [for p in data.noderings_permissions.dns.permissions : p.name]
}
