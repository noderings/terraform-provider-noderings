data "noderings_instant_apps" "cms" {
  categories = ["cms"]
}

# The variables each app accepts in instant_app_variables.
output "apps" {
  value = { for a in data.noderings_instant_apps.cms.instant_apps : a.slug => a.variables[*].name }
}
