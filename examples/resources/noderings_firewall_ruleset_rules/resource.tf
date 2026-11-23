resource "noderings_firewall_address_group" "office" {
  name  = "office-networks"
  cidrs = ["203.0.113.0/24", "2001:db8:10::/48"]
}

resource "noderings_firewall_ruleset" "web" {
  name = "web"
}

resource "noderings_firewall_ruleset_rules" "web" {
  ruleset_id = noderings_firewall_ruleset.web.id

  # Evaluated top to bottom.
  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "443"
    source { cidrs = ["0.0.0.0/0"] }
  }

  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "443"
    source { cidrs = ["::/0"] }
  }

  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "22"
    source { address_group_ids = [noderings_firewall_address_group.office.id] }
  }

  # Default deny: traffic that matches no rule is otherwise accepted.
  rule {
    direction = "ingress"
    action    = "drop"
    protocol  = "any"
    source { cidrs = ["0.0.0.0/0"] }
  }

  rule {
    direction = "ingress"
    action    = "drop"
    protocol  = "any"
    source { cidrs = ["::/0"] }
  }
}
