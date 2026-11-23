package gameserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

type dataSource struct{ client *client.Client }

func (d *dataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = framework.ClientFromDataSource(req, resp)
}

// readyError explains a FailedPrecondition from an API call that needs a ready server.
func (d *dataSource) readyError(ctx context.Context, server string, err error) string {
	return explainNotReady(ctx, d.client, server, err)
}

// --- noderings_game_server ---

var _ datasource.DataSourceWithConfigValidators = &serverDataSource{}

// NewServerDataSource returns the noderings_game_server data source.
func NewServerDataSource() datasource.DataSource { return &serverDataSource{} }

type serverDataSource struct{ dataSource }

type serverDataModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	PlanID          types.String `tfsdk:"plan_id"`
	TemplateID      types.String `tfsdk:"template_id"`
	RegionID        types.String `tfsdk:"region_id"`
	Phase           types.String `tfsdk:"phase"`
	LastError       types.String `tfsdk:"last_error"`
	NodeID          types.String `tfsdk:"node_id"`
	PanelIdentifier types.String `tfsdk:"panel_identifier"`
	GameName        types.String `tfsdk:"game_name"`
	Variant         types.String `tfsdk:"variant"`
	Icon            types.String `tfsdk:"icon"`
	PrimaryIP       types.String `tfsdk:"primary_ip"`
	PrimaryPort     types.Int64  `tfsdk:"primary_port"`
	PlanCode        types.String `tfsdk:"plan_code"`
	PlanBillingType types.String `tfsdk:"plan_billing_type"`
	ProviderName    types.String `tfsdk:"provider_name"`
	RegionCity      types.String `tfsdk:"region_city"`
	RegionCountry   types.String `tfsdk:"region_country"`
	DatabaseLimit   types.Int64  `tfsdk:"database_limit"`
	BackupLimit     types.Int64  `tfsdk:"backup_limit"`
	AllocationLimit types.Int64  `tfsdk:"allocation_limit"`
	CreatedAt       types.String `tfsdk:"created_at"`
}

func serverDataAttributes() map[string]schema.Attribute {
	s := func(desc string) schema.Attribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	i := func(desc string) schema.Attribute {
		return schema.Int64Attribute{Computed: true, MarkdownDescription: desc}
	}
	return map[string]schema.Attribute{
		"id":                s("Game server ID."),
		"name":              s("Server name."),
		"plan_id":           s("Plan ID."),
		"template_id":       s("Game ID."),
		"region_id":         s("Region ID."),
		"phase":             s("`pending`, `ready`, `error` or `failed`."),
		"last_error":        s("Why the last install failed, if it did."),
		"node_id":           s("Node the server runs on."),
		"panel_identifier":  s("Short server identifier shown in support conversations."),
		"game_name":         s("Game."),
		"variant":           s("Game variant."),
		"icon":              s("Game icon name."),
		"primary_ip":        s("IP address players connect to."),
		"primary_port":      i("Port players connect to."),
		"plan_code":         s("Plan code."),
		"plan_billing_type": s("`subscription`, `usage` or `free`."),
		"provider_name":     s("Provider that hosts the server."),
		"region_city":       s("Region city."),
		"region_country":    s("Region country."),
		"database_limit":    i("How many databases the plan allows."),
		"backup_limit":      i("How many backups the plan allows."),
		"allocation_limit":  i("How many extra ports the plan allows."),
		"created_at":        s("Creation time (RFC 3339)."),
	}
}

func toServerData(gs *client.GameServer) serverDataModel {
	var m serverModel
	setServer(&m, gs)
	return serverDataModel{
		ID: m.ID, Name: m.Name, PlanID: m.PlanID, TemplateID: m.TemplateID, RegionID: stringOrNull(gs.RegionID), Phase: m.Phase,
		LastError: m.LastError, NodeID: m.NodeID, PanelIdentifier: m.PanelIdentifier, GameName: m.GameName, Variant: m.Variant, Icon: m.Icon,
		PrimaryIP: stringOrNull(gs.AllocationIP), PrimaryPort: types.Int64Value(int64(gs.AllocationPort)),
		PlanCode: m.PlanCode, PlanBillingType: m.PlanBillingType, ProviderName: m.ProviderName, RegionCity: m.RegionCity,
		RegionCountry: m.RegionCountry, DatabaseLimit: m.DatabaseLimit, BackupLimit: m.BackupLimit, AllocationLimit: m.AllocationLimit,
		CreatedAt: m.CreatedAt,
	}
}

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := serverDataAttributes()
	attrs["id"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Game server ID.", Validators: []validator.String{framework.UUID()}}
	attrs["name"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Server name. Must match exactly one server."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a game server by ID or name.",
		Attributes:          attrs,
	}
}

func (d *serverDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serverDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var gs *client.GameServer
	if id := cfg.ID.ValueString(); id != "" {
		g, err := d.client.GetGameServer(ctx, id)
		if err != nil {
			resp.Diagnostics.AddError("Error reading game server", err.Error())
			return
		}
		gs = g
	} else {
		list, err := d.client.ListGameServers(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Error listing game servers", err.Error())
			return
		}
		var ids []string
		for i := range list {
			if list[i].Name == cfg.Name.ValueString() {
				gs = &list[i]
				ids = append(ids, list[i].ID)
			}
		}
		switch {
		case len(ids) == 0:
			resp.Diagnostics.AddError("Game server not found", "The organization has no game server named "+cfg.Name.ValueString()+".")
			return
		case len(ids) > 1:
			resp.Diagnostics.AddError("Several game servers match", fmt.Sprintf("%d game servers are named %s (%s). Look one up by id instead.",
				len(ids), cfg.Name.ValueString(), strings.Join(ids, ", ")))
			return
		}
		// The list leaves some fields empty; read the server itself.
		g, err := d.client.GetGameServer(ctx, gs.ID)
		if err != nil {
			resp.Diagnostics.AddError("Error reading game server", err.Error())
			return
		}
		gs = g
	}
	out := toServerData(gs)
	if gs.Phase == client.GameServerPhaseReady {
		// The server's own address fields go stale; the allocation list is authoritative.
		allocs, err := d.client.ListGameServerAllocations(ctx, gs.ID)
		if err != nil {
			resp.Diagnostics.AddError("Error reading game server allocations", err.Error())
			return
		}
		for _, a := range allocs {
			if a.IsDefault {
				out.PrimaryIP, out.PrimaryPort = types.StringValue(a.IP), types.Int64Value(int64(a.Port))
			}
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, out)...)
}

// --- noderings_game_servers ---

// NewServersDataSource returns the noderings_game_servers data source.
func NewServersDataSource() datasource.DataSource { return &serversDataSource{} }

type serversDataSource struct{ dataSource }

type serversDataModel struct {
	Name        types.String      `tfsdk:"name"`
	Phase       types.String      `tfsdk:"phase"`
	TemplateID  types.String      `tfsdk:"template_id"`
	IDs         types.List        `tfsdk:"ids"`
	GameServers []serverDataModel `tfsdk:"game_servers"`
}

func (d *serversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_servers"
}

func (d *serversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := serverDataAttributes()
	attrs["primary_ip"] = schema.StringAttribute{Computed: true, MarkdownDescription: "IP address recorded when the server was installed. Use the `noderings_game_server` data source for the current primary address."}
	attrs["primary_port"] = schema.Int64Attribute{Computed: true, MarkdownDescription: "Port recorded when the server was installed."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the organization's game servers, newest first, optionally filtered.",
		Attributes: map[string]schema.Attribute{
			"name":        schema.StringAttribute{Optional: true, MarkdownDescription: "Only servers with this exact name."},
			"phase":       schema.StringAttribute{Optional: true, MarkdownDescription: "Only servers in this phase: `pending`, `ready`, `error` or `failed`.", Validators: []validator.String{stringvalidator.OneOf("pending", "ready", "error", "failed")}},
			"template_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Only servers running this game.", Validators: []validator.String{framework.UUID()}},
			"ids":         schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "IDs of the matching servers."},
			"game_servers": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The matching servers.",
				NestedObject: schema.NestedAttributeObject{Attributes: attrs},
			},
		},
	}
}

func (d *serversDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serversDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.client.ListGameServers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing game servers", err.Error())
		return
	}
	m.GameServers = []serverDataModel{}
	ids := []string{}
	for i := range list {
		gs := &list[i]
		if (!m.Name.IsNull() && gs.Name != m.Name.ValueString()) || (!m.Phase.IsNull() && phase(gs.Phase) != m.Phase.ValueString()) ||
			(!m.TemplateID.IsNull() && gs.TemplateID != m.TemplateID.ValueString()) {
			continue
		}
		m.GameServers = append(m.GameServers, toServerData(gs))
		ids = append(ids, gs.ID)
	}
	m.IDs = framework.Strings(ctx, ids, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_game_server_games ---

// NewGamesDataSource returns the noderings_game_server_games data source.
func NewGamesDataSource() datasource.DataSource { return &gamesDataSource{} }

type gamesDataSource struct{ dataSource }

type gameVariableModel struct {
	EnvVariable  types.String `tfsdk:"env_variable"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	DefaultValue types.String `tfsdk:"default_value"`
	Required     types.Bool   `tfsdk:"required"`
	UserEditable types.Bool   `tfsdk:"user_editable"`
	Rules        types.String `tfsdk:"rules"`
}

type gameModel struct {
	ID        types.String        `tfsdk:"id"`
	Name      types.String        `tfsdk:"name"`
	Icon      types.String        `tfsdk:"icon"`
	EULA      types.Bool          `tfsdk:"eula"`
	Variables []gameVariableModel `tfsdk:"variables"`
}

type gamesDataModel struct {
	PlanID    types.String        `tfsdk:"plan_id"`
	Name      types.String        `tfsdk:"name"`
	ID        types.String        `tfsdk:"id"`
	Icon      types.String        `tfsdk:"icon"`
	EULA      types.Bool          `tfsdk:"eula"`
	Variables []gameVariableModel `tfsdk:"variables"`
	Games     []gameModel         `tfsdk:"games"`
}

func (d *gamesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server_games"
}

func variablesAttribute(desc string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Computed: true, MarkdownDescription: desc,
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"env_variable":  schema.StringAttribute{Computed: true, MarkdownDescription: "Variable name, the key to use in `environment`."},
			"name":          schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
			"description":   schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
			"default_value": schema.StringAttribute{Computed: true, MarkdownDescription: "Default value."},
			"required":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the variable must have a value."},
			"user_editable": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the variable can be changed after the server is created."},
			"rules":         schema.StringAttribute{Computed: true, MarkdownDescription: "Validation rules the value must pass, for example `required|integer|max:1000`."},
		}},
	}
}

func (d *gamesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the games that can be ordered on a game-server plan, and looks one up by name. " +
			"Use the `id` as `noderings_game_server.template_id` and the `variables` to fill in `environment`.",
		Attributes: map[string]schema.Attribute{
			"plan_id": schema.StringAttribute{Required: true, MarkdownDescription: "Game-server plan ID.", Validators: []validator.String{framework.UUID()}},
			"name": schema.StringAttribute{Optional: true,
				MarkdownDescription: "Game to look up, as `<game> · <variant>`, for example `Minecraft · Paper` (case-insensitive). When set, `id`, `icon`, `eula` and `variables` describe it."},
			"id":        schema.StringAttribute{Computed: true, MarkdownDescription: "ID of the game named `name`."},
			"icon":      schema.StringAttribute{Computed: true, MarkdownDescription: "Icon of the game named `name`."},
			"eula":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the game named `name` has a licence agreement that must be accepted (`accept_eula`)."},
			"variables": variablesAttribute("Startup variables of the game named `name`."),
			"games": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Every game on the plan.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":        schema.StringAttribute{Computed: true, MarkdownDescription: "Game ID."},
					"name":      schema.StringAttribute{Computed: true, MarkdownDescription: "Name, as `<game> · <variant>`."},
					"icon":      schema.StringAttribute{Computed: true, MarkdownDescription: "Icon name."},
					"eula":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the game has a licence agreement."},
					"variables": variablesAttribute("Startup variables."),
				}},
			},
		},
	}
}

func toGame(g *client.OrderGame) gameModel {
	vars := []gameVariableModel{}
	for _, v := range g.Variables {
		vars = append(vars, gameVariableModel{
			EnvVariable: types.StringValue(v.EnvVariable), Name: types.StringValue(v.Name), Description: types.StringValue(v.Description),
			DefaultValue: types.StringValue(v.DefaultValue), Required: types.BoolValue(v.IsRequired || strings.Contains("|"+v.Rules+"|", "|required|")),
			UserEditable: types.BoolValue(v.UserEditable), Rules: types.StringValue(v.Rules),
		})
	}
	return gameModel{ID: types.StringValue(g.ID), Name: types.StringValue(g.Name), Icon: types.StringValue(g.Icon),
		EULA: types.BoolValue(g.EULA != nil && *g.EULA), Variables: vars}
}

func (d *gamesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m gamesDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	games, err := d.client.ListOrderGames(ctx, m.PlanID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error listing games", err.Error())
		return
	}
	m.Games = []gameModel{}
	m.ID, m.Icon, m.EULA, m.Variables = types.StringNull(), types.StringNull(), types.BoolNull(), nil
	var names []string
	for i := range games {
		g := toGame(&games[i])
		m.Games = append(m.Games, g)
		names = append(names, games[i].Name)
		if !m.Name.IsNull() && strings.EqualFold(games[i].Name, m.Name.ValueString()) {
			m.ID, m.Icon, m.EULA, m.Variables = g.ID, g.Icon, g.EULA, g.Variables
		}
	}
	if !m.Name.IsNull() && m.ID.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Game not found",
			fmt.Sprintf("Plan %s has no game named %q. Games on this plan: %s.", m.PlanID.ValueString(), m.Name.ValueString(), strings.Join(names, ", ")))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_game_server_startup ---

// NewStartupDataSource returns the noderings_game_server_startup data source.
func NewStartupDataSource() datasource.DataSource { return &startupDataSource{} }

type startupDataSource struct{ dataSource }

type dockerImageModel struct {
	Name  types.String `tfsdk:"name"`
	Image types.String `tfsdk:"image"`
}

type startupVariableModel struct {
	EnvVariable  types.String `tfsdk:"env_variable"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	Value        types.String `tfsdk:"value"`
	UserEditable types.Bool   `tfsdk:"user_editable"`
	Rules        types.String `tfsdk:"rules"`
}

type startupDataModel struct {
	GameServerID      types.String           `tfsdk:"game_server_id"`
	StartupCommand    types.String           `tfsdk:"startup_command"`
	RawStartupCommand types.String           `tfsdk:"raw_startup_command"`
	DockerImage       types.String           `tfsdk:"docker_image"`
	DockerImages      []dockerImageModel     `tfsdk:"docker_images"`
	Variables         []startupVariableModel `tfsdk:"variables"`
}

func (d *startupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server_startup"
}

func (d *startupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a game server's startup command, startup variables and container image. The server must be ready.",
		Attributes: map[string]schema.Attribute{
			"game_server_id":      schema.StringAttribute{Required: true, MarkdownDescription: "Game server ID.", Validators: []validator.String{framework.UUID()}},
			"startup_command":     schema.StringAttribute{Computed: true, MarkdownDescription: "Startup command with the variables filled in."},
			"raw_startup_command": schema.StringAttribute{Computed: true, MarkdownDescription: "Startup command with variable placeholders."},
			"docker_image":        schema.StringAttribute{Computed: true, MarkdownDescription: "Current container image."},
			"docker_images": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Images the game can run on, for `noderings_game_server.docker_image`.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":  schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"image": schema.StringAttribute{Computed: true, MarkdownDescription: "Image reference."},
				}},
			},
			"variables": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Startup variables and their current values.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"env_variable":  schema.StringAttribute{Computed: true, MarkdownDescription: "Variable name."},
					"name":          schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"description":   schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
					"value":         schema.StringAttribute{Computed: true, MarkdownDescription: "Current value."},
					"user_editable": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the variable can be changed."},
					"rules":         schema.StringAttribute{Computed: true, MarkdownDescription: "Validation rules."},
				}},
			},
		},
	}
}

func (d *startupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m startupDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := m.GameServerID.ValueString()
	st, err := d.client.GetGameServerStartup(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading game server startup settings", d.readyError(ctx, id, err))
		return
	}
	m.StartupCommand = types.StringValue(st.StartupCommand)
	m.RawStartupCommand = types.StringValue(st.RawStartupCommand)
	m.DockerImage = types.StringValue(st.DockerImage)
	m.DockerImages = []dockerImageModel{}
	for _, i := range st.DockerImages {
		m.DockerImages = append(m.DockerImages, dockerImageModel{Name: types.StringValue(i.Name), Image: types.StringValue(i.Image)})
	}
	m.Variables = []startupVariableModel{}
	for _, v := range st.Variables {
		m.Variables = append(m.Variables, startupVariableModel{
			EnvVariable: types.StringValue(v.EnvVariable), Name: types.StringValue(v.Name), Description: types.StringValue(v.Description),
			Value: types.StringValue(v.ServerValue), UserEditable: types.BoolValue(v.UserEditable), Rules: types.StringValue(v.Rules),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// --- noderings_game_server_resources ---

// NewResourcesDataSource returns the noderings_game_server_resources data source.
func NewResourcesDataSource() datasource.DataSource { return &resourcesDataSource{} }

type resourcesDataSource struct{ dataSource }

type resourcesDataModel struct {
	GameServerID   types.String  `tfsdk:"game_server_id"`
	State          types.String  `tfsdk:"state"`
	CPUAbsolute    types.Float64 `tfsdk:"cpu_absolute"`
	MemoryBytes    types.Int64   `tfsdk:"memory_bytes"`
	DiskBytes      types.Int64   `tfsdk:"disk_bytes"`
	NetworkRxBytes types.Int64   `tfsdk:"network_rx_bytes"`
	NetworkTxBytes types.Int64   `tfsdk:"network_tx_bytes"`
	UptimeMillis   types.Int64   `tfsdk:"uptime_ms"`
}

func (d *resourcesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server_resources"
}

func (d *resourcesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	i := func(desc string) schema.Attribute {
		return schema.Int64Attribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a game server's live state and resource usage. The server must be ready.",
		Attributes: map[string]schema.Attribute{
			"game_server_id":   schema.StringAttribute{Required: true, MarkdownDescription: "Game server ID.", Validators: []validator.String{framework.UUID()}},
			"state":            schema.StringAttribute{Computed: true, MarkdownDescription: "`running`, `starting`, `stopping` or `offline`."},
			"cpu_absolute":     schema.Float64Attribute{Computed: true, MarkdownDescription: "CPU use in percent of one core."},
			"memory_bytes":     i("Memory in use, in bytes."),
			"disk_bytes":       i("Disk space in use, in bytes."),
			"network_rx_bytes": i("Bytes received since the server started."),
			"network_tx_bytes": i("Bytes sent since the server started."),
			"uptime_ms":        i("Milliseconds since the server started."),
		},
	}
}

func (d *resourcesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m resourcesDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := m.GameServerID.ValueString()
	res, err := d.client.GetGameServerResources(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading game server resources", d.readyError(ctx, id, err))
		return
	}
	m.State = types.StringValue(res.State)
	m.CPUAbsolute = types.Float64Value(res.CPUAbsolute)
	m.MemoryBytes = types.Int64Value(int64(res.MemoryBytes))
	m.DiskBytes = types.Int64Value(int64(res.DiskBytes))
	m.NetworkRxBytes = types.Int64Value(int64(res.NetworkRxBytes))
	m.NetworkTxBytes = types.Int64Value(int64(res.NetworkTxBytes))
	m.UptimeMillis = types.Int64Value(int64(res.UptimeMillis))
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
