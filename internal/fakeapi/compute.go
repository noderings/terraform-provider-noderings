package fakeapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
)

// Compute fake: virtual machines, their add-ons, additional IPs, snapshots
// and firewall attachment.
//
// A VM's crStatus goes PENDING (create response) → PROCESSING → APPLIED, one
// step per request that touches the VM. While PROCESSING, power, delete,
// reinstall and firewall calls fail with FailedPrecondition "operation in
// progress", like the real API.

type fakeVM struct {
	id, hostname     string
	plan             *fakePlan
	op, cr           string
	failProvisioning bool
	provisioned      bool
	busy             int
	distro           string
	tags             map[string]bool
	firewall         string
	addons           []map[string]any
	extraIPs         []map[string]any
	reinstall        string
	reinstalledAt    string
	app              map[string]any
	n                int
	snapshots        []*fakeSnapshot
	backups          []map[string]any
	created          string
}

type fakeSnapshot struct {
	data map[string]any
	fail bool
}

type computeFake struct {
	vms              map[string]*fakeVM
	order            []string
	idem             map[string]string // idempotency key → VM ID (paid plans only)
	lose             map[string]int
	failNextVM       bool
	failNextSnapshot bool
	enableBackupRuns int
	n                int
}

func computeState(s *Server) *computeFake {
	return state(s, "compute", func() *computeFake {
		return &computeFake{vms: map[string]*fakeVM{}, idem: map[string]string{}, lose: map[string]int{}}
	})
}

// --- Out-of-band helpers for tests ---

// LoseNextResponse makes the next call to the named operation take effect but
// answer HTTP 504, as if the response was lost on the way back. op is one of
// CreateVirtualMachine, EnableAdditionalIP or CreateSnapshot.
func (s *Server) LoseNextResponse(op string) {
	s.With(func() { computeState(s).lose[op]++ })
}

// CreateVMOutOfBand creates a running VM as if it was ordered in the console,
// and returns its ID. distro is an API enum such as DISTRO_DEBIAN_13.
func (s *Server) CreateVMOutOfBand(hostname, planID, distro string) string {
	var id string
	s.With(func() {
		c := computeState(s)
		c.n++
		vm := &fakeVM{
			id: s.id(), hostname: hostname, plan: findPlan(planID), op: "OPERATION_STATUS_STARTED", cr: "CR_STATUS_APPLIED", provisioned: true,
			distro: distro, tags: map[string]bool{}, reinstall: "REINSTALL_STATUS_NONE", n: c.n, created: now(),
			addons: []map[string]any{}, extraIPs: []map[string]any{},
		}
		c.vms[vm.id] = vm
		c.order = append(c.order, vm.id)
		id = vm.id
	})
	return id
}

// FailNextVMProvisioning makes the next VM created end in crStatus FAILED.
func (s *Server) FailNextVMProvisioning() {
	s.With(func() { computeState(s).failNextVM = true })
}

// FailNextSnapshot makes the next snapshot end in operationStatus FAILED.
func (s *Server) FailNextSnapshot() {
	s.With(func() { computeState(s).failNextSnapshot = true })
}

// SetVMBusy makes the VM report "operation in progress" for the next n mutating calls.
func (s *Server) SetVMBusy(id string, n int) {
	s.With(func() {
		if vm := computeState(s).vms[id]; vm != nil {
			vm.busy = n
		}
	})
}

// DeleteVMOutOfBand deletes a VM as if someone deleted it in the console.
func (s *Server) DeleteVMOutOfBand(id string) {
	s.With(func() { computeState(s).remove(id) })
}

// SetVMPowerOutOfBand sets a VM's operationStatus, for example OPERATION_STATUS_STOPPED.
func (s *Server) SetVMPowerOutOfBand(id, status string) {
	s.With(func() {
		if vm := computeState(s).vms[id]; vm != nil {
			vm.op = status
		}
	})
}

// SetVMTagsOutOfBand replaces a VM's tags.
func (s *Server) SetVMTagsOutOfBand(id string, tags ...string) {
	s.With(func() {
		if vm := computeState(s).vms[id]; vm != nil {
			vm.tags = map[string]bool{}
			for _, t := range tags {
				vm.tags[t] = true
			}
		}
	})
}

// DisableBackupOutOfBand cancels a VM's backup add-on.
func (s *Server) DisableBackupOutOfBand(vmID string) {
	s.With(func() {
		if vm := computeState(s).vms[vmID]; vm != nil {
			vm.cancelBackups()
		}
	})
}

// EnableBackupOutOfBand activates a backup add-on as if done in the console.
func (s *Server) EnableBackupOutOfBand(vmID, addonID string) {
	s.With(func() {
		if vm := computeState(s).vms[vmID]; vm != nil {
			vm.activateBackup(computeState(s), addonID)
		}
	})
}

// EnableBackupCalls returns how many times EnableBackup was called.
func (s *Server) EnableBackupCalls() int {
	var n int
	s.With(func() { n = computeState(s).enableBackupRuns })
	return n
}

// RemoveAdditionalIPOutOfBand releases an additional IP.
func (s *Server) RemoveAdditionalIPOutOfBand(vmID, ip string) {
	s.With(func() {
		if vm := computeState(s).vms[vmID]; vm != nil {
			vm.removeIP(ip)
		}
	})
}

// DeleteSnapshotOutOfBand deletes a snapshot.
func (s *Server) DeleteSnapshotOutOfBand(vmID, snapshotID string) {
	s.With(func() {
		if vm := computeState(s).vms[vmID]; vm != nil {
			vm.removeSnapshot(snapshotID)
		}
	})
}

// SetFirewallOutOfBand sets (or, with "", clears) a VM's attached ruleset.
func (s *Server) SetFirewallOutOfBand(vmID, rulesetID string) {
	s.With(func() {
		if vm := computeState(s).vms[vmID]; vm != nil {
			vm.firewall = rulesetID
		}
	})
}

// VMCount returns the number of live VMs.
func (s *Server) VMCount() int {
	var n int
	s.With(func() { n = len(computeState(s).vms) })
	return n
}

// VMTags returns a VM's tags, sorted.
func (s *Server) VMTags(id string) []string {
	var out []string
	s.With(func() {
		if vm := computeState(s).vms[id]; vm != nil {
			out = vm.tagList()
		}
	})
	sort.Strings(out)
	return out
}

// VMOperationStatus returns a VM's operationStatus.
func (s *Server) VMOperationStatus(id string) string {
	var op string
	s.With(func() {
		if vm := computeState(s).vms[id]; vm != nil {
			op = vm.op
		}
	})
	return op
}

// --- VM model ---

func (c *computeFake) remove(id string) {
	delete(c.vms, id)
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// tick advances the VM's asynchronous state by one step.
func (vm *fakeVM) tick() {
	vm.tickSnapshots()
	if vm.reinstall == "REINSTALL_STATUS_REQUESTED" {
		vm.reinstall = "REINSTALL_STATUS_PROVISIONING"
		return
	}
	switch vm.cr {
	case "CR_STATUS_PENDING":
		vm.cr = "CR_STATUS_PROCESSING"
	case "CR_STATUS_PROCESSING":
		switch {
		case vm.failProvisioning:
			vm.cr = "CR_STATUS_FAILED"
		case vm.reinstall == "REINSTALL_STATUS_PROVISIONING":
			vm.cr = "CR_STATUS_APPLIED"
			vm.reinstall = "REINSTALL_STATUS_COMPLETED"
			vm.reinstalledAt = now()
			vm.op = "OPERATION_STATUS_STARTED"
		default:
			vm.cr = "CR_STATUS_APPLIED"
			// After the first apply the VM reports CREATED until the
			// hypervisor reports it.
			if !vm.provisioned && vm.op == "OPERATION_STATUS_STARTED" {
				vm.op = "OPERATION_STATUS_CREATED"
			}
		}
		vm.provisioned = true
	case "CR_STATUS_APPLIED":
		// The hypervisor reports the new VM; CREATED becomes STARTED.
		if vm.op == "OPERATION_STATUS_CREATED" {
			vm.op = "OPERATION_STATUS_STARTED"
		}
	}
}

func (vm *fakeVM) tickSnapshots() {
	for _, sn := range vm.snapshots {
		switch sn.data["operationStatus"] {
		case "SNAPSHOT_OPERATION_STATUS_PENDING":
			sn.data["operationStatus"] = "SNAPSHOT_OPERATION_STATUS_PROCESSING"
		case "SNAPSHOT_OPERATION_STATUS_PROCESSING":
			if sn.fail {
				sn.data["operationStatus"] = "SNAPSHOT_OPERATION_STATUS_FAILED"
			} else {
				sn.data["operationStatus"] = "SNAPSHOT_OPERATION_STATUS_COMPLETED"
			}
		}
	}
}

// operable mirrors the API's check before power, delete, reinstall and firewall calls.
func (vm *fakeVM) operable(w http.ResponseWriter) bool {
	if vm.busy > 0 {
		vm.busy--
		apiError(w, 400, 9, "Virtual machine operation in progress. Please try again shortly.")
		return false
	}
	if vm.cr == "CR_STATUS_PROCESSING" {
		vm.tick()
		apiError(w, 400, 9, "Virtual machine operation in progress. Please try again shortly.")
		return false
	}
	return true
}

func hasCap(vm *fakeVM, name string) bool {
	for _, c := range findProvider(vm.plan.provider).caps {
		if c == name {
			return true
		}
	}
	return false
}

func (vm *fakeVM) tagList() []string {
	out := []string{}
	for t := range vm.tags {
		out = append(out, t)
	}
	// The real API does not keep tag order.
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func (vm *fakeVM) view(full bool) map[string]any {
	pr := findProvider(vm.plan.provider)
	hasBackup, backupName := false, ""
	for _, a := range vm.addons {
		if a["addonType"] == "ADDON_TYPE_BACKUP" && a["addonStatus"] == "ADDON_STATUS_ACTIVE" {
			hasBackup, backupName = true, fmt.Sprint(a["addonName"])
		}
	}
	v := map[string]any{
		"id": vm.id, "hostname": vm.hostname, "operationStatus": vm.op, "cpu": vm.plan.vcpus, "memory": vm.plan.ramGB * 1024,
		"storage": vm.plan.diskGB * 1024, "providerOrganizationId": pr.id, "ipv4": fmt.Sprintf("192.0.2.%d", vm.n), "ipv6": "",
		"distro": vm.distro, "createTime": vm.created, "updateTime": now(), "regionCountry": vm.plan.region.country,
		"regionCity": vm.plan.region.city, "providerName": pr.name, "tags": vm.tagList(), "firewallRulesetId": vm.firewall,
		"addonSubscriptions": []any{}, "hasActiveBackupSubscription": hasBackup, "activeBackupSubscriptionName": backupName,
		"planBillingType": vm.plan.billing, "additionalIpv4": append([]map[string]any{}, vm.extraIPs...),
		"bandwidthEgress": map[string]any{"usedGb": float64(vm.n) / 10}, "reinstallStatus": vm.reinstall,
		"instantAppId": "", "instantAppSlug": "", "instantAppName": "", "planCode": vm.plan.code,
		"platformDriver": "PLATFORM_DRIVER_UNSPECIFIED", "capabilities": []string{}, "complianceHeld": false,
		"planId": vm.plan.id, "regionId": vm.plan.region.id, "crStatus": vm.cr,
	}
	if pr.ipv6 {
		v["ipv6"] = fmt.Sprintf("2001:db8::%d", vm.n)
	}
	if vm.reinstalledAt != "" {
		v["reinstalledAt"] = vm.reinstalledAt
	}
	if vm.app != nil {
		v["instantAppId"], v["instantAppSlug"], v["instantAppName"] = vm.app["id"], vm.app["slug"], vm.app["name"]
	}
	// List omits add-ons, the platform driver and capabilities.
	if full {
		v["addonSubscriptions"] = vm.addons
		v["platformDriver"] = pr.driver
		v["capabilities"] = pr.caps
	}
	return v
}

func (vm *fakeVM) activateBackup(c *computeFake, addonID string) {
	vm.cancelBackups()
	var addon map[string]any
	for _, a := range fakeAddons {
		if a["id"] == addonID {
			addon = a
		}
	}
	for _, a := range vm.addons {
		if a["addonId"] == addonID {
			a["addonStatus"] = "ADDON_STATUS_ACTIVE"
			a["canceledAt"] = nil
			vm.addBackup(c, addonID)
			return
		}
	}
	c.n++
	vm.addons = append(vm.addons, map[string]any{
		"id": fmt.Sprintf("a0000000-0000-4000-8000-%012d", c.n), "quantity": 1, "startDate": now(), "addonStatus": "ADDON_STATUS_ACTIVE",
		"cancelAtPeriodEnd": false, "subscriptionId": "50000000-0000-4000-8000-" + vm.id[len(vm.id)-12:], "addonId": addonID,
		"addonName": addon["name"], "addonCode": addon["code"], "addonType": "ADDON_TYPE_BACKUP", "unitAmount": "2.00", "currency": "EUR",
		"pricingModel": "PRICING_MODEL_FIXED",
	})
	vm.addBackup(c, addonID)
}

// addBackup records the immediate backup run that enabling backups starts.
func (vm *fakeVM) addBackup(c *computeFake, addonID string) {
	c.n++
	vm.backups = append(vm.backups, map[string]any{
		"id": fmt.Sprintf("b0000000-0000-4000-8000-%012d", c.n), "name": fmt.Sprintf("backup-%d", c.n), "backupId": fmt.Sprintf("backup-run-%d", c.n),
		"virtualMachineId": vm.id, "addonId": addonID, "createdAt": now(), "sizeBytes": "1073741824", "backupStatus": "BACKUP_STATUS_ACTIVE",
		"restoreStatus": "RESTORE_STATUS_NONE", "operationStatus": "BACKUP_OPERATION_STATUS_COMPLETED",
	})
}

func (vm *fakeVM) cancelBackups() {
	for _, a := range vm.addons {
		if a["addonType"] == "ADDON_TYPE_BACKUP" && a["addonStatus"] == "ADDON_STATUS_ACTIVE" {
			a["addonStatus"] = "ADDON_STATUS_CANCELED"
			a["canceledAt"] = now()
		}
	}
	vm.backups = nil
}

func (vm *fakeVM) removeIP(ip string) bool {
	for i, x := range vm.extraIPs {
		if x["ipAddress"] == ip {
			vm.extraIPs = append(vm.extraIPs[:i], vm.extraIPs[i+1:]...)
			return true
		}
	}
	return false
}

func (vm *fakeVM) removeSnapshot(id string) {
	for i, sn := range vm.snapshots {
		if sn.data["id"] == id {
			vm.snapshots = append(vm.snapshots[:i], vm.snapshots[i+1:]...)
			return
		}
	}
}

var fakeSanitizer = strings.NewReplacer("&", "&amp;", "<", "", ">", "", `"`, "&#34;", "'", "&#39;")

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var fakeDistros = map[string]bool{
	"DISTRO_UBUNTU_22_04": true, "DISTRO_UBUNTU_24_04": true, "DISTRO_UBUNTU_26_04": true, "DISTRO_FEDORA_43": true, "DISTRO_FEDORA_44": true,
	"DISTRO_DEBIAN_12": true, "DISTRO_DEBIAN_13": true, "DISTRO_CENTOS_9": true, "DISTRO_CENTOS_10": true, "DISTRO_ROCKY_9": true,
	"DISTRO_ROCKY_10": true, "DISTRO_ALMALINUX_9": true, "DISTRO_ALMALINUX_10": true,
}

// --- Routes ---

func init() {
	register(func(s *Server, w http.ResponseWriter, r *http.Request, p []string) bool {
		if len(p) < 2 || p[1] != "virtual-machines" {
			return false
		}
		c := computeState(s)
		op := computeOp(r, p)
		if c.lose[op] > 0 {
			c.lose[op]--
			s.compute(httptest.NewRecorder(), r, p, c)
			apiError(w, 504, 4, "upstream request timeout")
			return true
		}
		s.compute(w, r, p, c)
		return true
	})
}

func computeOp(r *http.Request, p []string) string {
	switch {
	case len(p) == 2 && r.Method == http.MethodPost:
		return "CreateVirtualMachine"
	case len(p) == 3 && strings.HasSuffix(p[2], ":enable-additional-ip"):
		return "EnableAdditionalIP"
	case len(p) == 4 && p[3] == "snapshots" && r.Method == http.MethodPost:
		return "CreateSnapshot"
	}
	return ""
}

func (s *Server) compute(w http.ResponseWriter, r *http.Request, p []string, c *computeFake) {
	if len(p) == 2 {
		switch r.Method {
		case http.MethodPost:
			s.createVM(w, r, c)
		case http.MethodGet:
			if q := r.URL.Query().Get("pageSize"); q == "" || q == "0" {
				apiError(w, 400, 3, "page_size: value is required")
				return
			}
			list := []any{}
			for i := len(c.order) - 1; i >= 0; i-- {
				list = append(list, c.vms[c.order[i]].view(false))
			}
			writeJSON(w, 200, map[string]any{"virtualMachines": list, "totalCount": len(list)})
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	if len(p) == 3 && p[2] == "tags" && r.Method == http.MethodGet {
		seen := map[string]bool{}
		tags := []string{}
		for _, vm := range c.vms {
			for t := range vm.tags {
				if !seen[t] {
					seen[t] = true
					tags = append(tags, t)
				}
			}
		}
		sort.Strings(tags)
		writeJSON(w, 200, map[string]any{"tags": tags, "totalCount": len(tags)})
		return
	}
	id, action, _ := strings.Cut(p[2], ":")
	vm := c.vms[id]
	if vm == nil {
		notFound(w, "Virtual machine")
		return
	}
	if len(p) >= 4 {
		switch p[3] {
		case "snapshots":
			s.snapshots(w, r, p, c, vm)
		case "backups":
			vm.tick()
			if r.Method != http.MethodGet {
				apiError(w, 404, 5, "Not Found")
				return
			}
			list := []any{}
			for i := len(vm.backups) - 1; i >= 0; i-- {
				list = append(list, vm.backups[i])
			}
			writeJSON(w, 200, map[string]any{"backups": list, "totalCount": len(list)})
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	if action == "" {
		switch r.Method {
		case http.MethodGet:
			vm.tick()
			writeJSON(w, 200, vm.view(true))
		case http.MethodPatch:
			var in struct{ AddTags, RemoveTags []string }
			if !decode(w, r, &in) {
				return
			}
			if len(in.AddTags) == 0 && len(in.RemoveTags) == 0 {
				apiError(w, 400, 3, "add_tags or remove_tags is required")
				return
			}
			for _, t := range in.RemoveTags {
				delete(vm.tags, fakeSanitizer.Replace(t))
			}
			for _, t := range in.AddTags {
				if t != "" {
					vm.tags[fakeSanitizer.Replace(t)] = true
				}
			}
			writeJSON(w, 200, vm.view(true))
		case http.MethodDelete:
			if !vm.operable(w) {
				return
			}
			c.remove(id)
			writeJSON(w, 200, map[string]any{"success": true})
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	if r.Method != http.MethodPost {
		apiError(w, 404, 5, "Not Found")
		return
	}
	s.vmAction(w, r, c, vm, action)
}

func (s *Server) createVM(w http.ResponseWriter, r *http.Request, c *computeFake) {
	var in struct {
		Hostname, PlanID, RegionID, Distro, Password, UserData, CloudInitUserDataID, InstantAppID, IdempotencyKey string
		Tags, SSHKeyIDs                                                                                           []string
		InstantAppVariables                                                                                       map[string]string
		Coupon                                                                                                    *struct{ Code string }
	}
	if !decode(w, r, &in) {
		return
	}
	switch {
	case in.Hostname == "":
		apiError(w, 400, 3, "hostname: value is required")
		return
	case !uuidRE.MatchString(in.PlanID) || !uuidRE.MatchString(in.RegionID):
		apiError(w, 400, 3, "plan_id and region_id must be UUIDs")
		return
	case !uuidRE.MatchString(in.IdempotencyKey):
		apiError(w, 400, 3, "idempotency_key: value must be a UUID")
		return
	case in.UserData != "" || in.CloudInitUserDataID != "":
		apiError(w, 400, 3, "Cloud-init user data is no longer accepted.")
		return
	case (len(in.SSHKeyIDs) > 0) == (in.Password != ""):
		apiError(w, 400, 3, "Provide exactly one of ssh_key_ids or password.")
		return
	case len(in.Password) > 255:
		apiError(w, 400, 3, "password: value length must be at most 255 characters")
		return
	case in.Coupon != nil && in.Coupon.Code != "" && in.Coupon.Code != "WELCOME10":
		apiError(w, 400, 9, "Coupon code is not valid.")
		return
	}
	plan := findPlan(in.PlanID)
	if plan == nil || plan.asset != "ASSET_TYPE_VM" {
		notFound(w, "Plan")
		return
	}
	if plan.region.id != in.RegionID {
		apiError(w, 400, 3, "The region is not part of the plan.")
		return
	}
	for _, k := range in.SSHKeyIDs {
		if s.keys[k] == nil {
			notFound(w, "SSH key")
			return
		}
	}
	var app map[string]any
	if in.InstantAppID != "" {
		for _, a := range []map[string]any{wordpressApp(), dockerApp()} {
			if a["id"] == in.InstantAppID {
				app = a
			}
		}
		if app == nil {
			notFound(w, "Instant App")
			return
		}
		if in.Distro == "" {
			in.Distro = fmt.Sprint(app["distro"])
		} else if in.Distro != app["distro"] {
			apiError(w, 400, 3, "distro does not match the Instant App's operating system.")
			return
		}
		if err := checkAppVariables(app, in.InstantAppVariables); err != "" {
			apiError(w, 400, 3, err)
			return
		}
	} else if len(in.InstantAppVariables) > 0 {
		apiError(w, 400, 3, "instant_app_variables requires instant_app_id.")
		return
	}
	if !fakeDistros[in.Distro] {
		apiError(w, 400, 3, "distro is required")
		return
	}
	if in.Distro == "DISTRO_CENTOS_10" {
		apiError(w, 404, 5, "No VM template matches the requested OS in this region.")
		return
	}
	// Paid plans honour the idempotency key; free plans ignore it.
	if plan.billing != "BILLING_TYPE_FREE" {
		if prev, ok := c.idem[in.IdempotencyKey]; ok {
			vm := c.vms[prev]
			if vm == nil {
				apiError(w, 400, 9, "This purchase attempt has ended. Use a new idempotency key.")
				return
			}
			if vm.plan.id != plan.id {
				apiError(w, 400, 9, "The idempotency key was used for a different plan.")
				return
			}
			writeJSON(w, 200, map[string]any{"success": true, "virtualMachine": vm.view(true)})
			return
		}
	}
	c.n++
	vm := &fakeVM{
		id: s.id(), hostname: fakeSanitizer.Replace(in.Hostname), plan: plan, op: "OPERATION_STATUS_STARTED", cr: "CR_STATUS_PENDING",
		failProvisioning: c.failNextVM, distro: in.Distro, tags: map[string]bool{}, reinstall: "REINSTALL_STATUS_NONE", app: app,
		n: c.n, created: now(), addons: []map[string]any{}, extraIPs: []map[string]any{},
	}
	c.failNextVM = false
	for _, t := range in.Tags {
		if t != "" {
			vm.tags[fakeSanitizer.Replace(t)] = true
		}
	}
	c.vms[vm.id] = vm
	c.order = append(c.order, vm.id)
	if plan.billing != "BILLING_TYPE_FREE" {
		c.idem[in.IdempotencyKey] = vm.id
	}
	writeJSON(w, 200, map[string]any{"success": true, "virtualMachine": vm.view(true)})
}

func checkAppVariables(app map[string]any, vars map[string]string) string {
	known := map[string]bool{}
	if md, ok := app["metadata"].(map[string]any); ok {
		if list, ok := md["variables"].([]any); ok {
			for _, v := range list {
				if m, ok := v.(map[string]any); ok {
					known[fmt.Sprint(m["name"])] = true
				}
			}
		}
	}
	for k := range vars {
		if !known[k] {
			return fmt.Sprintf("Unknown Instant App variable %q.", k)
		}
	}
	return ""
}

func (s *Server) vmAction(w http.ResponseWriter, r *http.Request, c *computeFake, vm *fakeVM, action string) {
	switch action {
	case "start", "stop", "restart":
		if !vm.operable(w) {
			return
		}
		vm.op = map[string]string{"start": "OPERATION_STATUS_STARTED", "stop": "OPERATION_STATUS_STOPPED", "restart": "OPERATION_STATUS_RESTARTED"}[action]
		vm.cr = "CR_STATUS_PROCESSING"
		writeJSON(w, 200, vm.view(true))
	case "reinstall":
		var in struct {
			Distro, Hostname, Password, InstantAppID string
			SSHKeyIDs                                []string
			InstantAppVariables                      map[string]string
		}
		if !decode(w, r, &in) || !vm.operable(w) {
			return
		}
		if (len(in.SSHKeyIDs) > 0) == (in.Password != "") {
			apiError(w, 400, 3, "Provide exactly one of ssh_key_ids or password.")
			return
		}
		if in.InstantAppID != "" && in.Distro == "" {
			in.Distro = "DISTRO_UBUNTU_24_04"
		}
		if !fakeDistros[in.Distro] {
			apiError(w, 400, 3, "distro is required")
			return
		}
		vm.distro = in.Distro
		if in.Hostname != "" {
			vm.hostname = fakeSanitizer.Replace(in.Hostname)
		}
		vm.app = nil
		if in.InstantAppID == WordPressAppID {
			vm.app = wordpressApp()
		}
		vm.op, vm.cr, vm.reinstall = "OPERATION_STATUS_REINSTALLING", "CR_STATUS_PROCESSING", "REINSTALL_STATUS_REQUESTED"
		// Reinstall deletes snapshots and cancels the backup add-on.
		vm.snapshots = nil
		vm.cancelBackups()
		writeJSON(w, 200, vm.view(true))
	case "attach-firewall", "detach-firewall":
		var in struct{ RulesetID string }
		if !decode(w, r, &in) {
			return
		}
		if !uuidRE.MatchString(in.RulesetID) {
			apiError(w, 400, 3, "ruleset_id: value must be a UUID")
			return
		}
		if !hasCap(vm, "guest_firewall") {
			apiError(w, 400, 9, "The firewall is not supported on this platform.")
			return
		}
		if !vm.operable(w) {
			return
		}
		if action == "attach-firewall" {
			vm.firewall = in.RulesetID
		} else {
			if vm.firewall == "" {
				apiError(w, 400, 9, "No firewall ruleset is attached to this virtual machine.")
				return
			}
			if vm.firewall != in.RulesetID {
				apiError(w, 400, 3, "The ruleset is not attached to this virtual machine.")
				return
			}
			vm.firewall = ""
		}
		// Attach and detach queue a "started" operation for the VM.
		vm.op, vm.cr = "OPERATION_STATUS_STARTED", "CR_STATUS_PROCESSING"
		writeJSON(w, 200, map[string]any{"success": true})
	case "enable-backup":
		var in struct{ AddonID string }
		if !decode(w, r, &in) {
			return
		}
		if !hasCap(vm, "on_demand_backup") {
			apiError(w, 400, 9, "Backups are not supported on this platform.")
			return
		}
		found := false
		for _, a := range fakeAddons {
			found = found || (a["id"] == in.AddonID && a["addonType"] == "ADDON_TYPE_BACKUP")
		}
		if !found {
			notFound(w, "Backup add-on")
			return
		}
		c.enableBackupRuns++
		vm.activateBackup(c, in.AddonID)
		writeJSON(w, 200, map[string]any{"success": true})
	case "disable-backup":
		var in struct {
			AddonID           string
			CancelImmediately bool
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.CancelImmediately || !uuidRE.MatchString(in.AddonID) {
			apiError(w, 400, 3, "cancel_immediately must be true and addon_id must be a UUID")
			return
		}
		active := false
		for _, a := range vm.addons {
			active = active || (a["addonId"] == in.AddonID && a["addonStatus"] == "ADDON_STATUS_ACTIVE")
		}
		if !active {
			notFound(w, "Active backup add-on")
			return
		}
		vm.cancelBackups()
		writeJSON(w, 200, map[string]any{"success": true})
	case "enable-additional-ip":
		if len(vm.extraIPs) >= 10 {
			apiError(w, 429, 8, "A virtual machine can have at most 10 additional IPs.")
			return
		}
		c.n++
		ip := map[string]any{"ipAddress": fmt.Sprintf("198.51.100.%d", c.n), "gateway": "198.51.100.1", "prefixLength": "24"}
		vm.extraIPs = append(vm.extraIPs, ip)
		prorated := "0.00"
		if vm.plan.billing == "BILLING_TYPE_SUBSCRIPTION" {
			prorated = "1.25"
		}
		writeJSON(w, 200, map[string]any{"allocation": ip, "totalAdditionalIps": len(vm.extraIPs), "monthlyUnitPrice": "2.50",
			"proratedChargeAmount": prorated, "currency": "EUR", "billingMessage": ""})
	case "disable-additional-ip":
		var in struct {
			IPAddress         string
			CancelImmediately bool
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.CancelImmediately {
			apiError(w, 400, 3, "cancel_immediately must be true")
			return
		}
		if len(vm.extraIPs) == 0 {
			apiError(w, 400, 9, "There are no additional IPs to remove.")
			return
		}
		if !vm.removeIP(in.IPAddress) {
			notFound(w, "Additional IP")
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request, p []string, c *computeFake, vm *fakeVM) {
	if len(p) == 4 {
		switch r.Method {
		case http.MethodGet:
			vm.tick()
			list := []any{}
			for i := len(vm.snapshots) - 1; i >= 0; i-- {
				list = append(list, vm.snapshots[i].data)
			}
			writeJSON(w, 200, map[string]any{"snapshots": list, "totalCount": len(list)})
		case http.MethodPost:
			var in struct{ Description string }
			if !decode(w, r, &in) {
				return
			}
			if !hasCap(vm, "snapshots") {
				apiError(w, 400, 9, "Snapshots are not supported on this platform.")
				return
			}
			c.n++
			sn := &fakeSnapshot{fail: c.failNextSnapshot, data: map[string]any{
				"id": s.id(), "name": "snapshot_20261123_1200", "snapshotId": fmt.Sprintf("snapshot-%d", 1795000000+c.n),
				"virtualMachineId": vm.id, "createdAt": now(), "snapshotStatus": "SNAPSHOT_STATUS_ACTIVE", "restoreStatus": "RESTORE_STATUS_NONE",
				"operationStatus": "SNAPSHOT_OPERATION_STATUS_PENDING", "description": fakeSanitizer.Replace(in.Description),
			}}
			c.failNextSnapshot = false
			vm.snapshots = append(vm.snapshots, sn)
			writeJSON(w, 200, map[string]any{"success": true, "snapshot": sn.data})
		default:
			apiError(w, 404, 5, "Not Found")
		}
		return
	}
	var sn *fakeSnapshot
	for _, x := range vm.snapshots {
		if x.data["id"] == p[4] {
			sn = x
		}
	}
	if sn == nil || len(p) != 5 {
		notFound(w, "Snapshot")
		return
	}
	switch r.Method {
	case http.MethodGet:
		vm.tick()
		writeJSON(w, 200, map[string]any{"snapshot": sn.data})
	case http.MethodDelete:
		vm.removeSnapshot(p[4])
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}
