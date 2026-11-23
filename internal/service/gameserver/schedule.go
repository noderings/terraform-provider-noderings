package gameserver

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/noderings/terraform-provider-noderings/internal/client"
	"github.com/noderings/terraform-provider-noderings/internal/framework"
)

var cronFieldRE = regexp.MustCompile(`^[0-9*,/\-A-Za-z]{1,64}$`)

var (
	_ resource.Resource                   = &scheduleResource{}
	_ resource.ResourceWithImportState    = &scheduleResource{}
	_ resource.ResourceWithValidateConfig = &scheduleResource{}
	_ resource.ResourceWithModifyPlan     = &scheduleResource{}
)

// NewScheduleResource returns the noderings_game_server_schedule resource.
func NewScheduleResource() resource.Resource { return &scheduleResource{} }

type scheduleResource struct{ subResource }

type scheduleModel struct {
	ID             types.String `tfsdk:"id"`
	GameServerID   types.String `tfsdk:"game_server_id"`
	ScheduleID     types.String `tfsdk:"schedule_id"`
	Name           types.String `tfsdk:"name"`
	Cron           types.Object `tfsdk:"cron"`
	Active         types.Bool   `tfsdk:"active"`
	OnlyWhenOnline types.Bool   `tfsdk:"only_when_online"`
	Tasks          []taskModel  `tfsdk:"task"`
	LastRunAt      types.String `tfsdk:"last_run_at"`
	NextRunAt      types.String `tfsdk:"next_run_at"`
}

type cronModel struct {
	Minute     types.String `tfsdk:"minute"`
	Hour       types.String `tfsdk:"hour"`
	DayOfMonth types.String `tfsdk:"day_of_month"`
	Month      types.String `tfsdk:"month"`
	DayOfWeek  types.String `tfsdk:"day_of_week"`
}

var cronAttrTypes = map[string]attr.Type{
	"minute": types.StringType, "hour": types.StringType, "day_of_month": types.StringType, "month": types.StringType, "day_of_week": types.StringType,
}

type taskModel struct {
	ID                types.String `tfsdk:"id"`
	SequenceID        types.Int64  `tfsdk:"sequence_id"`
	Action            types.String `tfsdk:"action"`
	Payload           types.String `tfsdk:"payload"`
	TimeOffset        types.Int64  `tfsdk:"time_offset"`
	ContinueOnFailure types.Bool   `tfsdk:"continue_on_failure"`
}

func (r *scheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_game_server_schedule"
}

func (r *scheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	cronField := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional: true, Computed: true, Default: stringdefault.StaticString("*"),
			MarkdownDescription: desc + " Cron syntax; defaults to `*`.",
			Validators:          []validator.String{stringvalidator.RegexMatches(cronFieldRE, "must be a cron field such as *, 0, */15 or 1-5")},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A schedule that runs tasks on a game server at set times, for example a nightly restart or backup. " +
			"Tasks run in the order they are written. Changing the server's game deletes all its schedules; Terraform then creates this one again. " +
			"The game server must be ready.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute(),
			"game_server_id": serverIDAttribute(),
			"schedule_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Schedule ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Schedule name.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 255), framework.SafeString()},
			},
			"cron": schema.SingleNestedAttribute{
				Required: true, MarkdownDescription: "When the schedule runs, in the server's time zone.",
				Attributes: map[string]schema.Attribute{
					"minute":       cronField("Minute."),
					"hour":         cronField("Hour."),
					"day_of_month": cronField("Day of the month."),
					"month":        cronField("Month."),
					"day_of_week":  cronField("Day of the week (0 or 7 is Sunday)."),
				},
			},
			"active": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the schedule runs. Defaults to `true`.",
			},
			"only_when_online": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Run only while the server is running. Defaults to `false`.",
			},
			"last_run_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the schedule last ran."},
			"next_run_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the schedule runs next."},
		},
		Blocks: map[string]schema.Block{
			"task": schema.ListNestedBlock{
				MarkdownDescription: "A step of the schedule. Tasks run in the order written; `time_offset` delays a task after the previous one.",
				Validators:          []validator.List{listvalidator.SizeAtMost(10)},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Task ID."},
						"sequence_id": schema.Int64Attribute{Computed: true, MarkdownDescription: "Position, starting at 1."},
						"action": schema.StringAttribute{
							Required: true, MarkdownDescription: "`command` (send a console command), `power` (start, stop, restart or kill) or `backup` (create a backup).",
							Validators: []validator.String{stringvalidator.OneOf("command", "power", "backup")},
						},
						"payload": schema.StringAttribute{
							Optional: true, Computed: true, Default: stringdefault.StaticString(""),
							MarkdownDescription: "For `command`, the command to send. For `power`, one of `start`, `stop`, `restart` or `kill`. " +
								"For `backup`, files to leave out of the backup, one pattern per line (optional).",
							Validators: []validator.String{framework.SafeString()},
						},
						"time_offset": schema.Int64Attribute{
							Optional: true, Computed: true, Default: int64default.StaticInt64(0),
							MarkdownDescription: "Seconds to wait after the previous task, 0 to 900. Defaults to `0`.",
							Validators:          []validator.Int64{int64validator.Between(0, 900)},
						},
						"continue_on_failure": schema.BoolAttribute{
							Optional: true, Computed: true, Default: booldefault.StaticBool(false),
							MarkdownDescription: "Run the next tasks even if this one fails. Defaults to `false`.",
						},
					},
				},
			},
		},
	}
}

func (r *scheduleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var tasks []taskModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("task"), &tasks)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for i, t := range tasks {
		if t.Action.IsUnknown() || t.Payload.IsUnknown() {
			continue
		}
		p := path.Root("task").AtListIndex(i).AtName("payload")
		switch t.Action.ValueString() {
		case "command":
			if t.Payload.ValueString() == "" {
				resp.Diagnostics.AddAttributeError(p, "Missing command", "A command task needs the command to send in payload.")
			}
		case "power":
			switch t.Payload.ValueString() {
			case "start", "stop", "restart", "kill":
			default:
				resp.Diagnostics.AddAttributeError(p, "Invalid power action", "A power task's payload must be start, stop, restart or kill.")
			}
		}
	}
}

// ModifyPlan predicts task IDs and positions. Tasks are reconciled by
// position, so a task keeps the ID of the task that was at its position;
// only appended tasks get new IDs.
func (r *scheduleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, state []taskModel
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("task"), &plan)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("task"), &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	for i := range plan {
		plan[i].ID = types.StringUnknown()
		if i < len(state) && !state[i].ID.IsNull() && !state[i].ID.IsUnknown() {
			plan[i].ID = state[i].ID
		}
		plan[i].SequenceID = types.Int64Value(int64(i + 1))
	}
	if plan == nil {
		plan = []taskModel{}
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("task"), plan)...)
}

func (m *scheduleModel) input(ctx context.Context, diags *diag.Diagnostics) client.GameServerScheduleInput {
	var c cronModel
	diags.Append(m.Cron.As(ctx, &c, basetypes.ObjectAsOptions{})...)
	star := func(v types.String) string {
		if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
			return "*"
		}
		return v.ValueString()
	}
	return client.GameServerScheduleInput{
		Name: m.Name.ValueString(), Minute: star(c.Minute), Hour: star(c.Hour), DayOfMonth: star(c.DayOfMonth),
		Month: star(c.Month), DayOfWeek: star(c.DayOfWeek), OnlyWhenOnline: m.OnlyWhenOnline.ValueBool(), IsActive: m.Active.ValueBool(),
	}
}

func (r *scheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan scheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server := plan.GameServerID.ValueString()
	in := plan.input(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	sched, err := r.client.CreateGameServerSchedule(ctx, server, in)
	invalidate(r.client, server, "schedules")
	if err != nil && client.IsGameServerDeadlineExceeded(err) {
		sched = adoptSchedule(ctx, r.client, server, in.Name)
		if sched != nil {
			err = nil
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating game server schedule", explainNotReady(ctx, r.client, server, err))
		return
	}
	plan.ScheduleID = types.StringValue(sched.ID)
	plan.ID = types.StringValue(server + "/" + sched.ID)
	terr := r.reconcileTasks(ctx, server, sched.ID, nil, plan.Tasks)
	invalidate(r.client, server, "schedules")
	found := r.refresh(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("Schedule not found after creating it", "Schedule "+plan.ID.ValueString()+" was created but does not appear in the server's schedules.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if terr != nil {
		resp.Diagnostics.AddError("Error creating schedule tasks", terr.Error())
	}
}

func adoptSchedule(ctx context.Context, c *client.Client, server, name string) *client.GameServerSchedule {
	list, err := c.ListGameServerSchedules(ctx, server)
	if err != nil {
		return nil
	}
	var found *client.GameServerSchedule
	best := -1
	for i := range list {
		n, _ := strconv.Atoi(list[i].ID)
		if list[i].Name == name && len(list[i].Tasks) == 0 && n > best {
			found, best = &list[i], n
		}
	}
	return found
}

// reconcileTasks makes the schedule's tasks match want, position by
// position: surplus tasks are deleted, existing positions updated, and
// missing ones appended. Task IDs are not returned on create, so the caller
// lists the schedule afterwards.
func (r *scheduleResource) reconcileTasks(ctx context.Context, server, scheduleID string, have []client.GameServerScheduleTask, want []taskModel) error {
	sort.Slice(have, func(i, j int) bool { return have[i].SequenceID < have[j].SequenceID })
	for i := len(have) - 1; i >= len(want); i-- {
		if err := r.client.DeleteGameServerScheduleTask(ctx, server, scheduleID, have[i].ID); err != nil {
			return fmt.Errorf("deleting task %d: %w", i+1, err)
		}
	}
	for i, w := range want {
		t := client.GameServerScheduleTask{Action: w.Action.ValueString(), Payload: w.Payload.ValueString(),
			TimeOffset: int32(w.TimeOffset.ValueInt64()), ContinueOnFailure: w.ContinueOnFailure.ValueBool()}
		if i < len(have) {
			h := have[i]
			if h.Action == t.Action && h.Payload == t.Payload && h.TimeOffset == t.TimeOffset && h.ContinueOnFailure == t.ContinueOnFailure && int(h.SequenceID) == i+1 {
				continue
			}
			t.ID, t.SequenceID = h.ID, int32(i+1)
			if err := r.client.UpdateGameServerScheduleTask(ctx, server, scheduleID, t); err != nil {
				return fmt.Errorf("updating task %d: %w", i+1, err)
			}
			continue
		}
		if err := r.client.CreateGameServerScheduleTask(ctx, server, scheduleID, t); err != nil {
			return fmt.Errorf("creating task %d: %w", i+1, err)
		}
	}
	return nil
}

// refresh reads the schedule from the (cached) list. It returns false if the schedule is gone.
func (r *scheduleResource) refresh(ctx context.Context, m *scheduleModel, diags *diag.Diagnostics) bool {
	list, err := listSchedules(ctx, r.client, m.GameServerID.ValueString())
	if err != nil {
		diags.AddError("Error reading game server schedules", err.Error())
		return false
	}
	for i := range list {
		if list[i].ID == m.ScheduleID.ValueString() {
			setSchedule(m, &list[i], diags)
			return true
		}
	}
	return false
}

func setSchedule(m *scheduleModel, s *client.GameServerSchedule, diags *diag.Diagnostics) {
	m.ScheduleID = types.StringValue(s.ID)
	m.ID = types.StringValue(m.GameServerID.ValueString() + "/" + s.ID)
	m.Name = types.StringValue(s.Name)
	star := func(v string) attr.Value {
		if v == "" {
			v = "*"
		}
		return types.StringValue(v)
	}
	cron, d := types.ObjectValue(cronAttrTypes, map[string]attr.Value{
		"minute": star(s.Minute), "hour": star(s.Hour), "day_of_month": star(s.DayOfMonth), "month": star(s.Month), "day_of_week": star(s.DayOfWeek),
	})
	diags.Append(d...)
	m.Cron = cron
	m.Active = types.BoolValue(s.IsActive)
	m.OnlyWhenOnline = types.BoolValue(s.OnlyWhenOnline)
	tasks := append([]client.GameServerScheduleTask(nil), s.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].SequenceID < tasks[j].SequenceID })
	m.Tasks = []taskModel{}
	for _, t := range tasks {
		m.Tasks = append(m.Tasks, taskModel{
			ID: types.StringValue(t.ID), SequenceID: types.Int64Value(int64(t.SequenceID)), Action: types.StringValue(t.Action),
			Payload: types.StringValue(t.Payload), TimeOffset: types.Int64Value(int64(t.TimeOffset)), ContinueOnFailure: types.BoolValue(t.ContinueOnFailure),
		})
	}
	m.LastRunAt = types.StringValue(s.LastRunAt)
	m.NextRunAt = types.StringValue(s.NextRunAt)
}

func (r *scheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state scheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, ok := readSub(ctx, r.client, state.GameServerID.ValueString(), "schedules", listSchedules, &resp.Diagnostics, func() { resp.State.RemoveResource(ctx) })
	if !ok {
		return
	}
	for i := range list {
		if list[i].ID == state.ScheduleID.ValueString() {
			setSchedule(&state, &list[i], &resp.Diagnostics)
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *scheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state scheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server, id := state.GameServerID.ValueString(), state.ScheduleID.ValueString()
	in := plan.input(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// The update replaces every setting; isActive is always sent because a
	// missing value would turn the schedule off.
	err := r.client.UpdateGameServerSchedule(ctx, server, id, in)
	invalidate(r.client, server, "schedules")
	if err != nil {
		resp.Diagnostics.AddError("Error updating game server schedule", explainNotReady(ctx, r.client, server, err))
		return
	}
	list, err := r.client.ListGameServerSchedules(ctx, server)
	if err != nil {
		resp.Diagnostics.AddError("Error reading game server schedules", err.Error())
		return
	}
	var have []client.GameServerScheduleTask
	for _, s := range list {
		if s.ID == id {
			have = s.Tasks
		}
	}
	terr := r.reconcileTasks(ctx, server, id, have, plan.Tasks)
	invalidate(r.client, server, "schedules")
	plan.ScheduleID = state.ScheduleID
	if !r.refresh(ctx, &plan, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Schedule disappeared", "Schedule "+state.ID.ValueString()+" was deleted while it was being updated.")
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if terr != nil {
		resp.Diagnostics.AddError("Error updating schedule tasks", terr.Error())
	}
}

func (r *scheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state scheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server, id := state.GameServerID.ValueString(), state.ScheduleID.ValueString()
	err := deleteSub(ctx, r.client, server, "schedules",
		func() error { return r.client.DeleteGameServerSchedule(ctx, server, id) },
		listSchedules, func(s client.GameServerSchedule) bool { return s.ID == id })
	if err != nil {
		resp.Diagnostics.AddError("Error deleting game server schedule", err.Error())
	}
}

func (r *scheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if obj, ok := subImport(ctx, req, resp, "schedule"); ok {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("schedule_id"), obj)...)
	}
}
