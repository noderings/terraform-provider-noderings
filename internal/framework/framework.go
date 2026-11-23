// Package framework holds helpers shared by the provider's resources and data sources.
package framework

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
)

// ClientFromResource extracts the API client from provider data.
func ClientFromResource(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *client.Client {
	return clientFrom(req.ProviderData, &resp.Diagnostics)
}

// ClientFromDataSource extracts the API client from provider data.
func ClientFromDataSource(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *client.Client {
	return clientFrom(req.ProviderData, &resp.Diagnostics)
}

func clientFrom(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T. Please report this issue to the provider developers.", data))
		return nil
	}
	return c
}

// unsafeChars are rewritten by the API's input sanitiser, which would make the
// stored value differ from configuration.
var unsafeChars = regexp.MustCompile(`^[^<>&"']*$`)

// SafeString rejects characters the API rewrites.
func SafeString() validator.String {
	return stringvalidator.RegexMatches(unsafeChars, `must not contain <, >, &, " or ' (the API rewrites them)`)
}

// UUID validates a UUID string.
func UUID() validator.String {
	return stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`), "must be a UUID")
}

// Time formats an optional API timestamp.
func Time(t *time.Time) types.String {
	if t == nil || t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}

// Strings converts a string slice to a Terraform list, empty instead of null.
func Strings(ctx context.Context, in []string, diags *diag.Diagnostics) types.List {
	if in == nil {
		in = []string{}
	}
	l, d := types.ListValueFrom(ctx, types.StringType, in)
	diags.Append(d...)
	return l
}

// TrimEnum removes an API enum prefix and lower-cases the rest, so
// "DNS_ZONE_STATE_ACTIVE" becomes "active".
func TrimEnum(v, prefix string) string {
	if len(v) >= len(prefix) && v[:len(prefix)] == prefix {
		v = v[len(prefix):]
	}
	out := []byte(v)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 'a' - 'A'
		}
	}
	return string(out)
}
