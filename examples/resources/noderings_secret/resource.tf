variable "registry_password" {
  type      = string
  sensitive = true
}

resource "noderings_secret" "registry_password" {
  key   = "registry_password"
  value = var.registry_password
}
