data "noderings_distros" "all" {}

output "distros" {
  value = data.noderings_distros.all.distros[*].distro
}
