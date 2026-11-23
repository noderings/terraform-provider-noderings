data "noderings_permissions" "dns" {
  services = ["DnsZoneService", "DnsRecordService", "DnssecService"]
}

data "noderings_permission" "list_ssh_keys" {
  name = "SSHKeyService::ListSSHKeys"
}

resource "noderings_role" "dns_admin" {
  name        = "dns-admin"
  description = "Manages DNS zones and records"
  permission_ids = setunion(
    data.noderings_permissions.dns.ids,
    [data.noderings_permission.list_ssh_keys.id],
  )
}
