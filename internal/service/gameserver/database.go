package gameserver

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
)

var (
	dbNameRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)
	dbRemoteRE = regexp.MustCompile(`^[A-Za-z0-9_\-/.%:]{1,255}$`)
	// The platform prefixes database names with the server's number, for example s1_luckperms.
	dbPrefixRE = regexp.MustCompile(`^s\d+_(.+)$`)
)

var (
	_ resource.Resource                = &databaseResource{}
	_ resource.ResourceWithImportState = &databaseResource{}
	_ resource.ResourceWithModifyPlan  = &databaseResource{}
	_ resource.ResourceWithConfigure   = &databaseResource{}
)

// NewDatabaseResource returns the noderings_game_server_database resource.
func NewDatabaseResource() resource.Resource { return &databaseResource{} }

type databaseResource struct{ subResource }

type databaseModel struct {
	ID              types.String `tfsdk:"id"`
	GameServerID    types.String `tfsdk:"game_server_id"`
	DatabaseID      types.String `tfsdk:"database_id"`
	Name            types.String `tfsdk:"name"`
	Remote          types.String `tfsdk:"remote"`
	RotationTrigger types.String `tfsdk:"rotation_trigger"`
	FullName        types.String `tfsdk:"full_name"`
	Username        types.String `tfsdk:"username"`
	Host            types.String `tfsdk:"host"`
	Port            types.Int64  `tfsdk:"port"`
	ConnectionsFrom types.String `tfsdk:"connections_from"`
	Password        types.String `tfsdk:"password"`
	JDBCURL         types.String `tfsdk:"jdbc_url"`
}

func (r *databaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server_database"
}

func (r *databaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A MySQL database for a game server, for example for a plugin. Databases count against the plan's `database_limit`. " +
			"The password is only returned when the database is created and when it is rotated; Terraform keeps it in state (marked sensitive). " +
			"The game server must be ready.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute(),
			"game_server_id": serverIDAttribute(),
			"database_id":    schema.StringAttribute{Computed: true, MarkdownDescription: "Database ID.", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required: true, PlanModifiers: replace,
				MarkdownDescription: "Database name: letters, digits, `_` and `-`, up to 48 characters. NodeRings adds a prefix (see `full_name`). Changing it replaces the database.",
				Validators:          []validator.String{stringvalidator.RegexMatches(dbNameRE, "must be 1 to 48 letters, digits, _ or -")},
			},
			"remote": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("%"), PlanModifiers: replace,
				MarkdownDescription: "Hosts that may connect, in MySQL notation. `%` (the default) allows any host. Changing it replaces the database.",
				Validators:          []validator.String{stringvalidator.RegexMatches(dbRemoteRE, "must be a MySQL host pattern such as % or 192.0.2.%")},
			},
			"rotation_trigger": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Any value. Changing it sets a new random password. For example, set it to the current month to rotate monthly.",
			},
			"full_name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Database name as created, with the platform's prefix.", PlanModifiers: keep},
			"username":         schema.StringAttribute{Computed: true, MarkdownDescription: "User name.", PlanModifiers: keep},
			"host":             schema.StringAttribute{Computed: true, MarkdownDescription: "Database host.", PlanModifiers: keep},
			"port":             schema.Int64Attribute{Computed: true, MarkdownDescription: "Database port.", PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"connections_from": schema.StringAttribute{Computed: true, MarkdownDescription: "Hosts that may connect, as stored.", PlanModifiers: keep},
			"password": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep,
				MarkdownDescription: "Password. Only known after create or rotation: after an import it is empty until `rotation_trigger` changes.",
			},
			"jdbc_url": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep,
				MarkdownDescription: "JDBC connection URL, including the password. Known under the same conditions as `password`.",
			},
		},
	}
}

// ModifyPlan marks the password unknown when a rotation is planned.
func (r *databaseResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("rotation_trigger"), &plan)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("rotation_trigger"), &state)...)
	if resp.Diagnostics.HasError() || plan.Equal(state) {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("password"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("jdbc_url"), types.StringUnknown())...)
}

func (r *databaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := plan.GameServerID.ValueString()
	db, err := r.client.CreateGameServerDatabase(ctx, server, plan.Name.ValueString(), plan.Remote.ValueString())
	invalidate(r.client, server, "databases")
	if err != nil && client.IsGameServerDeadlineExceeded(err) {
		// The database may exist anyway. Adopt it, but its password is lost: rotate it.
		if list, lerr := r.client.ListGameServerDatabases(ctx, server); lerr == nil {
			for i := range list {
				if dbShortName(list[i].Name) == plan.Name.ValueString() {
					if rotated, rerr := r.client.RotateGameServerDatabasePassword(ctx, server, list[i].ID); rerr == nil {
						db, err = rotated, nil
					}
					break
				}
			}
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating game server database", explainNotReady(ctx, r.client, server, err))
		return
	}
	setDatabase(&plan, db)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func dbShortName(full string) string {
	if m := dbPrefixRE.FindStringSubmatch(full); m != nil {
		return m[1]
	}
	return full
}

// setDatabase copies the API object. Name keeps the configured value; the
// password is only replaced when the response carries one (create and
// rotate), never by the empty value a list returns.
func setDatabase(m *databaseModel, d *client.GameServerDatabase) {
	m.DatabaseID = types.StringValue(d.ID)
	m.ID = types.StringValue(m.GameServerID.ValueString() + "/" + d.ID)
	if m.Name.IsNull() || m.Name.IsUnknown() {
		m.Name = types.StringValue(dbShortName(d.Name))
	}
	m.FullName = types.StringValue(d.Name)
	m.Username = types.StringValue(d.Username)
	m.Host = types.StringValue(d.Host)
	m.Port = types.Int64Value(int64(d.Port))
	m.ConnectionsFrom = types.StringValue(d.ConnectionsFrom)
	if m.Remote.IsNull() || m.Remote.IsUnknown() {
		m.Remote = types.StringValue(d.ConnectionsFrom)
	}
	if d.Password != "" {
		m.Password = types.StringValue(d.Password)
		m.JDBCURL = types.StringValue(d.JDBC)
	}
	if m.Password.IsUnknown() {
		m.Password = types.StringNull()
	}
	if m.JDBCURL.IsUnknown() {
		m.JDBCURL = types.StringNull()
	}
}

func (r *databaseResource) find(list []client.GameServerDatabase, id string) *client.GameServerDatabase {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func (r *databaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, ok := readSub(ctx, r.client, state.GameServerID.ValueString(), "databases", listDatabases, &resp.Diagnostics, func() { resp.State.RemoveResource(ctx) })
	if !ok {
		return
	}
	db := r.find(list, state.DatabaseID.ValueString())
	if db == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	setDatabase(&state, db)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only rotates the password; every other argument forces replacement.
func (r *databaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := state.GameServerID.ValueString()
	rotate := !plan.RotationTrigger.Equal(state.RotationTrigger)
	state.RotationTrigger = plan.RotationTrigger
	if rotate {
		db, err := r.client.RotateGameServerDatabasePassword(ctx, server, state.DatabaseID.ValueString())
		invalidate(r.client, server, "databases")
		if err != nil {
			resp.Diagnostics.AddError("Error rotating the database password", explainNotReady(ctx, r.client, server, err))
			return
		}
		setDatabase(&state, db)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *databaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server, id := state.GameServerID.ValueString(), state.DatabaseID.ValueString()
	err := deleteSub(ctx, r.client, server, "databases",
		func() error { return r.client.DeleteGameServerDatabase(ctx, server, id) },
		listDatabases, func(d client.GameServerDatabase) bool { return d.ID == id })
	if err != nil {
		resp.Diagnostics.AddError("Error deleting game server database", err.Error())
	}
}

func (r *databaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if obj, ok := subImport(ctx, req, resp, "database"); ok {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("database_id"), obj)...)
	}
}
