# Rulesets attached to at least one virtual machine.
data "noderings_firewall_rulesets" "attached" {
  attached = true
}

output "attached_rulesets" {
  value = [for rs in data.noderings_firewall_rulesets.attached.rulesets : rs.name]
}
