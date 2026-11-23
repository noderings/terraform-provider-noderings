data "noderings_permission" "create_zone" {
  name = "DnsZoneService::CreateDnsZone"
}

data "noderings_permission" "list_zones" {
  service = "DnsZoneService"
  method  = "ListDnsZones"
}

resource "noderings_role" "zone_creator" {
  name = "zone-creator"
  permission_ids = [
    data.noderings_permission.create_zone.id,
    data.noderings_permission.list_zones.id,
  ]
}
