data "noderings_credit_balance" "this" {}

check "enough_credit" {
  assert {
    condition     = data.noderings_credit_balance.this.available >= 20 && !data.noderings_credit_balance.this.frozen
    error_message = "Top up credits before adding more hourly virtual machines."
  }
}
