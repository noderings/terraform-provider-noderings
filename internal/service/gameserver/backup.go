package gameserver

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

const defaultBackupTimeout = 30 * time.Minute

var (
	_ resource.Resource                = &backupResource{}
	_ resource.ResourceWithImportState = &backupResource{}
)

// NewBackupResource returns the noderings_game_server_backup resource.
func NewBackupResource() resource.Resource { return &backupResource{} }

type backupResource struct{ subResource }

type backupModel struct {
	ID           types.String `tfsdk:"id"`
	GameServerID types.String `tfsdk:"game_server_id"`
	BackupID     types.String `tfsdk:"backup_id"`
	Name         types.String `tfsdk:"name"`
	Locked       types.Bool   `tfsdk:"locked"`
	Bytes        types.Int64  `tfsdk:"bytes"`
	CompletedAt  types.String `tfsdk:"completed_at"`
	Successful   types.Bool   `tfsdk:"successful"`
	Timeouts     types.Object `tfsdk:"timeouts"`
}

func (r *backupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server_backup"
}

func (r *backupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A backup of a game server's files. Terraform waits until the backup has finished. Backups count against the plan's `backup_limit`. " +
			"Changing the server's game deletes all its backups; Terraform then creates this one again. " +
			"Only the most recent backups (about 20) are listed by the API, so with more backups than that, older ones read as deleted. " +
			"Restoring a backup is not done through Terraform. The game server must be ready.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute(),
			"game_server_id": serverIDAttribute(),
			"backup_id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Backup ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Backup name. Changing it creates a new backup and deletes this one.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 191), framework.SafeString()},
			},
			"locked": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Lock the backup so it cannot be deleted, for example by a scheduled backup task making room. " +
					"Terraform unlocks it before destroying it. Defaults to `false`.",
			},
			"bytes":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Size in bytes.", PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"completed_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the backup finished.", PlanModifiers: keep},
			"successful": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the backup succeeded.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeoutsBlock(map[string]string{"create": "30m"}),
		},
	}
}

func (r *backupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan backupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := plan.GameServerID.ValueString()
	b, err := r.client.CreateGameServerBackup(ctx, server, plan.Name.ValueString())
	invalidate(r.client, server, "backups")
	if err != nil && client.IsGameServerDeadlineExceeded(err) {
		b = adoptBackup(ctx, r.client, server, plan.Name.ValueString())
		if b != nil {
			err = nil
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating game server backup", explainNotReady(ctx, r.client, server, err))
		return
	}
	// Save the backup now so a failed wait does not lose it.
	m := plan
	m.Locked = types.BoolValue(false)
	setBackup(&m, b)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)

	id := b.UUID
	to := timeout(plan.Timeouts, "create", defaultBackupTimeout)
	err = waitFor(ctx, to, func() (bool, error) {
		list, err := r.client.ListGameServerBackups(ctx, server)
		if err != nil {
			return false, err
		}
		for i := range list {
			if list[i].UUID == id {
				b = &list[i]
				return list[i].CompletedAt != "", nil
			}
		}
		return false, fmt.Errorf("backup %s disappeared while it was running", id)
	})
	invalidate(r.client, server, "backups")
	setBackup(&m, b)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
	switch {
	case err == errWaitTimeout:
		resp.Diagnostics.AddError("Backup did not finish", fmt.Sprintf("Backup %s was still running after %s. Raise timeouts.create to wait longer.", id, to))
		return
	case err != nil:
		resp.Diagnostics.AddError("Error waiting for the backup", err.Error())
		return
	case !b.IsSuccessful:
		resp.Diagnostics.AddError("Backup failed", fmt.Sprintf("Backup %s finished but did not succeed. Check the server's disk space and try again.", id))
		return
	}
	if plan.Locked.ValueBool() {
		if err := r.setLock(ctx, server, id, true); err != nil {
			resp.Diagnostics.AddError("Error locking the backup", err.Error())
			return
		}
		m.Locked = types.BoolValue(true)
	}
	if !r.refresh(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func adoptBackup(ctx context.Context, c *client.Client, server, name string) *client.GameServerBackup {
	list, err := c.ListGameServerBackups(ctx, server)
	if err != nil {
		return nil
	}
	var found *client.GameServerBackup
	for i := range list {
		if list[i].Name == name {
			if found != nil {
				return nil
			}
			found = &list[i]
		}
	}
	return found
}

// setLock locks or unlocks a backup. The API only toggles the lock, so the
// current state is read first.
func (r *backupResource) setLock(ctx context.Context, server, id string, want bool) error {
	get := func() (*client.GameServerBackup, error) {
		list, err := r.client.ListGameServerBackups(ctx, server)
		if err != nil {
			return nil, err
		}
		for i := range list {
			if list[i].UUID == id {
				return &list[i], nil
			}
		}
		return nil, fmt.Errorf("backup %s was not found", id)
	}
	b, err := get()
	if err != nil {
		return err
	}
	if b.IsLocked == want {
		return nil
	}
	err = r.client.ToggleGameServerBackupLock(ctx, server, id)
	invalidate(r.client, server, "backups")
	if err != nil {
		return fmt.Errorf("%s", explainNotReady(ctx, r.client, server, err))
	}
	if b, err = get(); err != nil {
		return err
	}
	if b.IsLocked != want {
		return fmt.Errorf("backup %s's lock changed at the same time from elsewhere; apply again", id)
	}
	return nil
}

func setBackup(m *backupModel, b *client.GameServerBackup) {
	m.BackupID = types.StringValue(b.UUID)
	m.ID = types.StringValue(m.GameServerID.ValueString() + "/" + b.UUID)
	m.Name = types.StringValue(b.Name)
	m.Locked = types.BoolValue(b.IsLocked)
	m.Bytes = types.Int64Value(int64(b.Bytes))
	m.CompletedAt = stringOrNull(b.CompletedAt)
	m.Successful = types.BoolValue(b.IsSuccessful)
}

func (r *backupResource) refresh(ctx context.Context, m *backupModel, diags *diag.Diagnostics) bool {
	list, err := listBackups(ctx, r.client, m.GameServerID.ValueString())
	if err != nil {
		diags.AddError("Error reading game server backups", err.Error())
		return false
	}
	for i := range list {
		if list[i].UUID == m.BackupID.ValueString() {
			setBackup(m, &list[i])
			return true
		}
	}
	diags.AddError("Backup disappeared", "Backup "+m.ID.ValueString()+" no longer exists.")
	return false
}

func (r *backupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state backupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, ok := readSub(ctx, r.client, state.GameServerID.ValueString(), "backups", listBackups, &resp.Diagnostics, func() { resp.State.RemoveResource(ctx) })
	if !ok {
		return
	}
	for i := range list {
		if list[i].UUID == state.BackupID.ValueString() {
			setBackup(&state, &list[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

// Update only changes the lock.
func (r *backupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state backupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server, id := state.GameServerID.ValueString(), state.BackupID.ValueString()
	if err := r.setLock(ctx, server, id, plan.Locked.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Error changing the backup lock", err.Error())
		return
	}
	state.Timeouts = plan.Timeouts
	if !r.refresh(ctx, &state, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *backupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state backupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server, id := state.GameServerID.ValueString(), state.BackupID.ValueString()
	invalidate(r.client, server, "backups")
	list, st, ph, err := listOrExplain(ctx, r.client, server, listBackups)
	switch {
	case err != nil:
		resp.Diagnostics.AddError("Error reading game server backups", err.Error())
		return
	case st == parentGone:
		return
	case st == parentNotReady:
		resp.Diagnostics.AddError("Game server is not ready",
			fmt.Sprintf("Game server %s is %s, so backup %s cannot be deleted now. Wait until the server is ready and destroy again.", server, ph, id))
		return
	}
	var b *client.GameServerBackup
	for i := range list {
		if list[i].UUID == id {
			b = &list[i]
		}
	}
	if b == nil {
		return
	}
	if b.IsLocked {
		// Locked backups cannot be deleted.
		if err := r.setLock(ctx, server, id, false); err != nil {
			resp.Diagnostics.AddError("Error unlocking the backup before deleting it", err.Error())
			return
		}
	}
	err = deleteSub(ctx, r.client, server, "backups",
		func() error { return r.client.DeleteGameServerBackup(ctx, server, id) },
		listBackups, func(b client.GameServerBackup) bool { return b.UUID == id })
	if err != nil {
		resp.Diagnostics.AddError("Error deleting game server backup", err.Error())
	}
}

func (r *backupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if obj, ok := subImport(ctx, req, resp, "backup"); ok {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("backup_id"), obj)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("timeouts"), nullTimeouts())...)
	}
}
