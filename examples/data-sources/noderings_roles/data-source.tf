data "noderings_roles" "all" {}

output "custom_roles" {
  value = [for r in data.noderings_roles.all.roles : r.name if !r.builtin]
}
