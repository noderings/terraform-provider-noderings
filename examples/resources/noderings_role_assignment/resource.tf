resource "noderings_group" "ops" {
  name = "ops"
}

data "noderings_role" "operator" {
  name = "Operator"
}

# Everyone in the group gets the built-in Operator role.
resource "noderings_role_assignment" "ops" {
  group_id = noderings_group.ops.id
  role_id  = data.noderings_role.operator.id
}

data "noderings_users" "auditor" {
  emails = ["carol@example.com"]
}

data "noderings_role" "viewer" {
  name = "Viewer"
}

resource "noderings_role_assignment" "auditor" {
  user_id = data.noderings_users.auditor.ids[0]
  role_id = data.noderings_role.viewer.id
}
