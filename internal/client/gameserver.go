package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Game server phases.
const (
	GameServerPhasePending = "GAME_SERVER_PHASE_PENDING"
	GameServerPhaseReady   = "GAME_SERVER_PHASE_READY"
	GameServerPhaseError   = "GAME_SERVER_PHASE_ERROR"
	GameServerPhaseFailed  = "GAME_SERVER_PHASE_FAILED"
)

// IsGameServerDeadlineExceeded reports whether a game-server call timed out on
// the API side. The work may still have been done.
func IsGameServerDeadlineExceeded(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == 4 || e.HTTPStatus == http.StatusGatewayTimeout)
}

// GameServer is a game server.
type GameServer struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	PlanID          string     `json:"planId"`
	TemplateID      string     `json:"templateId"`
	RegionID        string     `json:"regionId"`
	NodeID          string     `json:"nodeId"`
	OrganizationID  string     `json:"organizationId"`
	Icon            string     `json:"icon"`
	Variant         string     `json:"variant"`
	VersionValue    string     `json:"versionValue"`
	AllocationIP    string     `json:"allocationIp"`
	AllocationPort  int32      `json:"allocationPort"`
	Phase           string     `json:"phase"`
	LastError       string     `json:"lastError"`
	PanelIdentifier string     `json:"panelIdentifier"`
	PowerState      string     `json:"powerState"`
	Timestamps      Timestamps `json:"timestamps"`
	ProviderName    string     `json:"providerName"`
	RegionCity      string     `json:"regionCity"`
	RegionCountry   string     `json:"regionCountry"`
	PlanCode        string     `json:"planCode"`
	GameName        string     `json:"gameName"`
	PlanBillingType string     `json:"planBillingType"`
	DatabaseLimit   *int32     `json:"databaseLimit,omitempty"`
	BackupLimit     *int32     `json:"backupLimit,omitempty"`
	AllocationLimit *int32     `json:"allocationLimit,omitempty"`
}

// CreateGameServerRequest orders a game server.
type CreateGameServerRequest struct {
	PlanID         string            `json:"planId"`
	TemplateID     string            `json:"templateId"`
	RegionID       string            `json:"regionId,omitempty"`
	Name           string            `json:"name"`
	VersionValue   string            `json:"versionValue,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	AcceptEULA     bool              `json:"acceptEula"`
}

// GameServerAllocation is one IP and port of a game server.
type GameServerAllocation struct {
	ID        int32  `json:"id"`
	IP        string `json:"ip"`
	Port      int32  `json:"port"`
	Alias     string `json:"alias"`
	Notes     string `json:"notes"`
	IsDefault bool   `json:"isDefault"`
}

// GameServerDatabase is a MySQL database of a game server. Password and JDBC
// are only returned by create and rotate.
type GameServerDatabase struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Username        string `json:"username"`
	Host            string `json:"host"`
	Port            int32  `json:"port"`
	ConnectionsFrom string `json:"connectionsFrom"`
	Password        string `json:"password"`
	JDBC            string `json:"jdbc"`
}

// GameServerScheduleTask is one step of a schedule.
type GameServerScheduleTask struct {
	ID                string `json:"id,omitempty"`
	SequenceID        int32  `json:"sequenceId,omitempty"`
	Action            string `json:"action"`
	Payload           string `json:"payload"`
	TimeOffset        int32  `json:"timeOffset"`
	ContinueOnFailure bool   `json:"continueOnFailure"`
}

// GameServerSchedule is a cron schedule with ordered tasks.
type GameServerSchedule struct {
	ID             string                   `json:"id"`
	Name           string                   `json:"name"`
	Minute         string                   `json:"minute"`
	Hour           string                   `json:"hour"`
	DayOfMonth     string                   `json:"dayOfMonth"`
	Month          string                   `json:"month"`
	DayOfWeek      string                   `json:"dayOfWeek"`
	IsActive       bool                     `json:"isActive"`
	OnlyWhenOnline bool                     `json:"onlyWhenOnline"`
	Tasks          []GameServerScheduleTask `json:"tasks"`
	LastRunAt      string                   `json:"lastRunAt"`
	NextRunAt      string                   `json:"nextRunAt"`
	IsProcessing   bool                     `json:"isProcessing"`
}

// GameServerScheduleInput is the body of create and update schedule calls.
// IsActive is always sent: update treats a missing value as false.
type GameServerScheduleInput struct {
	Name           string `json:"name"`
	Minute         string `json:"minute"`
	Hour           string `json:"hour"`
	DayOfMonth     string `json:"dayOfMonth"`
	Month          string `json:"month"`
	DayOfWeek      string `json:"dayOfWeek"`
	OnlyWhenOnline bool   `json:"onlyWhenOnline"`
	IsActive       bool   `json:"isActive"`
}

// GameServerBackup is a backup of a game server's files.
type GameServerBackup struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	IsSuccessful bool   `json:"isSuccessful"`
	IsLocked     bool   `json:"isLocked"`
	Bytes        Int64  `json:"bytes"`
	CompletedAt  string `json:"completedAt"`
}

// GameServerStartupVariable is a startup variable as the server sees it.
type GameServerStartupVariable struct {
	EnvVariable  string `json:"envVariable"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ServerValue  string `json:"serverValue"`
	UserViewable bool   `json:"userViewable"`
	UserEditable bool   `json:"userEditable"`
	Rules        string `json:"rules"`
}

// GameServerDockerImage is one image the game can run on.
type GameServerDockerImage struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// GameServerStartup is a server's startup command, variables and image.
type GameServerStartup struct {
	StartupCommand    string                      `json:"startupCommand"`
	RawStartupCommand string                      `json:"rawStartupCommand"`
	Variables         []GameServerStartupVariable `json:"variables"`
	DockerImage       string                      `json:"dockerImage"`
	DockerImages      []GameServerDockerImage     `json:"dockerImages"`
}

// GameServerResources is a server's live state and usage.
type GameServerResources struct {
	State          string  `json:"state"`
	CPUAbsolute    float64 `json:"cpuAbsolute"`
	MemoryBytes    Int64   `json:"memoryBytes"`
	DiskBytes      Int64   `json:"diskBytes"`
	NetworkRxBytes Int64   `json:"networkRxBytes"`
	NetworkTxBytes Int64   `json:"networkTxBytes"`
	UptimeMillis   Int64   `json:"uptimeMillis"`
}

// GameVariable describes a game's startup variable.
type GameVariable struct {
	EnvVariable  string `json:"envVariable"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	DefaultValue string `json:"defaultValue"`
	Rules        string `json:"rules"`
	UserViewable bool   `json:"userViewable"`
	UserEditable bool   `json:"userEditable"`
	IsRequired   bool   `json:"isRequired"`
	HelpText     string `json:"helpText"`
	HelpURL      string `json:"helpUrl"`
}

// OrderGame is a game that can be ordered on a plan.
type OrderGame struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Icon      string         `json:"icon"`
	Variables []GameVariable `json:"variables"`
	EULA      *bool          `json:"eula,omitempty"`
}

func gsPath(id string, rest ...string) string {
	p := "/v1/game-servers/" + url.PathEscape(id)
	for _, r := range rest {
		p += r
	}
	return p
}

// --- Servers ---

// ListGameServers returns every game server in the organization. The API has no paging.
func (c *Client) ListGameServers(ctx context.Context) ([]GameServer, error) {
	var out struct {
		GameServers []GameServer `json:"gameServers"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListGameServers", path: "/v1/game-servers",
		query: url.Values{"pageSize": {"-1"}}, retryUnavailable: true}, &out)
	return out.GameServers, err
}

// GetGameServer returns one game server.
func (c *Client) GetGameServer(ctx context.Context, id string) (*GameServer, error) {
	var out GameServer
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetGameServer", path: gsPath(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: "Game server was not found."}
	}
	return &out, nil
}

// CreateGameServer orders a game server. The response is partial; read the server again.
func (c *Client) CreateGameServer(ctx context.Context, in CreateGameServerRequest) (*GameServer, error) {
	var out GameServer
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateGameServer", path: "/v1/game-servers", body: in}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGameServer deletes a game server. Deletion finishes asynchronously.
func (c *Client) DeleteGameServer(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteGameServer", path: gsPath(id)}, nil)
}

// RenameGameServer renames a game server.
func (c *Client) RenameGameServer(ctx context.Context, id, name string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "RenameGameServer", path: gsPath(id, ":rename"),
		body: map[string]string{"name": name}}, nil)
}

// ChangeGameServerGame reinstalls a server with another game. It deletes every
// schedule and backup and wipes all files.
func (c *Client) ChangeGameServerGame(ctx context.Context, id, templateID string, env map[string]string, acceptEULA bool) error {
	body := map[string]any{"templateId": templateID, "acceptEula": acceptEULA}
	if len(env) > 0 {
		body["environment"] = env
	}
	return c.do(ctx, request{method: http.MethodPost, op: "ChangeGameServerGame", path: gsPath(id, ":changeGame"), body: body}, nil)
}

// PowerGameServer sends start, stop, restart or kill.
func (c *Client) PowerGameServer(ctx context.Context, id, signal string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "PowerGameServer", path: gsPath(id, ":power"),
		body: map[string]string{"signal": signal}}, nil)
}

// GetGameServerResources returns the live state and usage.
func (c *Client) GetGameServerResources(ctx context.Context, id string) (*GameServerResources, error) {
	var out GameServerResources
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetGameServerResources", path: gsPath(id, "/resources"), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetGameServerStartup returns the startup command, variables and image.
func (c *Client) GetGameServerStartup(ctx context.Context, id string) (*GameServerStartup, error) {
	var out GameServerStartup
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetStartup", path: gsPath(id, "/startup"), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateGameServerStartupVariable sets one startup variable.
func (c *Client) UpdateGameServerStartupVariable(ctx context.Context, id, key, value string) error {
	return c.do(ctx, request{method: http.MethodPut, op: "UpdateStartupVariable", path: gsPath(id, "/startup"),
		body: map[string]string{"key": key, "value": value}}, nil)
}

// UpdateGameServerDockerImage changes the image the server runs on.
func (c *Client) UpdateGameServerDockerImage(ctx context.Context, id, image string) error {
	return c.do(ctx, request{method: http.MethodPut, op: "UpdateDockerImage", path: gsPath(id, "/docker-image"),
		body: map[string]string{"dockerImage": image}}, nil)
}

// ListOrderGames returns the games that can be ordered on a plan.
func (c *Client) ListOrderGames(ctx context.Context, planID string) ([]OrderGame, error) {
	var out struct {
		Games []OrderGame `json:"games"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListOrderGames", path: "/v1/game-servers:order-games",
		query: url.Values{"planId": {planID}}, retryUnavailable: true}, &out)
	return out.Games, err
}

// --- Allocations ---

// ListGameServerAllocations returns every allocation, the primary included.
func (c *Client) ListGameServerAllocations(ctx context.Context, id string) ([]GameServerAllocation, error) {
	var out struct {
		Allocations []GameServerAllocation `json:"allocations"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListAllocations", path: gsPath(id, "/allocations"), retryUnavailable: true}, &out)
	return out.Allocations, err
}

// CreateGameServerAllocation adds a port. The platform picks the IP and port.
func (c *Client) CreateGameServerAllocation(ctx context.Context, id string) (*GameServerAllocation, error) {
	var out GameServerAllocation
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateAllocation", path: gsPath(id, "/allocations"), body: map[string]any{}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func gsAllocPath(id string, allocationID int32, suffix string) string {
	return gsPath(id, "/allocations/", strconv.Itoa(int(allocationID)), suffix)
}

// UpdateGameServerAllocation changes an allocation's notes.
func (c *Client) UpdateGameServerAllocation(ctx context.Context, id string, allocationID int32, notes string) (*GameServerAllocation, error) {
	var out GameServerAllocation
	if err := c.do(ctx, request{method: http.MethodPost, op: "UpdateAllocation", path: gsAllocPath(id, allocationID, ""),
		body: map[string]string{"notes": notes}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetPrimaryGameServerAllocation makes an allocation the server's primary address.
func (c *Client) SetPrimaryGameServerAllocation(ctx context.Context, id string, allocationID int32) error {
	return c.do(ctx, request{method: http.MethodPost, op: "SetPrimaryAllocation", path: gsAllocPath(id, allocationID, ":primary"), body: map[string]any{}}, nil)
}

// DeleteGameServerAllocation removes an allocation. The primary cannot be removed.
func (c *Client) DeleteGameServerAllocation(ctx context.Context, id string, allocationID int32) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteAllocation", path: gsAllocPath(id, allocationID, "")}, nil)
}

// --- Databases ---

// ListGameServerDatabases returns the server's databases, without passwords.
func (c *Client) ListGameServerDatabases(ctx context.Context, id string) ([]GameServerDatabase, error) {
	var out struct {
		Databases []GameServerDatabase `json:"databases"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListDatabases", path: gsPath(id, "/databases"), retryUnavailable: true}, &out)
	return out.Databases, err
}

// CreateGameServerDatabase creates a database. The response holds the password.
func (c *Client) CreateGameServerDatabase(ctx context.Context, id, name, remote string) (*GameServerDatabase, error) {
	var out GameServerDatabase
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateDatabase", path: gsPath(id, "/databases"),
		body: map[string]string{"database": name, "remote": remote}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateGameServerDatabasePassword sets a new random password and returns it.
func (c *Client) RotateGameServerDatabasePassword(ctx context.Context, id, databaseID string) (*GameServerDatabase, error) {
	var out GameServerDatabase
	if err := c.do(ctx, request{method: http.MethodPost, op: "RotateDatabasePassword",
		path: gsPath(id, "/databases/", url.PathEscape(databaseID), ":rotate"), body: map[string]any{}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGameServerDatabase deletes a database.
func (c *Client) DeleteGameServerDatabase(ctx context.Context, id, databaseID string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteDatabase", path: gsPath(id, "/databases/", url.PathEscape(databaseID))}, nil)
}

// --- Schedules ---

// ListGameServerSchedules returns the server's schedules with their tasks.
func (c *Client) ListGameServerSchedules(ctx context.Context, id string) ([]GameServerSchedule, error) {
	var out struct {
		Schedules []GameServerSchedule `json:"schedules"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListSchedules", path: gsPath(id, "/schedules"), retryUnavailable: true}, &out)
	return out.Schedules, err
}

// CreateGameServerSchedule creates a schedule without tasks.
func (c *Client) CreateGameServerSchedule(ctx context.Context, id string, in GameServerScheduleInput) (*GameServerSchedule, error) {
	var out GameServerSchedule
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateSchedule", path: gsPath(id, "/schedules"), body: in}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateGameServerSchedule replaces a schedule's settings. Tasks are not changed.
func (c *Client) UpdateGameServerSchedule(ctx context.Context, id, scheduleID string, in GameServerScheduleInput) error {
	return c.do(ctx, request{method: http.MethodPost, op: "UpdateSchedule", path: gsPath(id, "/schedules/", url.PathEscape(scheduleID)), body: in}, nil)
}

// DeleteGameServerSchedule deletes a schedule and its tasks.
func (c *Client) DeleteGameServerSchedule(ctx context.Context, id, scheduleID string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteSchedule", path: gsPath(id, "/schedules/", url.PathEscape(scheduleID))}, nil)
}

// CreateGameServerScheduleTask appends a task. The API returns no task ID; list
// the schedules to find it.
func (c *Client) CreateGameServerScheduleTask(ctx context.Context, id, scheduleID string, t GameServerScheduleTask) error {
	body := map[string]any{"action": t.Action, "payload": t.Payload, "timeOffset": t.TimeOffset, "continueOnFailure": t.ContinueOnFailure}
	return c.do(ctx, request{method: http.MethodPost, op: "CreateScheduleTask",
		path: gsPath(id, "/schedules/", url.PathEscape(scheduleID), "/tasks"), body: body}, nil)
}

// UpdateGameServerScheduleTask replaces a task. A zero SequenceID keeps its position.
func (c *Client) UpdateGameServerScheduleTask(ctx context.Context, id, scheduleID string, t GameServerScheduleTask) error {
	body := map[string]any{"action": t.Action, "payload": t.Payload, "timeOffset": t.TimeOffset, "continueOnFailure": t.ContinueOnFailure}
	if t.SequenceID > 0 {
		body["sequenceId"] = t.SequenceID
	}
	return c.do(ctx, request{method: http.MethodPost, op: "UpdateScheduleTask",
		path: gsPath(id, "/schedules/", url.PathEscape(scheduleID), "/tasks/", url.PathEscape(t.ID)), body: body}, nil)
}

// DeleteGameServerScheduleTask deletes a task.
func (c *Client) DeleteGameServerScheduleTask(ctx context.Context, id, scheduleID, taskID string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteScheduleTask",
		path: gsPath(id, "/schedules/", url.PathEscape(scheduleID), "/tasks/", url.PathEscape(taskID))}, nil)
}

// --- Backups ---

// ListGameServerBackups returns the server's backups.
func (c *Client) ListGameServerBackups(ctx context.Context, id string) ([]GameServerBackup, error) {
	var out struct {
		Backups []GameServerBackup `json:"backups"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListBackups", path: gsPath(id, "/backups"), retryUnavailable: true}, &out)
	return out.Backups, err
}

// CreateGameServerBackup starts a backup. It completes asynchronously.
func (c *Client) CreateGameServerBackup(ctx context.Context, id, name string) (*GameServerBackup, error) {
	var out GameServerBackup
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateBackup", path: gsPath(id, "/backups"),
		body: map[string]string{"name": name}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ToggleGameServerBackupLock flips a backup's lock. The API has no "set
// locked"; read isLocked first.
func (c *Client) ToggleGameServerBackupLock(ctx context.Context, id, backupID string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "LockBackup", path: gsPath(id, "/backups/", url.PathEscape(backupID), ":lock"), body: map[string]any{}}, nil)
}

// DeleteGameServerBackup deletes a backup. Locked backups must be unlocked first.
func (c *Client) DeleteGameServerBackup(ctx context.Context, id, backupID string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteBackup", path: gsPath(id, "/backups/", url.PathEscape(backupID))}, nil)
}
