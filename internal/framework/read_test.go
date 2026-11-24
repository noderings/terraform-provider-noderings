package framework

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestGoneIfNoID(t *testing.T) {
	ctx := context.Background()
	s := schema.Schema{Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true}}}
	typ := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String}}

	for name, tc := range map[string]struct {
		id   tftypes.Value
		gone bool
	}{
		"empty id":   {tftypes.NewValue(tftypes.String, ""), true},
		"null id":    {tftypes.NewValue(tftypes.String, nil), true},
		"id present": {tftypes.NewValue(tftypes.String, "abc"), false},
	} {
		t.Run(name, func(t *testing.T) {
			raw := tftypes.NewValue(typ, map[string]tftypes.Value{"id": tc.id})
			req := resource.ReadRequest{State: tfsdk.State{Schema: s, Raw: raw}}
			resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: raw}}
			if got := GoneIfNoID(ctx, req, resp); got != tc.gone {
				t.Fatalf("GoneIfNoID = %v, want %v", got, tc.gone)
			}
			if tc.gone != resp.State.Raw.IsNull() {
				t.Fatalf("state removed = %v, want %v", resp.State.Raw.IsNull(), tc.gone)
			}
		})
	}
}
