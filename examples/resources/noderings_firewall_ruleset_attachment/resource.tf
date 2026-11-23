resource "noderings_firewall_ruleset_attachment" "web" {
  virtual_machine_id = noderings_virtual_machine.web.id
  ruleset_id         = noderings_firewall_ruleset.web.id
}
