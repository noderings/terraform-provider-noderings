package fakeapi

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GameTemplateVanillaID is a second game on GamePlanID, on the same platform
// as GameTemplateID, so tests can change a server's game.
const GameTemplateVanillaID = "8d4b0000-0000-4000-8000-000000000002"

// Phases of a game server.
const (
	GamePhasePending = "GAME_SERVER_PHASE_PENDING"
	GamePhaseReady   = "GAME_SERVER_PHASE_READY"
	GamePhaseError   = "GAME_SERVER_PHASE_ERROR"
	GamePhaseFailed  = "GAME_SERVER_PHASE_FAILED"
)

// Feature limits of GamePlanID.
const (
	gsAllocationLimit = 2
	gsDatabaseLimit   = 2
	gsBackupLimit     = 3
)

type gsVar struct {
	Env, Name, Default, Rules string
	Editable, Required        bool
}

type gsTemplate struct {
	ID, Name, Icon, Game, Variant, VersionVar string
	EULA                                      bool
	Vars                                      []gsVar
	Images                                    []map[string]string
	Orderable                                 bool
}

var gsTemplates = []*gsTemplate{
	{
		ID: GameTemplateID, Name: GameTemplateName, Icon: "minecraft", Game: "Minecraft", Variant: "Paper", VersionVar: "MINECRAFT_VERSION",
		EULA: true, Orderable: true,
		Vars: []gsVar{
			{Env: "MINECRAFT_VERSION", Name: "Minecraft Version", Default: "latest", Rules: "required|string|max:20", Editable: true, Required: true},
			{Env: "SERVER_JARFILE", Name: "Server Jar File", Default: "server.jar", Rules: "required|string|max:40", Required: true},
			{Env: "BUILD_NUMBER", Name: "Build Number", Default: "latest", Rules: "required|string|max:20", Editable: true, Required: true},
			{Env: "MAX_PLAYERS", Name: "Max Players", Default: "20", Rules: "required|integer|max:1000", Editable: true, Required: true},
			{Env: "MOTD", Name: "Message of the Day", Default: "", Rules: "nullable|string|max:60", Editable: true},
		},
		Images: []map[string]string{
			{"name": "Java 21", "image": "ghcr.io/example/games:java_21"},
			{"name": "Java 17", "image": "ghcr.io/example/games:java_17"},
		},
	},
	{
		ID: GameTemplateVanillaID, Name: "Minecraft · Vanilla", Icon: "minecraft", Game: "Minecraft", Variant: "Vanilla", VersionVar: "VANILLA_VERSION",
		EULA: true, Orderable: true,
		Vars: []gsVar{
			{Env: "VANILLA_VERSION", Name: "Server Version", Default: "latest", Rules: "required|string|max:20", Editable: true, Required: true},
			{Env: "SERVER_JARFILE", Name: "Server Jar File", Default: "server.jar", Rules: "required|string|max:40", Required: true},
		},
		Images: []map[string]string{{"name": "Java 21", "image": "ghcr.io/example/games:java_21"}},
	},
}

func gsTemplateByID(id string) *gsTemplate {
	for _, t := range gsTemplates {
		if t.ID == id {
			return t
		}
	}
	return nil
}

type gsAlloc struct {
	ID      int
	IP      string
	Port    int
	Notes   string
	Default bool
}

type gsDB struct {
	ID, Name, Username, Remote, Password string
}

type gsTask struct {
	ID         int
	Seq        int
	Action     string
	Payload    string
	TimeOffset int
	Continue   bool
}

type gsSchedule struct {
	ID                                       int
	Name                                     string
	Minute, Hour, DayOfMonth, Month, DayOfWk string
	Active, OnlyWhenOnline                   bool
	Tasks                                    []*gsTask
}

type gsBackup struct {
	UUID        string
	Name        string
	Locked      bool
	Successful  bool
	CompletedAt string
	Bytes       int64
	pendingGets int
}

type gsServer struct {
	ID, Name, PlanID, TemplateID, RegionID, VersionValue string
	Num                                                  int
	Phase, LastError                                     string
	IdempotencyKey                                       string
	Env                                                  map[string]string
	DockerImage                                          string
	PowerState                                           string // last observed, as the API stores it
	live                                                 string // live state: running, offline, starting, stopping
	pendingGets                                          int
	outcome, outcomeErr                                  string
	deleteGets                                           int // -1 while not deleting
	AllocIP                                              string
	AllocPort                                            int
	Allocs                                               []*gsAlloc
	DBs                                                  []*gsDB
	Schedules                                            []*gsSchedule
	Backups                                              []*gsBackup
	created                                              int
}

type gsState struct {
	servers    map[string]*gsServer
	seq        int
	readyAfter int
	outcome    string
	outcomeErr string
}

func (s *Server) gs() *gsState {
	return state(s, "gameserver", func() *gsState {
		return &gsState{servers: map[string]*gsServer{}, readyAfter: 2, outcome: GamePhaseReady}
	})
}

func (st *gsState) next() int {
	st.seq++
	return st.seq
}

func init() {
	register(func(s *Server, w http.ResponseWriter, r *http.Request, p []string) bool {
		if len(p) < 2 || p[0] != "v1" || !strings.HasPrefix(p[1], "game-servers") || strings.HasPrefix(p[1], "game-server-templates") {
			return false
		}
		s.gameServers(w, r, p)
		return true
	})
}

// --- out-of-band helpers ---

// SetGameServerInstallOutcome makes later creates and game changes finish in
// the given phase (GamePhaseReady, GamePhaseError or GamePhaseFailed) after
// readyAfter GETs. An empty phase leaves the server pending forever.
func (s *Server) SetGameServerInstallOutcome(phase, lastError string, readyAfter int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.gs()
	st.outcome, st.outcomeErr, st.readyAfter = phase, lastError, readyAfter
}

// SetGameServerPhase forces a server's phase, as if an install failed or a
// reinstall started outside Terraform.
func (s *Server) SetGameServerPhase(id, phase, lastError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[id]; g != nil {
		g.Phase, g.LastError, g.pendingGets, g.outcome = phase, lastError, 0, ""
	}
}

// DeleteGameServerOutOfBand removes a server immediately.
func (s *Server) DeleteGameServerOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.gs().servers, id)
}

// RenameGameServerOutOfBand renames a server as if done in the console.
func (s *Server) RenameGameServerOutOfBand(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[id]; g != nil {
		g.Name = name
	}
}

// SetGameServerVariableOutOfBand changes a startup variable as if done in the console.
func (s *Server) SetGameServerVariableOutOfBand(id, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[id]; g != nil {
		g.Env[key] = value
	}
}

// SetGameServerPowerOutOfBand sets the live power state ("running" or "offline").
func (s *Server) SetGameServerPowerOutOfBand(id, live string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[id]; g != nil {
		g.live = live
	}
}

// GameServerVariable returns a startup variable's current value.
func (s *Server) GameServerVariable(id, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[id]; g != nil {
		return g.Env[key]
	}
	return ""
}

// DeleteGameServerObjectOutOfBand removes an allocation, database, schedule or
// backup ("allocation", "database", "schedule", "backup") by ID.
func (s *Server) DeleteGameServerObjectOutOfBand(serverID, kind, objectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.gs().servers[serverID]
	if g == nil {
		return
	}
	switch kind {
	case "allocation":
		g.Allocs = gsFilter(g.Allocs, func(a *gsAlloc) bool { return strconv.Itoa(a.ID) != objectID })
	case "database":
		g.DBs = gsFilter(g.DBs, func(d *gsDB) bool { return d.ID != objectID })
	case "schedule":
		g.Schedules = gsFilter(g.Schedules, func(x *gsSchedule) bool { return strconv.Itoa(x.ID) != objectID })
	case "backup":
		g.Backups = gsFilter(g.Backups, func(b *gsBackup) bool { return b.UUID != objectID })
	}
}

// SetAllocationNotesOutOfBand changes an allocation's notes.
func (s *Server) SetAllocationNotesOutOfBand(serverID, allocationID, notes string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[serverID]; g != nil {
		for _, a := range g.Allocs {
			if strconv.Itoa(a.ID) == allocationID {
				a.Notes = notes
			}
		}
	}
}

// SetScheduleActiveOutOfBand turns a schedule on or off.
func (s *Server) SetScheduleActiveOutOfBand(serverID, scheduleID string, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[serverID]; g != nil {
		for _, x := range g.Schedules {
			if strconv.Itoa(x.ID) == scheduleID {
				x.Active = active
			}
		}
	}
}

// SetBackupLockedOutOfBand locks or unlocks a backup.
func (s *Server) SetBackupLockedOutOfBand(serverID, backupID string, locked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[serverID]; g != nil {
		for _, b := range g.Backups {
			if b.UUID == backupID {
				b.Locked = locked
			}
		}
	}
}

// GameServerBackupLocked reports whether a backup is locked, and whether it exists.
func (s *Server) GameServerBackupLocked(serverID, backupID string) (locked, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gs().servers[serverID]; g != nil {
		for _, b := range g.Backups {
			if b.UUID == backupID {
				return b.Locked, true
			}
		}
	}
	return false, false
}

func gsFilter[T any](in []T, keep func(T) bool) []T {
	out := in[:0:0]
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

// --- routing ---

var gsUUIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Server) gameServers(w http.ResponseWriter, r *http.Request, p []string) {
	st := s.gs()
	if len(p) == 2 {
		switch {
		case p[1] == "game-servers:order-games" && r.Method == http.MethodGet:
			s.gsOrderGames(w, r)
		case p[1] == "game-servers" && r.Method == http.MethodGet:
			list := []any{}
			var ids []*gsServer
			for _, g := range st.servers {
				if g.deleteGets < 0 {
					ids = append(ids, g)
				}
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i].created > ids[j].created })
			for _, g := range ids {
				v := g.view(false)
				v["organizationId"] = "" // the real list leaves it empty
				list = append(list, v)
			}
			writeJSON(w, 200, map[string]any{"gameServers": list})
		case p[1] == "game-servers" && r.Method == http.MethodPost:
			s.gsCreate(w, r, st)
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}

	id, verb, _ := strings.Cut(p[2], ":")
	g := st.servers[id]
	if g == nil || g.deleteGets == 0 {
		notFound(w, "Game server")
		return
	}
	if len(p) == 3 {
		switch {
		case verb == "" && r.Method == http.MethodGet:
			s.gsGet(w, g)
		case verb == "" && r.Method == http.MethodDelete:
			if g.deleteGets < 0 {
				g.deleteGets = 1 // the next GET still returns it, then it is gone
			}
			writeJSON(w, 200, map[string]any{"success": true})
		case verb == "rename" && r.Method == http.MethodPost:
			var in struct{ Name string }
			if !decode(w, r, &in) {
				return
			}
			if !gsReady(w, g) {
				return
			}
			name := strings.TrimSpace(in.Name)
			if name == "" || len(name) > 64 {
				apiError(w, 400, 3, "Enter a server name.")
				return
			}
			g.Name = name
			writeJSON(w, 200, g.partial())
		case verb == "power" && r.Method == http.MethodPost:
			var in struct{ Signal string }
			if !decode(w, r, &in) {
				return
			}
			if !gsReady(w, g) {
				return
			}
			switch in.Signal {
			case "start", "restart":
				g.live = "starting"
			case "stop":
				g.live = "stopping"
			case "kill":
				g.live = "offline"
			default:
				apiError(w, 400, 3, "signal: value must be in list [start, stop, restart, kill]")
				return
			}
			v := g.partial()
			v["powerState"] = in.Signal
			writeJSON(w, 200, v)
		case verb == "changeGame" && r.Method == http.MethodPost:
			s.gsChangeGame(w, r, st, g)
		case verb == "reinstall" && r.Method == http.MethodPost:
			if g.Phase != GamePhaseReady && g.Phase != GamePhaseError {
				apiError(w, 400, 9, "The game server is not ready.")
				return
			}
			g.startInstall(st)
			writeJSON(w, 200, g.partial())
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}

	if !gsReady(w, g) {
		return
	}
	switch p[3] {
	case "startup":
		s.gsStartup(w, r, g)
	case "docker-image":
		var in struct{ DockerImage string }
		if !decode(w, r, &in) {
			return
		}
		if in.DockerImage == "" || strings.ContainsAny(in.DockerImage, " \t\n") || len(in.DockerImage) > 512 {
			apiError(w, 400, 3, "docker_image: value must be 1-512 characters without whitespace")
			return
		}
		ok := false
		for _, img := range gsTemplateByID(g.TemplateID).Images {
			if img["image"] == in.DockerImage {
				ok = true
			}
		}
		if !ok {
			apiError(w, 400, 9, "The selected Docker image is not allowed for this server.")
			return
		}
		g.DockerImage = in.DockerImage
		writeJSON(w, 200, map[string]any{"success": true})
	case "resources":
		live := g.live
		switch live {
		case "starting":
			g.live = "running"
		case "stopping":
			g.live = "offline"
		}
		switch live {
		case "running", "starting", "stopping", "offline":
			g.PowerState = live
		}
		up := "0"
		if live == "running" {
			up = "3600000"
		}
		writeJSON(w, 200, map[string]any{"state": live, "cpuAbsolute": 12.5, "memoryBytes": "1073741824", "diskBytes": "2147483648",
			"networkRxBytes": "1000", "networkTxBytes": "2000", "uptimeMillis": up})
	case "allocations":
		s.gsAllocations(w, r, p, st, g)
	case "databases":
		s.gsDatabases(w, r, p, st, g)
	case "schedules":
		s.gsSchedules(w, r, p, st, g)
	case "backups":
		s.gsBackups(w, r, p, g)
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

// gsReady is the API's gate for every sub-object call.
func gsReady(w http.ResponseWriter, g *gsServer) bool {
	if g.Phase != GamePhaseReady {
		apiError(w, 400, 9, "The game server is not ready.")
		return false
	}
	return true
}

// gsMissing is what the API returns for a sub-object that does not exist.
func gsMissing(w http.ResponseWriter) {
	apiError(w, 400, 9, "The requested resource does not exist on this server.")
}

func (g *gsServer) startInstall(st *gsState) {
	g.Phase, g.LastError = GamePhasePending, ""
	g.pendingGets = st.readyAfter
	g.outcome, g.outcomeErr = st.outcome, st.outcomeErr
}

func (g *gsServer) view(partial bool) map[string]any {
	t := gsTemplateByID(g.TemplateID)
	v := map[string]any{
		"id": g.ID, "name": g.Name, "planId": g.PlanID, "templateId": g.TemplateID, "regionId": RegionFrankfurtID,
		"nodeId": "9a000000-0000-4000-8000-000000000001", "organizationId": OrgID, "icon": t.Icon, "variant": t.Variant,
		"versionValue": g.VersionValue, "allocationIp": g.AllocIP, "allocationPort": g.AllocPort, "allocationAlias": "",
		"phase": g.Phase, "lastError": g.LastError, "panelIdentifier": "", "powerState": g.PowerState,
		"timestamps":   map[string]any{"createTime": now()},
		"providerName": ProviderName, "regionCity": "Frankfurt", "regionCountry": "Germany", "planCode": GamePlanCode,
		"gameName": t.Game, "planBillingType": "BILLING_TYPE_USAGE",
		"databaseLimit": gsDatabaseLimit, "backupLimit": gsBackupLimit, "allocationLimit": gsAllocationLimit,
	}
	if g.Phase == GamePhaseReady || g.Phase == GamePhaseError || g.AllocIP != "" {
		v["panelIdentifier"] = fmt.Sprintf("%08x", g.Num)
	}
	if partial {
		// Mutation responses leave out what comes from other records.
		v["regionCity"], v["regionCountry"], v["providerName"], v["organizationId"] = "", "", "", ""
		if g.RegionID == "" {
			v["regionId"] = ""
		}
	}
	return v
}

func (g *gsServer) partial() map[string]any { return g.view(true) }

func (s *Server) gsGet(w http.ResponseWriter, g *gsServer) {
	if g.deleteGets > 0 {
		g.deleteGets--
	}
	if g.Phase == GamePhasePending && g.outcome != "" {
		if g.pendingGets > 0 {
			g.pendingGets--
		}
		if g.pendingGets == 0 {
			g.Phase, g.LastError = g.outcome, g.outcomeErr
			if g.Phase == GamePhaseReady {
				// The install watcher records the primary address and the
				// server starts once installed.
				for _, a := range g.Allocs {
					if a.Default {
						g.AllocIP, g.AllocPort = a.IP, a.Port
					}
				}
				g.live = "starting"
			}
		}
	}
	writeJSON(w, 200, g.view(false))
}

func (s *Server) gsOrderGames(w http.ResponseWriter, r *http.Request) {
	plan := r.URL.Query().Get("planId")
	if !gsUUIDRE.MatchString(plan) {
		apiError(w, 400, 3, "plan_id: value must be a valid UUID")
		return
	}
	if plan != GamePlanID {
		apiError(w, 400, 3, "That plan is not a game server plan.")
		return
	}
	games := []any{}
	for _, t := range gsTemplates {
		if !t.Orderable {
			continue
		}
		vars := []any{}
		for _, v := range t.Vars {
			vars = append(vars, map[string]any{"envVariable": v.Env, "name": v.Name, "description": v.Name + ".", "defaultValue": v.Default,
				"rules": v.Rules, "userViewable": true, "userEditable": v.Editable, "isRequired": v.Required, "helpText": "", "helpUrl": ""})
		}
		games = append(games, map[string]any{"id": t.ID, "name": t.Name, "icon": t.Icon, "variables": vars, "eula": t.EULA})
	}
	writeJSON(w, 200, map[string]any{"games": games})
}

// gsResolveEnv follows the API: start from defaults, reject unknown keys and
// changed non-editable keys, ignore empty values for keys with a default, and
// require required variables.
func gsResolveEnv(t *gsTemplate, in map[string]string) (map[string]string, string) {
	env := map[string]string{}
	for _, v := range t.Vars {
		env[v.Env] = v.Default
	}
	for k, val := range in {
		var def *gsVar
		for i := range t.Vars {
			if t.Vars[i].Env == k {
				def = &t.Vars[i]
			}
		}
		if def == nil {
			return nil, fmt.Sprintf("Unknown variable %s.", k)
		}
		if !def.Editable && val != def.Default {
			return nil, fmt.Sprintf("Variable %s cannot be changed.", k)
		}
		if len(val) > 4096 {
			return nil, fmt.Sprintf("Variable %s is too long.", k)
		}
		if val == "" && def.Default != "" {
			continue
		}
		env[k] = val
	}
	for _, v := range t.Vars {
		if v.Required && env[v.Env] == "" {
			return nil, fmt.Sprintf("Variable %s is required.", v.Env)
		}
	}
	return env, ""
}

func (s *Server) gsCreate(w http.ResponseWriter, r *http.Request, st *gsState) {
	var in struct {
		PlanID, TemplateID, RegionID, VersionValue, Name, IdempotencyKey string
		Environment                                                      map[string]string
		AcceptEula                                                       bool
	}
	if !decode(w, r, &in) {
		return
	}
	if !gsUUIDRE.MatchString(in.PlanID) || !gsUUIDRE.MatchString(in.TemplateID) || (in.RegionID != "" && !gsUUIDRE.MatchString(in.RegionID)) {
		apiError(w, 400, 3, "plan_id, template_id and region_id must be valid UUIDs")
		return
	}
	if len(in.VersionValue) > 64 || len(in.IdempotencyKey) > 128 {
		apiError(w, 400, 3, "version_value or idempotency_key is too long")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		apiError(w, 400, 3, "Enter a server name.")
		return
	}
	if in.PlanID != GamePlanID {
		apiError(w, 400, 3, "That plan is not a game server plan.")
		return
	}
	t := gsTemplateByID(in.TemplateID)
	if t == nil || !t.Orderable {
		apiError(w, 400, 3, "That game is not for sale.")
		return
	}
	if t.EULA && !in.AcceptEula {
		apiError(w, 400, 9, "Accept the game's EULA to order this server.")
		return
	}
	env, msg := gsResolveEnv(t, in.Environment)
	if msg != "" {
		apiError(w, 400, 3, msg)
		return
	}
	if in.VersionValue != "" {
		if _, set := in.Environment[t.VersionVar]; !set {
			env[t.VersionVar] = in.VersionValue
		}
	}
	if in.RegionID != "" && in.RegionID != RegionFrankfurtID {
		apiError(w, 400, 9, "No node is available for that game.")
		return
	}
	// Usage plans need an idempotency key, and a repeated key returns the same server.
	if in.IdempotencyKey == "" {
		apiError(w, 400, 3, "Idempotency key is required.")
		return
	}
	for _, g := range st.servers {
		if g.IdempotencyKey == in.IdempotencyKey {
			writeJSON(w, 200, g.partial())
			return
		}
	}
	num := st.next()
	g := &gsServer{
		ID: s.id(), Name: name, PlanID: in.PlanID, TemplateID: in.TemplateID, RegionID: in.RegionID, VersionValue: in.VersionValue,
		Num: num, IdempotencyKey: in.IdempotencyKey, Env: env, DockerImage: t.Images[0]["image"], live: "offline",
		deleteGets: -1, created: num,
	}
	g.Allocs = []*gsAlloc{{ID: 1000 + num*10, IP: "192.0.2.10", Port: 25565 + num*10, Default: true}}
	g.startInstall(st)
	st.servers[g.ID] = g
	writeJSON(w, 200, g.partial())
}

func (s *Server) gsChangeGame(w http.ResponseWriter, r *http.Request, st *gsState, g *gsServer) {
	var in struct {
		TemplateID  string
		Environment map[string]string
		AcceptEula  bool
	}
	if !decode(w, r, &in) {
		return
	}
	if g.Phase != GamePhaseReady && g.Phase != GamePhaseError {
		apiError(w, 400, 9, "The game server is not ready.")
		return
	}
	t := gsTemplateByID(in.TemplateID)
	if t == nil {
		apiError(w, 400, 9, "That game is not on this server's platform.")
		return
	}
	if t.EULA && !in.AcceptEula {
		apiError(w, 400, 9, "Accept the game's EULA to change to this game.")
		return
	}
	env, msg := gsResolveEnv(t, in.Environment)
	if msg != "" {
		apiError(w, 400, 3, msg)
		return
	}
	// Changing the game deletes every schedule and backup and wipes the files.
	g.Schedules, g.Backups = nil, nil
	g.TemplateID, g.Env, g.DockerImage, g.VersionValue = t.ID, env, t.Images[0]["image"], ""
	g.live = "offline"
	g.startInstall(st)
	writeJSON(w, 200, g.partial())
}

func (s *Server) gsStartup(w http.ResponseWriter, r *http.Request, g *gsServer) {
	t := gsTemplateByID(g.TemplateID)
	switch r.Method {
	case http.MethodGet:
		vars := []any{}
		cmd := "java -jar {{SERVER_JARFILE}}"
		for _, v := range t.Vars {
			vars = append(vars, map[string]any{"envVariable": v.Env, "name": v.Name, "description": v.Name + ".", "serverValue": g.Env[v.Env],
				"userViewable": true, "userEditable": v.Editable, "rules": v.Rules})
		}
		images := []any{}
		for _, img := range t.Images {
			images = append(images, img)
		}
		writeJSON(w, 200, map[string]any{"startupCommand": strings.ReplaceAll(cmd, "{{SERVER_JARFILE}}", g.Env["SERVER_JARFILE"]),
			"rawStartupCommand": cmd, "variables": vars, "dockerImage": g.DockerImage, "dockerImages": images})
	case http.MethodPut:
		var in struct{ Key, Value string }
		if !decode(w, r, &in) {
			return
		}
		if in.Key == "" {
			apiError(w, 400, 3, "key: value length must be at least 1 characters")
			return
		}
		var def *gsVar
		for i := range t.Vars {
			if t.Vars[i].Env == in.Key {
				def = &t.Vars[i]
			}
		}
		if def == nil || !def.Editable {
			apiError(w, 400, 3, fmt.Sprintf("Variable %s cannot be changed.", in.Key))
			return
		}
		if strings.Contains(def.Rules, "integer") {
			if _, err := strconv.Atoi(in.Value); err != nil {
				apiError(w, 400, 9, fmt.Sprintf("The %s variable field must be an integer.", def.Name))
				return
			}
		}
		if strings.Contains(def.Rules, "required") && in.Value == "" {
			apiError(w, 400, 9, fmt.Sprintf("The %s variable field is required.", def.Name))
			return
		}
		g.Env[in.Key] = in.Value
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (a *gsAlloc) view() map[string]any {
	return map[string]any{"id": a.ID, "ip": a.IP, "port": a.Port, "alias": "", "notes": a.Notes, "isDefault": a.Default}
}

func (s *Server) gsAllocations(w http.ResponseWriter, r *http.Request, p []string, st *gsState, g *gsServer) {
	if len(p) == 4 {
		switch r.Method {
		case http.MethodGet:
			list := []any{}
			for _, a := range g.Allocs {
				list = append(list, a.view())
			}
			writeJSON(w, 200, map[string]any{"allocations": list})
		case http.MethodPost:
			if len(g.Allocs)-1 >= gsAllocationLimit {
				apiError(w, 400, 9, fmt.Sprintf("This plan allows %d extra allocations.", gsAllocationLimit))
				return
			}
			maxPort := 0
			for _, a := range g.Allocs {
				maxPort = max(maxPort, a.Port)
			}
			a := &gsAlloc{ID: 5000 + st.next(), IP: "192.0.2.10", Port: maxPort + 1}
			g.Allocs = append(g.Allocs, a)
			writeJSON(w, 200, a.view())
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	idStr, verb, _ := strings.Cut(p[4], ":")
	var a *gsAlloc
	for _, x := range g.Allocs {
		if strconv.Itoa(x.ID) == idStr {
			a = x
		}
	}
	if a == nil {
		gsMissing(w)
		return
	}
	switch {
	case verb == "primary" && r.Method == http.MethodPost:
		for _, x := range g.Allocs {
			x.Default = x == a
		}
		// The server's allocationIp/allocationPort are not updated (they go stale).
		writeJSON(w, 200, map[string]any{"success": true})
	case verb == "" && r.Method == http.MethodPost:
		var in struct{ Notes string }
		if !decode(w, r, &in) {
			return
		}
		a.Notes = in.Notes
		writeJSON(w, 200, a.view())
	case verb == "" && r.Method == http.MethodDelete:
		if a.Default {
			apiError(w, 400, 9, "You cannot delete the primary allocation for this server.")
			return
		}
		g.Allocs = gsFilter(g.Allocs, func(x *gsAlloc) bool { return x != a })
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

var gsDBNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)

func (g *gsServer) dbView(d *gsDB, withPassword bool) map[string]any {
	v := map[string]any{"id": d.ID, "name": d.Name, "username": d.Username, "host": "192.0.2.20", "port": 3306,
		"connectionsFrom": d.Remote, "password": "", "jdbc": ""}
	if withPassword {
		v["password"] = d.Password
		v["jdbc"] = fmt.Sprintf("jdbc:mysql://%s:%s@192.0.2.20:3306/%s", d.Username, d.Password, d.Name)
	}
	return v
}

func (s *Server) gsDatabases(w http.ResponseWriter, r *http.Request, p []string, st *gsState, g *gsServer) {
	if len(p) == 4 {
		switch r.Method {
		case http.MethodGet:
			list := []any{}
			for _, d := range g.DBs {
				list = append(list, g.dbView(d, false))
			}
			writeJSON(w, 200, map[string]any{"databases": list})
		case http.MethodPost:
			var in struct{ Database, Remote string }
			if !decode(w, r, &in) {
				return
			}
			if in.Remote == "" {
				in.Remote = "%"
			}
			if !gsDBNameRE.MatchString(in.Database) {
				apiError(w, 400, 9, "The database field may only contain letters, numbers, dashes and underscores.")
				return
			}
			if len(g.DBs) >= gsDatabaseLimit {
				apiError(w, 400, 9, fmt.Sprintf("This plan allows %d databases.", gsDatabaseLimit))
				return
			}
			full := fmt.Sprintf("s%d_%s", g.Num, in.Database)
			for _, d := range g.DBs {
				if d.Name == full {
					apiError(w, 400, 9, "A database with that name already exists for this server.")
					return
				}
			}
			n := st.next()
			d := &gsDB{ID: fmt.Sprintf("db%05d", n), Name: full, Username: fmt.Sprintf("u%d_%05d", g.Num, n), Remote: in.Remote,
				Password: fmt.Sprintf("generated-pw-%06d", n)}
			g.DBs = append(g.DBs, d)
			writeJSON(w, 200, g.dbView(d, true))
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	idStr, verb, _ := strings.Cut(p[4], ":")
	var d *gsDB
	for _, x := range g.DBs {
		if x.ID == idStr {
			d = x
		}
	}
	if d == nil {
		gsMissing(w)
		return
	}
	switch {
	case verb == "rotate" && r.Method == http.MethodPost:
		d.Password = fmt.Sprintf("generated-pw-%06d", st.next())
		writeJSON(w, 200, g.dbView(d, true))
	case verb == "" && r.Method == http.MethodDelete:
		g.DBs = gsFilter(g.DBs, func(x *gsDB) bool { return x != d })
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func gsStar(v string) string {
	if v == "" {
		return "*"
	}
	return v
}

func (x *gsSchedule) view() map[string]any {
	tasks := []any{}
	sort.Slice(x.Tasks, func(i, j int) bool { return x.Tasks[i].Seq < x.Tasks[j].Seq })
	for _, t := range x.Tasks {
		tasks = append(tasks, map[string]any{"id": strconv.Itoa(t.ID), "sequenceId": t.Seq, "action": t.Action, "payload": t.Payload,
			"timeOffset": t.TimeOffset, "continueOnFailure": t.Continue})
	}
	next := ""
	if x.Active {
		next = "2026-11-24T04:00:00+00:00"
	}
	return map[string]any{"id": strconv.Itoa(x.ID), "name": x.Name, "minute": x.Minute, "hour": x.Hour, "dayOfMonth": x.DayOfMonth,
		"month": x.Month, "dayOfWeek": x.DayOfWk, "isActive": x.Active, "onlyWhenOnline": x.OnlyWhenOnline, "tasks": tasks,
		"lastRunAt": "", "nextRunAt": next, "isProcessing": false}
}

func gsValidTask(action, payload string, offset int) string {
	switch action {
	case "command":
		if payload == "" {
			return "A command task needs a payload."
		}
	case "power":
		switch payload {
		case "start", "stop", "restart", "kill":
		default:
			return "A power task's payload must be start, stop, restart or kill."
		}
	case "backup":
	default:
		return "action: value must be in list [command, power, backup]"
	}
	if offset < 0 || offset > 900 {
		return "time_offset: value must be between 0 and 900"
	}
	return ""
}

func (s *Server) gsSchedules(w http.ResponseWriter, r *http.Request, p []string, st *gsState, g *gsServer) {
	type schedIn struct {
		Name, Minute, Hour, DayOfMonth, Month, DayOfWeek string
		OnlyWhenOnline                                   bool
		IsActive                                         *bool
	}
	apply := func(x *gsSchedule, in schedIn, create bool) string {
		if strings.TrimSpace(in.Name) == "" {
			return "name: value length must be at least 1 characters"
		}
		if in.Month == "" {
			return "month: value length must be at least 1 characters"
		}
		x.Name, x.Minute, x.Hour, x.DayOfMonth, x.Month, x.DayOfWk = in.Name, gsStar(in.Minute), gsStar(in.Hour), gsStar(in.DayOfMonth), in.Month, gsStar(in.DayOfWeek)
		x.OnlyWhenOnline = in.OnlyWhenOnline
		switch {
		case in.IsActive != nil:
			x.Active = *in.IsActive
		case create:
			x.Active = true // create treats a missing value as true
		default:
			x.Active = false // update treats a missing value as false
		}
		return ""
	}
	if len(p) == 4 {
		switch r.Method {
		case http.MethodGet:
			list := []any{}
			for _, x := range g.Schedules {
				list = append(list, x.view())
			}
			writeJSON(w, 200, map[string]any{"schedules": list})
		case http.MethodPost:
			var in schedIn
			if !decode(w, r, &in) {
				return
			}
			x := &gsSchedule{ID: st.next()}
			if msg := apply(x, in, true); msg != "" {
				apiError(w, 400, 3, msg)
				return
			}
			g.Schedules = append(g.Schedules, x)
			writeJSON(w, 200, x.view())
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	var x *gsSchedule
	for _, y := range g.Schedules {
		if strconv.Itoa(y.ID) == p[4] {
			x = y
		}
	}
	if x == nil {
		gsMissing(w)
		return
	}
	if len(p) == 5 {
		switch r.Method {
		case http.MethodPost:
			var in schedIn
			if !decode(w, r, &in) {
				return
			}
			if msg := apply(x, in, false); msg != "" {
				apiError(w, 400, 3, msg)
				return
			}
			writeJSON(w, 200, x.view())
		case http.MethodDelete:
			g.Schedules = gsFilter(g.Schedules, func(y *gsSchedule) bool { return y != x })
			writeJSON(w, 200, map[string]any{"success": true})
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	if p[5] != "tasks" {
		apiError(w, 404, 5, "Not Found")
		return
	}
	type taskIn struct {
		Action, Payload   string
		TimeOffset        int
		ContinueOnFailure bool
		SequenceID        int
	}
	renumber := func() {
		sort.Slice(x.Tasks, func(i, j int) bool { return x.Tasks[i].Seq < x.Tasks[j].Seq })
		for i, t := range x.Tasks {
			t.Seq = i + 1
		}
	}
	if len(p) == 6 && r.Method == http.MethodPost {
		var in taskIn
		if !decode(w, r, &in) {
			return
		}
		if msg := gsValidTask(in.Action, in.Payload, in.TimeOffset); msg != "" {
			apiError(w, 400, 3, msg)
			return
		}
		if len(x.Tasks) >= 10 {
			apiError(w, 400, 9, "Schedules may not have more than 10 tasks.")
			return
		}
		x.Tasks = append(x.Tasks, &gsTask{ID: st.next(), Seq: len(x.Tasks) + 1, Action: in.Action, Payload: in.Payload,
			TimeOffset: in.TimeOffset, Continue: in.ContinueOnFailure})
		// Like the real API, the created task and its ID are not returned.
		writeJSON(w, 200, map[string]any{"success": true})
		return
	}
	if len(p) != 7 {
		apiError(w, 404, 5, "Not Found")
		return
	}
	var t *gsTask
	for _, y := range x.Tasks {
		if strconv.Itoa(y.ID) == p[6] {
			t = y
		}
	}
	if t == nil {
		gsMissing(w)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var in taskIn
		if !decode(w, r, &in) {
			return
		}
		if msg := gsValidTask(in.Action, in.Payload, in.TimeOffset); msg != "" {
			apiError(w, 400, 3, msg)
			return
		}
		t.Action, t.Payload, t.TimeOffset, t.Continue = in.Action, in.Payload, in.TimeOffset, in.ContinueOnFailure
		if in.SequenceID > 0 {
			// Move the task to the new position.
			for _, y := range x.Tasks {
				if y != t && y.Seq >= in.SequenceID {
					y.Seq++
				}
			}
			t.Seq = in.SequenceID
			renumber()
		}
		writeJSON(w, 200, map[string]any{"success": true})
	case http.MethodDelete:
		x.Tasks = gsFilter(x.Tasks, func(y *gsTask) bool { return y != t })
		renumber()
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (b *gsBackup) view() map[string]any {
	return map[string]any{"uuid": b.UUID, "name": b.Name, "isSuccessful": b.Successful, "isLocked": b.Locked,
		"bytes": strconv.FormatInt(b.Bytes, 10), "completedAt": b.CompletedAt}
}

func (s *Server) gsBackups(w http.ResponseWriter, r *http.Request, p []string, g *gsServer) {
	if len(p) == 4 {
		switch r.Method {
		case http.MethodGet:
			list := []any{}
			for _, b := range g.Backups {
				list = append(list, b.view())
				// Backups finish in the background; this one is done after a list or two.
				if b.CompletedAt == "" {
					b.pendingGets--
					if b.pendingGets <= 0 {
						b.CompletedAt, b.Successful, b.Bytes = "2026-11-23T12:05:00+00:00", true, 52428800
					}
				}
			}
			writeJSON(w, 200, map[string]any{"backups": list})
		case http.MethodPost:
			var in struct{ Name string }
			if !decode(w, r, &in) {
				return
			}
			if len(g.Backups) >= gsBackupLimit {
				apiError(w, 400, 9, fmt.Sprintf("This plan allows %d backups.", gsBackupLimit))
				return
			}
			if in.Name == "" {
				in.Name = "Backup at 2026-11-23 12:00:00"
			}
			b := &gsBackup{UUID: s.id(), Name: in.Name, pendingGets: 1}
			g.Backups = append(g.Backups, b)
			writeJSON(w, 200, b.view())
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	idStr, verb, _ := strings.Cut(p[4], ":")
	var b *gsBackup
	for _, x := range g.Backups {
		if x.UUID == idStr {
			b = x
		}
	}
	if b == nil {
		gsMissing(w)
		return
	}
	switch {
	case verb == "lock" && r.Method == http.MethodPost:
		b.Locked = !b.Locked // a toggle, not "set locked"
		writeJSON(w, 200, map[string]any{"success": true})
	case verb == "restore" && r.Method == http.MethodPost:
		writeJSON(w, 200, map[string]any{"success": true})
	case verb == "" && r.Method == http.MethodDelete:
		if b.Locked {
			apiError(w, 400, 9, "This backup is locked and cannot be deleted.")
			return
		}
		g.Backups = gsFilter(g.Backups, func(x *gsBackup) bool { return x != b })
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}
