// Package compute implements virtual machines and their add-ons, IPs,
// snapshots and firewall attachment.
package compute

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
)

// Resources lists this package's resources.
func Resources() []func() resource.Resource {
	return []func() resource.Resource{
		NewVirtualMachineResource, NewBackupResource, NewAdditionalIPResource, NewSnapshotResource, NewFirewallAttachmentResource,
	}
}

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewVirtualMachineDataSource, NewVirtualMachinesDataSource, NewTagsDataSource, NewBackupsDataSource, NewSnapshotsDataSource,
	}
}

// PollInterval is the first delay between status checks. Checks back off up
// to MaxPollInterval. Tests lower both.
var (
	PollInterval    = 2 * time.Second
	MaxPollInterval = 15 * time.Second
)

// poll calls check until it reports done or returns an error, backing off
// between calls, until ctx ends.
func poll(ctx context.Context, what string, check func() (done bool, err error)) error {
	delay := PollInterval
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("timed out waiting for %s; raise the matching timeouts value if this keeps happening", what)
		case <-t.C:
		}
		delay = min(delay*3/2+time.Millisecond, MaxPollInterval)
	}
}

// retryBusy runs f until it succeeds or fails with something other than the
// API's "operation in progress" error. VMs stay busy for a while after create
// and after every power or firewall change.
func retryBusy(ctx context.Context, f func() error) error {
	var last error
	err := poll(ctx, "the virtual machine to finish its current operation", func() (bool, error) {
		last = f()
		if client.IsBusy(last) {
			return false, nil
		}
		return true, last
	})
	if err != nil && last != nil && client.IsBusy(last) {
		return fmt.Errorf("%w (last error: %v)", err, last)
	}
	return err
}

// newUUID returns a random version 4 UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// --- timeouts block ---

var timeoutAttrTypes = map[string]attr.Type{"create": types.StringType, "update": types.StringType, "delete": types.StringType}

type durationValidator struct{}

func (durationValidator) Description(context.Context) string {
	return "must be a duration such as 30s, 20m or 1h"
}
func (v durationValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (v durationValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if d, err := time.ParseDuration(req.ConfigValue.ValueString()); err != nil || d <= 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid duration", "Use a positive duration such as 30s, 20m or 1h.")
	}
}

// notPrefix rejects strings with a reserved prefix.
type notPrefix struct{ prefix string }

func (v notPrefix) Description(context.Context) string {
	return "must not start with " + v.prefix + " (reserved by the provider)"
}
func (v notPrefix) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (v notPrefix) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() && strings.HasPrefix(req.ConfigValue.ValueString(), v.prefix) {
		resp.Diagnostics.AddAttributeError(req.Path, "Reserved tag", "Tags must not start with "+v.prefix+"; the provider uses that prefix internally while creating VMs.")
	}
}

// timeoutsBlock is the standard `timeouts { create, update, delete }` block.
func timeoutsBlock(ops ...string) schema.SingleNestedBlock {
	attrs := map[string]schema.Attribute{}
	for _, op := range ops {
		attrs[op] = schema.StringAttribute{Optional: true, Validators: []validator.String{durationValidator{}},
			MarkdownDescription: fmt.Sprintf("How long to wait for %s, for example `30m`.", op)}
	}
	return schema.SingleNestedBlock{MarkdownDescription: "How long to wait for operations to finish.", Attributes: attrs}
}

// timeout returns the configured timeout for op, or def.
func timeout(obj types.Object, op string, def time.Duration) time.Duration {
	if obj.IsNull() || obj.IsUnknown() {
		return def
	}
	v, ok := obj.Attributes()[op].(types.String)
	if !ok || v.IsNull() || v.IsUnknown() {
		return def
	}
	d, err := time.ParseDuration(v.ValueString())
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// --- private state ---

func getPrivate(ctx context.Context, p interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}, key string) string {
	b, _ := p.GetKey(ctx, key)
	var s string
	if len(b) > 0 {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func privateValue(s string) []byte {
	if s == "" {
		return nil
	}
	b, _ := json.Marshal(s)
	return b
}

// --- enum mapping ---

func trimEnum(v, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(v, prefix))
}

// distros are the operating systems the API accepts, in Terraform form.
var distros = []string{
	"ubuntu_22_04", "ubuntu_24_04", "ubuntu_26_04", "debian_12", "debian_13", "fedora_43", "fedora_44",
	"centos_9", "centos_10", "rocky_9", "rocky_10", "almalinux_9", "almalinux_10",
}

func distroEnum(d string) string { return "DISTRO_" + strings.ToUpper(d) }

// powerState maps the API's operationStatus to running or stopped. CREATED
// and REINSTALLING are transitional and map to "".
func powerState(op string) string {
	switch op {
	case "OPERATION_STATUS_STARTED", "OPERATION_STATUS_RESTARTED":
		return "running"
	case "OPERATION_STATUS_STOPPED":
		return "stopped"
	}
	return ""
}

func objectList(ctx context.Context, attrTypes map[string]attr.Type, items []map[string]attr.Value, diags *diag.Diagnostics) types.List {
	vals := make([]attr.Value, 0, len(items))
	for _, it := range items {
		o, d := types.ObjectValue(attrTypes, it)
		diags.Append(d...)
		vals = append(vals, o)
	}
	l, d := types.ListValue(types.ObjectType{AttrTypes: attrTypes}, vals)
	diags.Append(d...)
	_ = ctx
	return l
}
