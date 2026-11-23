resource "noderings_firewall_address_group" "office" {
  name  = "office-networks"
  cidrs = ["203.0.113.0/24", "2001:db8:10::/48"]
}
