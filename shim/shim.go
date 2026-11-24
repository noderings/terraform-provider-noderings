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

// NewProviderWithUserAgent returns the provider with a fixed User-Agent for
// every API request, for example "pulumi-noderings/0.1.1".
func NewProviderWithUserAgent(version, userAgent string) provider.Provider {
	return internal.NewWithUserAgent(version, userAgent)()
}
