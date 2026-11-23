data "noderings_users" "all" {}

data "noderings_users" "admins" {
  emails = ["alice@example.com", "bob@example.com"]
}

output "admin_ids" {
  value = data.noderings_users.admins.ids
}

output "alice" {
  value = data.noderings_users.all.ids_by_email["alice@example.com"]
}
