# The cheapest hourly-billed offer of a plan code in Frankfurt from a
# provider whose platform supports the firewall and backups.
data "noderings_marketplace_offer" "web" {
  plan_code            = "SC-GC-2C-4G-80"
  country              = "Germany"
  city                 = "Frankfurt"
  billing_type         = "usage"
  require_ipv6         = true
  require_capabilities = ["guest_firewall", "on_demand_backup"]
}

resource "noderings_virtual_machine" "web" {
  hostname  = "web-1"
  plan_id   = data.noderings_marketplace_offer.web.plan_id
  region_id = data.noderings_marketplace_offer.web.region_id
  distro    = "ubuntu_24_04"
  password  = var.root_password
}

variable "root_password" {
  type      = string
  sensitive = true
}
