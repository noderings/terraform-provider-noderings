// Package gameserver implements game servers and their allocations,
// databases, schedules and backups.
package gameserver

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

// Resources lists this package's resources.
func Resources() []func() resource.Resource {
	return []func() resource.Resource{
		NewServerResource, NewAllocationResource, NewDatabaseResource, NewScheduleResource, NewBackupResource,
	}
}

// DataSources lists this package's data sources.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewServerDataSource, NewServersDataSource, NewGamesDataSource, NewStartupDataSource, NewResourcesDataSource,
	}
}

// PollInterval is how often the provider checks on asynchronous work. Tests lower it.
var PollInterval = 10 * time.Second

var errWaitTimeout = errors.New("timed out")

// waitFor calls check every PollInterval until it reports done, fails, or the
// timeout passes (errWaitTimeout).
func waitFor(ctx context.Context, timeout time.Duration, check func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		done, err := check()
		if err != nil {
			if ctx.Err() != nil {
				return errWaitTimeout
			}
			return err
		}
		if done {
			return nil
		}
		t := time.NewTimer(PollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return errWaitTimeout
		case <-t.C:
		}
	}
}

// --- timeouts block ---

var timeoutsAttrTypes = map[string]attr.Type{"create": types.StringType, "update": types.StringType, "delete": types.StringType}

type durationValidator struct{}

func (durationValidator) Description(context.Context) string {
	return "must be a duration such as 30m or 1h"
}
func (v durationValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (durationValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if d, err := time.ParseDuration(req.ConfigValue.ValueString()); err != nil || d <= 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid duration", fmt.Sprintf("%q is not a positive duration such as 30m or 1h.", req.ConfigValue.ValueString()))
	}
}

// timeoutsBlock is a `timeouts` block with the given operations, written like
// the standard Terraform timeouts block.
func timeoutsBlock(defaults map[string]string) schema.Block {
	attrs := map[string]schema.Attribute{}
	for _, op := range []string{"create", "update", "delete"} {
		desc := "Not used."
		if def, ok := defaults[op]; ok {
			desc = fmt.Sprintf("How long to wait for %s, as a duration such as `30m`. Defaults to `%s`.", op, def)
		}
		attrs[op] = schema.StringAttribute{Optional: true, MarkdownDescription: desc, Validators: []validator.String{durationValidator{}}}
	}
	return schema.SingleNestedBlock{MarkdownDescription: "Timeouts for long-running operations.", Attributes: attrs}
}

// timeout returns an operation's configured timeout or the default.
func timeout(t types.Object, op string, def time.Duration) time.Duration {
	if t.IsNull() || t.IsUnknown() {
		return def
	}
	v, ok := t.Attributes()[op].(types.String)
	if !ok || v.IsNull() || v.IsUnknown() {
		return def
	}
	if d, err := time.ParseDuration(v.ValueString()); err == nil && d > 0 {
		return d
	}
	return def
}

func nullTimeouts() types.Object { return types.ObjectNull(timeoutsAttrTypes) }

// --- per-refresh list cache ---

// Sub-objects have no Get in the API, only List, and every list call is slow.
// Reads of many sub-resources on one server share one list call: results are
// cached per client (one Terraform run), server and kind for a short time, and
// dropped on any change to that kind.
type cacheKey struct {
	c            *client.Client
	server, kind string
}

type cacheEntry struct {
	ready chan struct{}
	val   any
	err   error
	at    time.Time
}

var (
	cacheMu  sync.Mutex
	cacheMap = map[cacheKey]*cacheEntry{}
)

const cacheTTL = 30 * time.Second

func cached[T any](ctx context.Context, c *client.Client, server, kind string, fetch func(context.Context) ([]T, error)) ([]T, error) {
	key := cacheKey{c, server, kind}
	cacheMu.Lock()
	for k, e := range cacheMap {
		if !e.at.IsZero() && time.Since(e.at) > cacheTTL {
			delete(cacheMap, k)
		}
	}
	e, ok := cacheMap[key]
	if !ok {
		e = &cacheEntry{ready: make(chan struct{})}
		cacheMap[key] = e
		cacheMu.Unlock()
		e.val, e.err = fetch(ctx)
		cacheMu.Lock()
		e.at = time.Now()
		if e.err != nil && cacheMap[key] == e {
			delete(cacheMap, key) // never cache errors
		}
		cacheMu.Unlock()
		close(e.ready)
	} else {
		cacheMu.Unlock()
		select {
		case <-e.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if e.err != nil {
		return nil, e.err
	}
	v, _ := e.val.([]T)
	return v, nil
}

func invalidate(c *client.Client, server, kind string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	delete(cacheMap, cacheKey{c, server, kind})
}

func listAllocations(ctx context.Context, c *client.Client, server string) ([]client.GameServerAllocation, error) {
	return cached(ctx, c, server, "allocations", func(ctx context.Context) ([]client.GameServerAllocation, error) {
		return c.ListGameServerAllocations(ctx, server)
	})
}

func listDatabases(ctx context.Context, c *client.Client, server string) ([]client.GameServerDatabase, error) {
	return cached(ctx, c, server, "databases", func(ctx context.Context) ([]client.GameServerDatabase, error) {
		return c.ListGameServerDatabases(ctx, server)
	})
}

func listSchedules(ctx context.Context, c *client.Client, server string) ([]client.GameServerSchedule, error) {
	return cached(ctx, c, server, "schedules", func(ctx context.Context) ([]client.GameServerSchedule, error) {
		return c.ListGameServerSchedules(ctx, server)
	})
}

func listBackups(ctx context.Context, c *client.Client, server string) ([]client.GameServerBackup, error) {
	return cached(ctx, c, server, "backups", func(ctx context.Context) ([]client.GameServerBackup, error) {
		return c.ListGameServerBackups(ctx, server)
	})
}

// --- sub-object helpers ---

// parentState says why a sub-object list failed.
type parentState int

const (
	parentReady    parentState = iota // the list worked
	parentGone                        // the game server no longer exists
	parentNotReady                    // the game server is not READY; sub-objects cannot be read
)

// listOrExplain runs a sub-object list. A FailedPrecondition is ambiguous (the
// API uses it both for "server not ready" and for missing objects), so the
// server is read to tell the cases apart.
func listOrExplain[T any](ctx context.Context, c *client.Client, server string, list func(context.Context, *client.Client, string) ([]T, error)) ([]T, parentState, string, error) {
	items, err := list(ctx, c, server)
	if err == nil {
		return items, parentReady, "", nil
	}
	if client.IsNotFound(err) {
		return nil, parentGone, "", nil
	}
	if !client.IsFailedPrecondition(err) {
		return nil, parentReady, "", err
	}
	gs, gerr := c.GetGameServer(ctx, server)
	if client.IsNotFound(gerr) {
		return nil, parentGone, "", nil
	}
	if gerr != nil {
		return nil, parentReady, "", err
	}
	if gs.Phase != client.GameServerPhaseReady {
		return nil, parentNotReady, phase(gs.Phase), nil
	}
	return nil, parentReady, "", err
}

// explainNotReady improves a FailedPrecondition from a sub-object change by
// checking whether the server is ready.
func explainNotReady(ctx context.Context, c *client.Client, server string, err error) string {
	msg := err.Error()
	if !client.IsFailedPrecondition(err) {
		return msg
	}
	gs, gerr := c.GetGameServer(ctx, server)
	if gerr == nil && gs.Phase != client.GameServerPhaseReady {
		return fmt.Sprintf("Game server %s is %s, not ready. Allocations, databases, schedules and backups can only be changed while the server is ready. "+
			"Wait for the install, reinstall or game change to finish (or fix the server if it failed) and apply again.\n\nAPI error: %s", server, phase(gs.Phase), msg)
	}
	return msg
}

func notReadyWarning(diags *diag.Diagnostics, what, server, ph string) {
	diags.AddWarning("Game server is not ready",
		fmt.Sprintf("Game server %s is %s, so its %s could not be read. Terraform keeps the last known values until the server is ready again.", server, ph, what))
}

// phase turns GAME_SERVER_PHASE_READY into "ready".
func phase(p string) string {
	v := framework.TrimEnum(p, "GAME_SERVER_PHASE_")
	if v == "" || v == "unspecified" {
		return "pending"
	}
	return v
}

// splitID parses "<server_id>/<object_id>".
func splitID(id, what string) (string, string, error) {
	server, obj, ok := strings.Cut(id, "/")
	if !ok || server == "" || obj == "" || strings.Contains(obj, "/") {
		return "", "", fmt.Errorf("expected an import ID of the form <game_server_id>/<%s_id>, got %q", what, id)
	}
	return server, obj, nil
}

// newUUID returns a random version 4 UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func int32Ptr(v *int32) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*v))
}

func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// deleteSub deletes a sub-object. The API reports a missing sub-object as
// FailedPrecondition, so such an error counts as success only if a fresh list
// confirms the object is gone.
func deleteSub[T any](ctx context.Context, c *client.Client, server, kind string, del func() error,
	list func(context.Context, *client.Client, string) ([]T, error), match func(T) bool) error {
	err := del()
	invalidate(c, server, kind)
	if err == nil || client.IsNotFound(err) {
		return nil
	}
	if !client.IsFailedPrecondition(err) {
		return err
	}
	items, st, ph, lerr := listOrExplain(ctx, c, server, list)
	switch {
	case st == parentGone:
		return nil
	case st == parentNotReady:
		return fmt.Errorf("game server %s is %s, not ready, so its %s cannot be deleted now. Wait until it is ready and destroy again.\n\nAPI error: %w", server, ph, kind, err)
	case lerr != nil:
		return err
	}
	for _, it := range items {
		if match(it) {
			return err
		}
	}
	return nil
}

// readSub lists a server's sub-objects for a Read. ok is false when the
// caller should stop: the resource was removed, the prior state was kept, or
// an error was added.
func readSub[T any](ctx context.Context, c *client.Client, server, what string, list func(context.Context, *client.Client, string) ([]T, error),
	diags *diag.Diagnostics, remove func()) ([]T, bool) {
	items, st, ph, err := listOrExplain(ctx, c, server, list)
	switch {
	case err != nil:
		diags.AddError("Error reading game server "+what, err.Error())
		return nil, false
	case st == parentGone:
		remove()
		return nil, false
	case st == parentNotReady:
		notReadyWarning(diags, what, server, ph)
		return nil, false
	}
	return items, true
}

// subImport parses "<server_id>/<object_id>" into the id and game_server_id
// attributes and returns the object ID.
func subImport(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, what string) (string, bool) {
	server, obj, err := splitID(req.ID, what)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return "", false
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), server+"/"+obj)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("game_server_id"), server)...)
	return obj, !resp.Diagnostics.HasError()
}

// subResource holds what every sub-object resource shares.
type subResource struct{ client *client.Client }

func (r *subResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

func serverIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Required: true, MarkdownDescription: "ID of the game server. Changing it replaces the resource.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:    []validator.String{framework.UUID()},
	}
}

func idAttribute() schema.StringAttribute {
	return schema.StringAttribute{Computed: true, MarkdownDescription: "`<game_server_id>/<object ID>`, also the import ID.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}
