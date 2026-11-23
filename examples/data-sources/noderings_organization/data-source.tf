data "noderings_organization" "current" {}

output "organization" {
  value = data.noderings_organization.current.name
}
