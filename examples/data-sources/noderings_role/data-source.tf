data "noderings_role" "viewer" {
  name = "Viewer"
}

output "viewer_role_id" {
  value = data.noderings_role.viewer.id
}
