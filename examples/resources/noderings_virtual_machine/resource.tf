data "noderings_marketplace_offer" "web" {
  plan_code            = "SC-GC-2C-4G-80"
  country              = "Germany"
  city                 = "Frankfurt"
  billing_type         = "usage"
  require_capabilities = ["guest_firewall"]
}

data "noderings_ssh_key" "me" {
  name = "laptop"
}

resource "noderings_virtual_machine" "web" {
  hostname  = "web-1"
  plan_id   = data.noderings_marketplace_offer.web.plan_id
  region_id = data.noderings_marketplace_offer.web.region_id
  distro    = "ubuntu_24_04"

  ssh_key_ids = [data.noderings_ssh_key.me.id]
  tags        = ["web", "prod"]
  power_state = "running"

  deletion_protection = true

  timeouts {
    create = "20m"
  }
}

output "ip" {
  value = noderings_virtual_machine.web.ipv4
}
