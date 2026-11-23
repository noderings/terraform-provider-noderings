data "noderings_entitlements" "this" {}

locals {
  exhausted = [
    for r in data.noderings_entitlements.this.rules : r.key
    if r.effect == "limit" && r.usage >= r.limit
  ]
}

check "quotas" {
  assert {
    condition     = length(local.exhausted) == 0
    error_message = "These quotas are used up: ${join(", ", local.exhausted)}. Upgrade the plan or free some resources first."
  }
}
