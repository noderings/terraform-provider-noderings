// Package shim exposes the NodeRings Terraform provider to other Go programs,
// such as the Pulumi bridge in github.com/noderings/pulumi-noderings.
//
// Everything else lives under internal/ and is not a public API.
package shim

import (
	"github.com/hashicorp/terraform-plugin-framework/provider"

	internal "github.com/noderings/terraform-provider-noderings/internal/provider"
)

// NewProvider returns the Terraform provider, reporting the given version.
func NewProvider(version string) provider.Provider {
	return internal.New(version)()
}
