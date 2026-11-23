// Package acctest runs provider tests.
//
// By default tests run real Terraform plans and applies against an in-memory
// fake of the NodeRings API (internal/fakeapi), so they need no account and
// no network. Set NODERINGS_ACC=1 with TF_ACC=1 and NODERINGS_TOKEN to run the
// same tests against the real API instead; tests that need to change the fake
// API's state directly are skipped then.
package acctest

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
	"github.com/noderings/terraform-provider-noderings/internal/provider"
)

// ProtoV6ProviderFactories serves the provider in-process.
var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"noderings": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// Live reports whether tests run against the real API.
func Live() bool { return os.Getenv("NODERINGS_ACC") == "1" && os.Getenv("TF_ACC") == "1" }

// Env is a test environment: the provider block and, for fake runs, the fake API.
type Env struct {
	// Fake is nil when running against the real API.
	Fake *fakeapi.Server
	// Provider is the provider block to prepend to test configurations.
	Provider string
}

// New starts a test environment.
func New(t *testing.T) *Env {
	t.Helper()
	if Live() {
		if os.Getenv("NODERINGS_TOKEN") == "" {
			t.Fatal("NODERINGS_ACC=1 needs NODERINGS_TOKEN")
		}
		return &Env{Provider: `provider "noderings" {}` + "\n"}
	}
	fake := fakeapi.New()
	t.Cleanup(fake.Close)
	return &Env{
		Fake: fake,
		Provider: fmt.Sprintf(`
provider "noderings" {
  endpoint            = %q
  token               = "test-token"
  requests_per_second = 0
  max_retries         = 3
}
`, fake.URL),
	}
}

// Config prepends the provider block.
func (e *Env) Config(body string) string { return e.Provider + body }

// RequireFake skips the test when running against the real API.
func (e *Env) RequireFake(t *testing.T) {
	t.Helper()
	if e.Fake == nil {
		t.Skip("needs the fake API")
	}
}

// Run runs a test case against the environment.
func (e *Env) Run(t *testing.T, tc resource.TestCase) {
	t.Helper()
	tc.ProtoV6ProviderFactories = ProtoV6ProviderFactories
	if e.Fake == nil {
		resource.Test(t, tc)
		return
	}
	resource.UnitTest(t, tc)
}

// RandomName returns a lower-case name for test resources.
func RandomName(prefix string) string {
	return prefix + "-" + acctest.RandString(8)
}

// TestDomain returns a domain for DNS tests. Live runs need NODERINGS_ACC_DOMAIN,
// a parent domain the account may create zones under.
func TestDomain(t *testing.T) string {
	t.Helper()
	label := "tf-acc-" + acctest.RandString(8)
	if !Live() {
		return label + ".example.com"
	}
	parent := os.Getenv("NODERINGS_ACC_DOMAIN")
	if parent == "" {
		t.Skip("set NODERINGS_ACC_DOMAIN to run DNS tests against the real API")
	}
	return label + "." + parent
}
