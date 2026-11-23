data "noderings_virtual_machine_tags" "all" {}

output "tags" {
  value = data.noderings_virtual_machine_tags.all.tags
}
