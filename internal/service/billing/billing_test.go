package billing_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

func TestCreditBalanceDataSource(t *testing.T) {
	env := acctest.New(t)
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttrSet("data.noderings_credit_balance.this", "available"),
		resource.TestCheckResourceAttrSet("data.noderings_credit_balance.this", "currency"),
		resource.TestCheckResourceAttrSet("data.noderings_credit_balance.this", "frozen"),
	}
	if env.Fake != nil {
		checks = append(checks,
			resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "balance", "125.5"),
			resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "available", "120"),
			resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "reserved", "5.5"),
			resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "promotional", "10"),
			resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "currency", "USD"),
			resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "frozen", "false"),
		)
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(`data "noderings_credit_balance" "this" {}`),
			Check:  resource.ComposeAggregateTestCheckFunc(checks...),
		}},
	})
}

// Configurations can compare the available credit with a threshold.
func TestCreditBalanceGuard(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			PreConfig: func() { env.Fake.SetCreditAvailable("3.25", true) },
			Config: env.Config(`
data "noderings_credit_balance" "this" {}

output "enough" {
  value = data.noderings_credit_balance.this.available >= 20
}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "available", "3.25"),
				resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "frozen", "true"),
				resource.TestCheckResourceAttr("data.noderings_credit_balance.this", "frozen_reason", "Payment overdue"),
				resource.TestCheckOutput("enough", "false"),
			),
		}},
	})
}

func TestSubscriptionDataSource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	var usageVM = "5e6f0000-0000-4000-8000-000000000002"
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			PreConfig: func() {
				// A canceled subscription for the same VM is ignored.
				env.Fake.AddSubscription(fakeapi.SubscriptionOptions{VMID: usageVM, Hostname: "old", Canceled: true})
				env.Fake.AddSubscription(fakeapi.SubscriptionOptions{VMID: usageVM, Hostname: "batch-1", Usage: true})
			},
			Config: env.Config(fmt.Sprintf(`
data "noderings_subscription" "monthly" {
  virtual_machine_id = %q
}
data "noderings_subscription" "hourly" {
  virtual_machine_id = %q
}
`, fakeapi.SubscribedVMID, usageVM)),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.noderings_subscription.monthly", "id"),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "status", "active"),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "billing_type", "subscription"),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "billing_period", "month"),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "recurring_amount", "12"),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "plan_code", fakeapi.VMPlanCode),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "plan_id", fakeapi.VMPlanSubscriptionFRA),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "current_period_end", "2026-12-01T00:00:00Z"),
				resource.TestCheckResourceAttr("data.noderings_subscription.monthly", "cancel_at_period_end", "false"),
				resource.TestCheckResourceAttr("data.noderings_subscription.hourly", "hostname", "batch-1"),
				resource.TestCheckResourceAttr("data.noderings_subscription.hourly", "billing_type", "usage"),
				resource.TestCheckResourceAttr("data.noderings_subscription.hourly", "billing_period", "hour"),
				resource.TestCheckResourceAttr("data.noderings_subscription.hourly", "has_usage_charges", "true"),
			),
		}},
	})
}

// The subscription list is paged; a subscription past the first page is still found.
func TestSubscriptionDataSourcePaging(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	last := ""
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			PreConfig: func() {
				for i := range 120 {
					last = fmt.Sprintf("5e6f0000-0000-4000-8000-%012d", 1000+i)
					env.Fake.AddSubscription(fakeapi.SubscriptionOptions{VMID: last, Hostname: fmt.Sprintf("vm-%d", i)})
				}
			},
			Config: env.Config(`
data "noderings_subscription" "last" {
  virtual_machine_id = "5e6f0000-0000-4000-8000-000000001119"
}
`),
			Check: resource.TestCheckResourceAttr("data.noderings_subscription.last", "hostname", "vm-119"),
		}},
	})
}

func TestSubscriptionDataSourceErrors(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.Config(`data "noderings_subscription" "x" { virtual_machine_id = "00000000-0000-4000-8000-00000000dead" }`),
				ExpectError: regexp.MustCompile(`No active subscription bills virtual machine`),
			},
			{
				Config:      env.Config(`data "noderings_subscription" "x" { virtual_machine_id = "web-1" }`),
				ExpectError: regexp.MustCompile(`must be a\s+UUID`),
			},
		},
	})
}

func TestEntitlementsDataSource(t *testing.T) {
	env := acctest.New(t)
	checks := []resource.TestCheckFunc{resource.TestCheckResourceAttrSet("data.noderings_entitlements.this", "rules.#")}
	if env.Fake != nil {
		checks = append(checks,
			resource.TestCheckResourceAttr("data.noderings_entitlements.this", "rules.#", "4"),
			resource.TestCheckResourceAttr("data.noderings_entitlements.this", "limits.client.max_dns_zones", "40"),
			resource.TestCheckResourceAttr("data.noderings_entitlements.this", "usage.client.max_dns_zones", "0"),
			resource.TestCheckResourceAttr("data.noderings_entitlements.this", "limits.client.max_virtual_machines", "10"),
			resource.TestCheckResourceAttr("data.noderings_entitlements.this", "features.client.backups", "true"),
			resource.TestCheckNoResourceAttr("data.noderings_entitlements.this", "limits.client.backups"),
			resource.TestCheckTypeSetElemNestedAttrs("data.noderings_entitlements.this", "rules.*", map[string]string{
				"key": "client.max_ssh_keys", "effect": "limit", "limit": "50", "usage": "0",
			}),
		)
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			PreConfig: func() {
				if env.Fake != nil {
					env.Fake.SetEntitlementLimit("client.max_dns_zones", 40)
				}
			},
			Config: env.Config(`data "noderings_entitlements" "this" {}`),
			Check:  resource.ComposeAggregateTestCheckFunc(checks...),
		}},
	})
}
