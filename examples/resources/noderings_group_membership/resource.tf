resource "noderings_group" "ops" {
  name = "ops"
}

# Users join the organization through invites; look them up by email.
data "noderings_users" "ops" {
  emails = ["alice@example.com", "bob@example.com"]
}

resource "noderings_group_membership" "ops" {
  group_id = noderings_group.ops.id
  user_ids = data.noderings_users.ops.ids
  # Service accounts are managed only when this is set.
  service_account_ids = ["00000000-0000-0000-0000-000000000000"]
}
