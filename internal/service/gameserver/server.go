package gameserver

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

const (
	defaultCreateTimeout = 30 * time.Minute
	defaultUpdateTimeout = 30 * time.Minute
	defaultDeleteTimeout = 15 * time.Minute
	privateKeyIdem       = "idempotency_key"
)

var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	_ resource.Resource                = &serverResource{}
	_ resource.ResourceWithImportState = &serverResource{}
	_ resource.ResourceWithModifyPlan  = &serverResource{}
	_ resource.ResourceWithConfigure   = &serverResource{}
)

// NewServerResource returns the noderings_game_server resource.
func NewServerResource() resource.Resource { return &serverResource{} }

type serverResource struct{ client *client.Client }

type serverModel struct {
	ID                         types.String `tfsdk:"id"`
	Name                       types.String `tfsdk:"name"`
	PlanID                     types.String `tfsdk:"plan_id"`
	TemplateID                 types.String `tfsdk:"template_id"`
	RegionID                   types.String `tfsdk:"region_id"`
	Environment                types.Map    `tfsdk:"environment"`
	VersionValue               types.String `tfsdk:"version_value"`
	DockerImage                types.String `tfsdk:"docker_image"`
	AcceptEULA                 types.Bool   `tfsdk:"accept_eula"`
	PowerState                 types.String `tfsdk:"power_state"`
	AllowDestructiveGameChange types.Bool   `tfsdk:"allow_destructive_game_change"`
	DeletionProtection         types.Bool   `tfsdk:"deletion_protection"`
	Phase                      types.String `tfsdk:"phase"`
	LastError                  types.String `tfsdk:"last_error"`
	NodeID                     types.String `tfsdk:"node_id"`
	PanelIdentifier            types.String `tfsdk:"panel_identifier"`
	GameName                   types.String `tfsdk:"game_name"`
	Variant                    types.String `tfsdk:"variant"`
	Icon                       types.String `tfsdk:"icon"`
	PrimaryIP                  types.String `tfsdk:"primary_ip"`
	PrimaryPort                types.Int64  `tfsdk:"primary_port"`
	PlanCode                   types.String `tfsdk:"plan_code"`
	PlanBillingType            types.String `tfsdk:"plan_billing_type"`
	ProviderName               types.String `tfsdk:"provider_name"`
	RegionCity                 types.String `tfsdk:"region_city"`
	RegionCountry              types.String `tfsdk:"region_country"`
	DatabaseLimit              types.Int64  `tfsdk:"database_limit"`
	BackupLimit                types.Int64  `tfsdk:"backup_limit"`
	AllocationLimit            types.Int64  `tfsdk:"allocation_limit"`
	CreatedAt                  types.String `tfsdk:"created_at"`
	Timeouts                   types.Object `tfsdk:"timeouts"`
}

func (r *serverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server"
}

func (r *serverResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	keepInt := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc, PlanModifiers: keep}
	}
	computedInt := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Computed: true, MarkdownDescription: desc, PlanModifiers: keepInt}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A game server ordered from a game-server plan. Creating it places an order that is billed according to the plan; " +
			"Terraform waits until the game is installed. Destroying a fixed-price server forfeits the rest of the paid period.",
		Attributes: map[string]schema.Attribute{
			"id": computed("Game server ID."),
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Server name, 1 to 64 characters. Changes in place while the server is ready.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 64), framework.SafeString(),
					stringvalidator.RegexMatches(regexp.MustCompile(`^\S(.*\S)?$`), "must not start or end with whitespace")},
			},
			"plan_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "Game-server plan ID. A server cannot move to another plan, so changing it replaces the server (its files are lost).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{framework.UUID()},
			},
			"template_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Game to install, the `id` of a `noderings_game_server_games` entry. Game IDs are specific to the plan's provider. " +
					"Changing it replaces the server, unless `allow_destructive_game_change` is `true`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						var allow types.Bool
						resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("allow_destructive_game_change"), &allow)...)
						resp.RequiresReplace = !allow.ValueBool()
					},
					"Replaces the server unless allow_destructive_game_change is true.",
					"Replaces the server unless `allow_destructive_game_change` is `true`.",
				)},
				Validators: []validator.String{framework.UUID()},
			},
			"region_id": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Region to place the server in. Defaults to the region of the node the server is placed on. Changing it replaces the server.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{framework.UUID()},
			},
			"environment": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Startup variables, such as the game version or player limit. Only the keys listed here are managed; other variables keep their values. " +
					"At creation, variables that cannot be edited may only be set to their default. Afterwards only user-editable variables can change. " +
					"Keys are checked against the game's variables (see `noderings_game_server_games`). " +
					"A change applies the next time the game starts: restart the server (for example by setting `power_state` to `offline` and back) to use it. " +
					"Removing a key stops managing it; the server keeps its current value.",
				Validators: []validator.Map{
					mapvalidator.KeysAre(stringvalidator.RegexMatches(envKeyRE, "must be an environment variable name")),
					mapvalidator.ValueStringsAre(stringvalidator.LengthAtMost(4096), framework.SafeString()),
				},
			},
			"version_value": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Game version to install, at most 64 characters. Only used when the server is created; to change the version later, " +
					"set the game's version variable in `environment`. Not read back from the API.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 64)},
			},
			"docker_image": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "Container image the game runs in. Must be one of the game's images (`noderings_game_server_startup.docker_images`). Changes in place.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 512), stringvalidator.RegexMatches(regexp.MustCompile(`^\S+$`), "must not contain whitespace")},
			},
			"accept_eula": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Accept the game's end-user licence agreement. Required when the game has one (`noderings_game_server_games.eula`). " +
					"Only used when the server is created or its game changes. Not read back from the API.",
			},
			"power_state": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "`running` or `offline`. When set, Terraform starts or stops the server and waits for it. When not set, it reports the live state. " +
					"New servers start once installed.",
				Validators: []validator.String{stringvalidator.OneOf("running", "offline")},
			},
			"allow_destructive_game_change": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "When `true`, changing `template_id` changes the game in place instead of replacing the server. " +
					"The server keeps its address, but **all files, schedules and backups are deleted**. Defaults to `false`.",
			},
			"deletion_protection": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "When `true`, Terraform refuses to delete the server. Defaults to `false`.",
			},
			"phase": schema.StringAttribute{
				Computed: true, PlanModifiers: keep,
				MarkdownDescription: "`pending` while installing, `ready`, `error` (failed after payment; reinstall or change the game) or `failed` (failed before payment; delete it).",
			},
			"last_error":        schema.StringAttribute{Computed: true, PlanModifiers: keep, MarkdownDescription: "Why the last install failed, if it did."},
			"node_id":           computed("Node the server runs on."),
			"panel_identifier":  computed("Short server identifier shown in support conversations."),
			"game_name":         computed("Game, for example `Minecraft`."),
			"variant":           computed("Game variant, for example `Paper`."),
			"icon":              computed("Game icon name."),
			"primary_ip":        computed("IP address players connect to."),
			"primary_port":      computedInt("Port players connect to."),
			"plan_code":         computed("Plan code, for example `SC-GS-4C-8G-50`."),
			"plan_billing_type": computed("`subscription`, `usage` or `free`."),
			"provider_name":     computed("Provider that hosts the server."),
			"region_city":       computed("Region city."),
			"region_country":    computed("Region country."),
			"database_limit":    computedInt("How many databases the plan allows."),
			"backup_limit":      computedInt("How many backups the plan allows."),
			"allocation_limit":  computedInt("How many extra ports (allocations) the plan allows, besides the primary one."),
			"created_at":        computed("Creation time (RFC 3339)."),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeoutsBlock(map[string]string{"create": "30m", "update": "30m", "delete": "15m"}),
		},
	}
}

func (r *serverResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = framework.ClientFromResource(req, resp)
}

// --- plan ---

func (r *serverResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan, cfg serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state *serverModel
	if !req.State.Raw.IsNull() {
		state = &serverModel{}
		resp.Diagnostics.Append(req.State.Get(ctx, state)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	changeGame := state != nil && !plan.TemplateID.IsUnknown() && plan.TemplateID.ValueString() != state.TemplateID.ValueString() &&
		plan.AllowDestructiveGameChange.ValueBool()
	if changeGame {
		resp.Diagnostics.AddAttributeWarning(path.Root("template_id"), "Changing the game deletes data",
			"Changing template_id in place reinstalls the server with the new game. All files, schedules and backups on the server are deleted.")
		for _, attr := range []string{"phase", "last_error", "game_name", "variant", "icon"} {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(attr), types.StringUnknown())...)
		}
		if cfg.DockerImage.IsNull() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("docker_image"), types.StringUnknown())...)
		}
		if cfg.PowerState.IsNull() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("power_state"), types.StringUnknown())...)
		}
	}
	if state != nil && !plan.VersionValue.IsUnknown() && !plan.VersionValue.Equal(state.VersionValue) && !plan.VersionValue.IsNull() {
		resp.Diagnostics.AddAttributeWarning(path.Root("version_value"), "version_value only applies at creation",
			"Changing version_value does not change the installed version. Set the game's version variable in environment instead.")
	}
	fresh := state == nil || changeGame
	envChanged := state != nil && !plan.Environment.Equal(state.Environment)
	if fresh || envChanged {
		r.validateGame(ctx, &plan, state, fresh, &resp.Diagnostics)
	}
}

// validateGame checks accept_eula and environment against the game's metadata.
func (r *serverResource) validateGame(ctx context.Context, plan, state *serverModel, fresh bool, diags *diag.Diagnostics) {
	if plan.PlanID.IsUnknown() || plan.TemplateID.IsUnknown() || plan.Environment.IsUnknown() || plan.AcceptEULA.IsUnknown() {
		return
	}
	games, err := r.client.ListOrderGames(ctx, plan.PlanID.ValueString())
	if err != nil {
		if fresh {
			diags.AddWarning("Could not check the game's variables", "Listing the games of plan "+plan.PlanID.ValueString()+" failed, so environment was not checked: "+err.Error())
		}
		return
	}
	var game *client.OrderGame
	names := make([]string, 0, len(games))
	for i := range games {
		names = append(names, games[i].Name+" ("+games[i].ID+")")
		if games[i].ID == plan.TemplateID.ValueString() {
			game = &games[i]
		}
	}
	if game == nil {
		if fresh {
			diags.AddAttributeError(path.Root("template_id"), "Game not available on this plan",
				fmt.Sprintf("Game %s cannot be ordered on plan %s. Look it up with the noderings_game_server_games data source. Games on this plan: %s.",
					plan.TemplateID.ValueString(), plan.PlanID.ValueString(), strings.Join(names, ", ")))
		}
		return
	}
	if fresh && game.EULA != nil && *game.EULA && !plan.AcceptEULA.ValueBool() {
		diags.AddAttributeError(path.Root("accept_eula"), "The game's EULA must be accepted",
			fmt.Sprintf("%s has an end-user licence agreement. Set accept_eula = true to accept it.", game.Name))
	}
	vars := map[string]client.GameVariable{}
	var keys []string
	for _, v := range game.Variables {
		vars[v.EnvVariable] = v
		keys = append(keys, v.EnvVariable)
	}
	sort.Strings(keys)
	env := map[string]types.String{}
	if !plan.Environment.IsNull() {
		diags.Append(plan.Environment.ElementsAs(ctx, &env, false)...)
	}
	var prior map[string]types.String
	if state != nil && !state.Environment.IsNull() {
		diags.Append(state.Environment.ElementsAs(ctx, &prior, false)...)
	}
	for k, v := range env {
		p := path.Root("environment").AtMapKey(k)
		def, ok := vars[k]
		if !ok {
			diags.AddAttributeError(p, "Unknown startup variable",
				fmt.Sprintf("%s has no variable %s. Its variables are: %s.", game.Name, k, strings.Join(keys, ", ")))
			continue
		}
		if v.IsUnknown() || def.UserEditable {
			continue
		}
		if fresh {
			if v.ValueString() != def.DefaultValue {
				diags.AddAttributeError(p, "Startup variable cannot be set",
					fmt.Sprintf("%s cannot be edited. Remove it from environment, or set it to its default %q.", k, def.DefaultValue))
			}
		} else if pv, had := prior[k]; !had || !pv.Equal(v) {
			diags.AddAttributeError(p, "Startup variable cannot be changed", fmt.Sprintf("%s cannot be edited after the server is created. Remove it from environment.", k))
		}
	}
	if fresh {
		for _, k := range keys {
			def := vars[k]
			if !def.IsRequired && !strings.Contains("|"+def.Rules+"|", "|required|") {
				continue
			}
			v, set := env[k]
			if set && v.IsUnknown() {
				continue
			}
			if (!set || v.ValueString() == "") && def.DefaultValue == "" {
				diags.AddAttributeError(path.Root("environment"), "Required startup variable missing",
					fmt.Sprintf("%s needs the variable %s (%s). Add it to environment.", game.Name, k, def.Name))
			}
		}
	}
}

// --- CRUD ---

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.CreateGameServerRequest{
		PlanID: plan.PlanID.ValueString(), TemplateID: plan.TemplateID.ValueString(), RegionID: plan.RegionID.ValueString(),
		Name: plan.Name.ValueString(), VersionValue: plan.VersionValue.ValueString(), AcceptEULA: plan.AcceptEULA.ValueBool(),
	}
	if !plan.Environment.IsNull() {
		resp.Diagnostics.Append(plan.Environment.ElementsAs(ctx, &in.Environment, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	// The key makes a retried order return the same server instead of a second one.
	in.IdempotencyKey = newUUID()
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateKeyIdem, []byte(strconv.Quote(in.IdempotencyKey)))...)
	gs, err := r.client.CreateGameServer(ctx, in)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "new idempotency key") {
		// A failed earlier attempt with this key needs a new key, once.
		in.IdempotencyKey = newUUID()
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateKeyIdem, []byte(strconv.Quote(in.IdempotencyKey)))...)
		gs, err = r.client.CreateGameServer(ctx, in)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating game server", err.Error())
		return
	}

	// Save the ID now, so a failed or timed-out install does not lose a billed server.
	m := plan
	setServer(&m, gs)
	nullUnknowns(&m)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := gs.ID
	gs, err = r.waitInstalled(ctx, id, timeout(plan.Timeouts, "create", defaultCreateTimeout))
	if gs != nil {
		setServer(&m, gs)
		resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Game server installation did not finish", err.Error())
		return
	}

	to := timeout(plan.Timeouts, "create", defaultCreateTimeout)
	if !plan.DockerImage.IsNull() && !plan.DockerImage.IsUnknown() {
		if err := r.applyDockerImage(ctx, id, plan.DockerImage.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error setting the game server's Docker image", err.Error())
			return
		}
	}
	if !plan.PowerState.IsNull() && !plan.PowerState.IsUnknown() {
		if err := r.applyPower(ctx, id, plan.PowerState.ValueString(), to); err != nil {
			resp.Diagnostics.AddError("Error setting the game server's power state", err.Error())
			return
		}
	}
	r.readInto(ctx, &m, &resp.Diagnostics)
	nullUnknowns(&m)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// waitInstalled polls until the server leaves the pending phase.
func (r *serverResource) waitInstalled(ctx context.Context, id string, to time.Duration) (*client.GameServer, error) {
	var last *client.GameServer
	err := waitFor(ctx, to, func() (bool, error) {
		gs, err := r.client.GetGameServer(ctx, id)
		if err != nil {
			return false, err
		}
		last = gs
		return gs.Phase == client.GameServerPhaseReady || gs.Phase == client.GameServerPhaseError || gs.Phase == client.GameServerPhaseFailed, nil
	})
	if err == errWaitTimeout {
		return last, fmt.Errorf("game server %s was still installing after %s. The install may still finish: run terraform apply again later to check. "+
			"If it stays pending for more than an hour, the platform may have stopped tracking the install; contact NodeRings support with the server ID. "+
			"Raise timeouts.create to wait longer", id, to)
	}
	if err != nil {
		return last, err
	}
	switch last.Phase {
	case client.GameServerPhaseError:
		return last, fmt.Errorf("game server %s failed to install: %s\n\nThe server was paid for. Its game can be reinstalled or changed; "+
			"Terraform marks it tainted, so the next apply replaces it", id, orUnknown(last.LastError))
	case client.GameServerPhaseFailed:
		return last, fmt.Errorf("game server %s failed to install: %s\n\nNo payment was taken. It can only be deleted; "+
			"Terraform marks it tainted, so the next apply replaces it", id, orUnknown(last.LastError))
	}
	return last, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}

func (r *serverResource) applyDockerImage(ctx context.Context, id, image string) error {
	st, err := r.client.GetGameServerStartup(ctx, id)
	if err != nil {
		return err
	}
	if st.DockerImage == image {
		return nil
	}
	if err := r.client.UpdateGameServerDockerImage(ctx, id, image); err != nil {
		var choices []string
		for _, i := range st.DockerImages {
			choices = append(choices, i.Image)
		}
		return fmt.Errorf("%w\n\nImages this game can use: %s", err, strings.Join(choices, ", "))
	}
	return nil
}

// livePower maps the live state to running or offline. Transitions count as
// where they are heading.
func livePower(state string) string {
	switch state {
	case "running", "starting":
		return "running"
	case "offline", "stopping":
		return "offline"
	}
	return ""
}

// applyPower starts or stops the server and waits until it is there.
func (r *serverResource) applyPower(ctx context.Context, id, want string, to time.Duration) error {
	res, err := r.client.GetGameServerResources(ctx, id)
	if err != nil {
		return err
	}
	if res.State == want {
		return nil
	}
	if livePower(res.State) != want {
		signal := "start"
		if want == "offline" {
			signal = "stop"
		}
		if err := r.client.PowerGameServer(ctx, id, signal); err != nil {
			return err
		}
	}
	err = waitFor(ctx, to, func() (bool, error) {
		res, err := r.client.GetGameServerResources(ctx, id)
		if err != nil {
			return false, err
		}
		return res.State == want, nil
	})
	if err == errWaitTimeout {
		return fmt.Errorf("game server %s did not become %s within %s. Check the game's console output for errors", id, want, to)
	}
	return err
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if framework.GoneIfNoID(ctx, req, resp) {
		return
	}
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeletionProtection.IsNull() {
		state.DeletionProtection = types.BoolValue(false)
	}
	if state.AllowDestructiveGameChange.IsNull() {
		state.AllowDestructiveGameChange = types.BoolValue(false)
	}
	if !r.readInto(ctx, &state, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	nullUnknowns(&state)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// readInto refreshes m from the API. It returns false if the server is gone
// or reading failed.
func (r *serverResource) readInto(ctx context.Context, m *serverModel, diags *diag.Diagnostics) bool {
	id := m.ID.ValueString()
	gs, err := r.client.GetGameServer(ctx, id)
	if client.IsNotFound(err) {
		return false
	}
	if err != nil {
		diags.AddError("Error reading game server", err.Error())
		return false
	}
	setServer(m, gs)
	if gs.Phase != client.GameServerPhaseReady {
		notReadyWarning(diags, "environment, Docker image, power state and primary address", id, phase(gs.Phase))
		if m.PrimaryIP.IsNull() || m.PrimaryIP.IsUnknown() {
			m.PrimaryIP, m.PrimaryPort = stringOrNull(gs.AllocationIP), types.Int64Value(int64(gs.AllocationPort))
		}
		return true
	}

	startup, err := r.client.GetGameServerStartup(ctx, id)
	if err != nil {
		diags.AddError("Error reading game server startup settings", err.Error())
		return false
	}
	m.DockerImage = types.StringValue(startup.DockerImage)
	if !m.Environment.IsNull() && !m.Environment.IsUnknown() {
		env := map[string]string{}
		diags.Append(m.Environment.ElementsAs(ctx, &env, false)...)
		for _, v := range startup.Variables {
			if _, managed := env[v.EnvVariable]; managed {
				env[v.EnvVariable] = v.ServerValue
			}
		}
		val, d := types.MapValueFrom(ctx, types.StringType, env)
		diags.Append(d...)
		m.Environment = val
	}

	// The server's own address fields go stale when the primary changes; the
	// allocation list is authoritative.
	allocs, err := listAllocations(ctx, r.client, id)
	if err != nil {
		diags.AddError("Error reading game server allocations", err.Error())
		return false
	}
	m.PrimaryIP, m.PrimaryPort = stringOrNull(gs.AllocationIP), types.Int64Value(int64(gs.AllocationPort))
	for _, a := range allocs {
		if a.IsDefault {
			m.PrimaryIP, m.PrimaryPort = types.StringValue(a.IP), types.Int64Value(int64(a.Port))
		}
	}

	res, err := r.client.GetGameServerResources(ctx, id)
	if err != nil {
		diags.AddError("Error reading game server power state", err.Error())
		return false
	}
	if p := livePower(res.State); p != "" {
		m.PowerState = types.StringValue(p)
	}
	return true
}

func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	to := timeout(plan.Timeouts, "update", defaultUpdateTimeout)
	changeGame := plan.TemplateID.ValueString() != state.TemplateID.ValueString()
	rename := plan.Name.ValueString() != state.Name.ValueString()
	envChanged := !plan.Environment.Equal(state.Environment)
	image := !plan.DockerImage.IsUnknown() && !plan.DockerImage.IsNull() && (changeGame || !plan.DockerImage.Equal(state.DockerImage))
	power := !plan.PowerState.IsUnknown() && !plan.PowerState.IsNull() && (changeGame || !plan.PowerState.Equal(state.PowerState))

	if changeGame || rename || envChanged || image || power {
		gs, err := r.client.GetGameServer(ctx, id)
		if err != nil {
			resp.Diagnostics.AddError("Error reading game server", err.Error())
			return
		}
		ok := gs.Phase == client.GameServerPhaseReady || (changeGame && gs.Phase == client.GameServerPhaseError)
		if !ok {
			resp.Diagnostics.AddError("Game server is not ready",
				fmt.Sprintf("Game server %s is %s. Its name, game, environment, image and power state can only change while it is ready. "+
					"Wait for the current install to finish and apply again. A failed server can only be deleted.", id, phase(gs.Phase)))
			return
		}
	}

	if changeGame {
		env := map[string]string{}
		if !plan.Environment.IsNull() {
			resp.Diagnostics.Append(plan.Environment.ElementsAs(ctx, &env, false)...)
		}
		if err := r.client.ChangeGameServerGame(ctx, id, plan.TemplateID.ValueString(), env, plan.AcceptEULA.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Error changing the game", err.Error())
			return
		}
		for _, kind := range []string{"schedules", "backups"} {
			invalidate(r.client, id, kind)
		}
		if _, err := r.waitInstalled(ctx, id, to); err != nil {
			resp.Diagnostics.AddError("Game change did not finish", err.Error())
			return
		}
	}
	if rename {
		if err := r.client.RenameGameServer(ctx, id, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error renaming game server", err.Error())
			return
		}
	}
	if envChanged && !changeGame && !plan.Environment.IsNull() {
		want, have := map[string]string{}, map[string]string{}
		resp.Diagnostics.Append(plan.Environment.ElementsAs(ctx, &want, false)...)
		if !state.Environment.IsNull() {
			resp.Diagnostics.Append(state.Environment.ElementsAs(ctx, &have, false)...)
		}
		keys := make([]string, 0, len(want))
		for k := range want {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if cur, ok := have[k]; ok && cur == want[k] {
				continue
			}
			if err := r.client.UpdateGameServerStartupVariable(ctx, id, k, want[k]); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("environment").AtMapKey(k), "Error setting startup variable "+k, err.Error())
				return
			}
		}
	}
	if image {
		if err := r.applyDockerImage(ctx, id, plan.DockerImage.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error setting the game server's Docker image", err.Error())
			return
		}
	}
	if power {
		if err := r.applyPower(ctx, id, plan.PowerState.ValueString(), to); err != nil {
			resp.Diagnostics.AddError("Error setting the game server's power state", err.Error())
			return
		}
	}

	m := plan
	m.ID = state.ID
	if !r.readInto(ctx, &m, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Game server disappeared", "Game server "+id+" was deleted while it was being updated.")
		}
		return
	}
	nullUnknowns(&m)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if state.DeletionProtection.ValueBool() {
		resp.Diagnostics.AddError("Deletion protection is on",
			"Set deletion_protection = false and apply before destroying game server "+state.Name.ValueString()+" ("+id+").")
		return
	}
	err := r.client.DeleteGameServer(ctx, id)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting game server", err.Error())
		return
	}
	for _, kind := range []string{"allocations", "databases", "schedules", "backups"} {
		invalidate(r.client, id, kind)
	}
	to := timeout(state.Timeouts, "delete", defaultDeleteTimeout)
	err = waitFor(ctx, to, func() (bool, error) {
		_, err := r.client.GetGameServer(ctx, id)
		if client.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err == errWaitTimeout {
		resp.Diagnostics.AddError("Game server deletion did not finish",
			fmt.Sprintf("Game server %s was still being deleted after %s. Billing has stopped; check again later and raise timeouts.delete if this repeats.", id, to))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for game server deletion", err.Error())
	}
}

func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deletion_protection"), false)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allow_destructive_game_change"), false)...)
}

// setServer copies the API object into the model. Empty values in partial
// responses do not overwrite known ones.
func setServer(m *serverModel, gs *client.GameServer) {
	m.ID = types.StringValue(gs.ID)
	m.Name = types.StringValue(gs.Name)
	m.PlanID = types.StringValue(gs.PlanID)
	m.TemplateID = types.StringValue(gs.TemplateID)
	m.Phase = types.StringValue(phase(gs.Phase))
	m.LastError = types.StringValue(gs.LastError)
	m.GameName = types.StringValue(gs.GameName)
	m.Variant = types.StringValue(gs.Variant)
	m.Icon = types.StringValue(gs.Icon)
	set := func(dst *types.String, v string) {
		if v != "" || dst.IsNull() || dst.IsUnknown() {
			*dst = types.StringValue(v)
		}
	}
	if gs.RegionID != "" {
		m.RegionID = types.StringValue(gs.RegionID)
	}
	set(&m.NodeID, gs.NodeID)
	set(&m.PanelIdentifier, gs.PanelIdentifier)
	set(&m.PlanCode, gs.PlanCode)
	set(&m.PlanBillingType, framework.TrimEnum(gs.PlanBillingType, "BILLING_TYPE_"))
	set(&m.ProviderName, gs.ProviderName)
	set(&m.RegionCity, gs.RegionCity)
	set(&m.RegionCountry, gs.RegionCountry)
	m.DatabaseLimit = int32Ptr(gs.DatabaseLimit)
	m.BackupLimit = int32Ptr(gs.BackupLimit)
	m.AllocationLimit = int32Ptr(gs.AllocationLimit)
	if t := framework.Time(gs.Timestamps.CreateTime); !t.IsNull() || m.CreatedAt.IsUnknown() {
		m.CreatedAt = t
	}
}

// nullUnknowns replaces values the API did not provide, since state cannot hold unknowns.
func nullUnknowns(m *serverModel) {
	for _, s := range []*types.String{&m.RegionID, &m.DockerImage, &m.PowerState, &m.PrimaryIP, &m.NodeID, &m.PanelIdentifier,
		&m.PlanCode, &m.PlanBillingType, &m.ProviderName, &m.RegionCity, &m.RegionCountry, &m.CreatedAt, &m.Phase, &m.LastError,
		&m.GameName, &m.Variant, &m.Icon} {
		if s.IsUnknown() {
			*s = types.StringNull()
		}
	}
	for _, i := range []*types.Int64{&m.PrimaryPort, &m.DatabaseLimit, &m.BackupLimit, &m.AllocationLimit} {
		if i.IsUnknown() {
			*i = types.Int64Null()
		}
	}
}
