package framework

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// GoneIfNoID removes the resource from state and returns true when the prior
// state has no ID. Terraform never reads a resource without an ID, but other
// callers of the plugin protocol do: Crossplane's Upjet reads a resource before
// it has been created, with an empty ID, and expects "not found" back instead
// of an API call that fails validation.
func GoneIfNoID(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) bool {
	var id types.String
	if diags := req.State.GetAttribute(ctx, path.Root("id"), &id); diags.HasError() {
		return false
	}
	if id.IsNull() || id.IsUnknown() || id.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return true
	}
	return false
}
