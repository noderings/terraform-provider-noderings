package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// AddonSubscription is an add-on attached to a VM's subscription.
type AddonSubscription struct {
	ID                string `json:"id"`
	AddonStatus       string `json:"addonStatus"`
	CancelAtPeriodEnd bool   `json:"cancelAtPeriodEnd"`
	SubscriptionID    string `json:"subscriptionId"`
	AddonID           string `json:"addonId"`
	AddonName         string `json:"addonName"`
	AddonCode         string `json:"addonCode"`
	AddonType         string `json:"addonType"`
	UnitAmount        string `json:"unitAmount"`
	Currency          string `json:"currency"`
}

// AdditionalIP is an extra IPv4 address of a VM.
type AdditionalIP struct {
	IPAddress    string `json:"ipAddress"`
	Gateway      string `json:"gateway"`
	PrefixLength Int64  `json:"prefixLength"`
}

// VirtualMachine is a NodeRings virtual machine. AddonSubscriptions,
// PlatformDriver and Capabilities are only filled by GetVirtualMachine.
type VirtualMachine struct {
	ID                          string              `json:"id"`
	Hostname                    string              `json:"hostname"`
	OperationStatus             string              `json:"operationStatus"`
	CPU                         int32               `json:"cpu"`
	Memory                      int32               `json:"memory"`
	Storage                     int32               `json:"storage"`
	ProviderOrganizationID      string              `json:"providerOrganizationId"`
	IPv4                        string              `json:"ipv4"`
	IPv6                        string              `json:"ipv6"`
	Distro                      string              `json:"distro"`
	CreateTime                  *time.Time          `json:"createTime,omitempty"`
	RegionCountry               string              `json:"regionCountry"`
	RegionCity                  string              `json:"regionCity"`
	ProviderName                string              `json:"providerName"`
	Tags                        []string            `json:"tags"`
	FirewallRulesetID           string              `json:"firewallRulesetId"`
	AddonSubscriptions          []AddonSubscription `json:"addonSubscriptions"`
	HasActiveBackupSubscription bool                `json:"hasActiveBackupSubscription"`
	PlanBillingType             string              `json:"planBillingType"`
	AdditionalIPv4              []AdditionalIP      `json:"additionalIpv4"`
	ReinstallStatus             string              `json:"reinstallStatus"`
	ReinstalledAt               *time.Time          `json:"reinstalledAt,omitempty"`
	InstantAppID                string              `json:"instantAppId"`
	InstantAppSlug              string              `json:"instantAppSlug"`
	PlanCode                    string              `json:"planCode"`
	PlatformDriver              string              `json:"platformDriver"`
	Capabilities                []string            `json:"capabilities"`
	ComplianceHeld              bool                `json:"complianceHeld"`
	PlanID                      string              `json:"planId"`
	RegionID                    string              `json:"regionId"`
	CRStatus                    string              `json:"crStatus"`
}

// HasCapability reports whether the VM's platform supports a feature such as "guest_firewall".
func (v *VirtualMachine) HasCapability(name string) bool {
	for _, c := range v.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

// CreateVirtualMachineRequest is the body of a VM create. Exactly one of
// SSHKeyIDs and Password is set; Distro or InstantAppID (or both).
type CreateVirtualMachineRequest struct {
	Hostname            string            `json:"hostname"`
	PlanID              string            `json:"planId"`
	RegionID            string            `json:"regionId"`
	Distro              string            `json:"distro,omitempty"`
	Tags                []string          `json:"tags,omitempty"`
	SSHKeyIDs           []string          `json:"sshKeyIds,omitempty"`
	Password            string            `json:"password,omitempty"`
	InstantAppID        string            `json:"instantAppId,omitempty"`
	InstantAppVariables map[string]string `json:"instantAppVariables,omitempty"`
	IdempotencyKey      string            `json:"idempotencyKey"`
	Coupon              *Coupon           `json:"coupon,omitempty"`
}

// Coupon is a discount code applied at create.
type Coupon struct {
	Code string `json:"code"`
}

// ReinstallVirtualMachineRequest is the body of a reinstall. It wipes the disk.
type ReinstallVirtualMachineRequest struct {
	Distro              string            `json:"distro,omitempty"`
	Hostname            string            `json:"hostname,omitempty"`
	SSHKeyIDs           []string          `json:"sshKeyIds,omitempty"`
	Password            string            `json:"password,omitempty"`
	InstantAppID        string            `json:"instantAppId,omitempty"`
	InstantAppVariables map[string]string `json:"instantAppVariables,omitempty"`
}

// Backup is a VM backup taken by the platform.
type Backup struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	VirtualMachineID string     `json:"virtualMachineId"`
	AddonID          string     `json:"addonId"`
	CreatedAt        *time.Time `json:"createdAt,omitempty"`
	SizeBytes        Int64      `json:"sizeBytes"`
	BackupStatus     string     `json:"backupStatus"`
	RestoreStatus    string     `json:"restoreStatus"`
	OperationStatus  string     `json:"operationStatus"`
}

// Snapshot is a VM snapshot.
type Snapshot struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	SnapshotID       string     `json:"snapshotId"`
	VirtualMachineID string     `json:"virtualMachineId"`
	CreatedAt        *time.Time `json:"createdAt,omitempty"`
	SnapshotStatus   string     `json:"snapshotStatus"`
	RestoreStatus    string     `json:"restoreStatus"`
	OperationStatus  string     `json:"operationStatus"`
	Description      string     `json:"description"`
}

// EnableAdditionalIPResult is the response of adding an IP.
type EnableAdditionalIPResult struct {
	Allocation           *AdditionalIP `json:"allocation"`
	TotalAdditionalIPs   int32         `json:"totalAdditionalIps"`
	MonthlyUnitPrice     string        `json:"monthlyUnitPrice"`
	ProratedChargeAmount string        `json:"proratedChargeAmount"`
	Currency             string        `json:"currency"`
	BillingMessage       string        `json:"billingMessage"`
}

// IsBusy reports whether err is the API refusing an action because another
// operation on the VM is still running ("operation in progress", "reinstall
// in progress", "... restore in progress"). Such calls succeed when retried later.
func IsBusy(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeFailedPrecondition && strings.Contains(strings.ToLower(e.Message), "in progress")
}

// IsAmbiguous reports whether a request may or may not have taken effect:
// the connection failed, or a gateway or rate limit answered instead of the API.
func IsAmbiguous(err error) bool {
	if err == nil {
		return false
	}
	var e *Error
	if !errors.As(err, &e) {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	switch e.HTTPStatus {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusTooManyRequests:
		return !e.IsQuota()
	}
	return false
}

// once sends a request exactly once, without the client's automatic retries,
// for calls that are not safe to repeat blindly. The caller decides what to
// do after an ambiguous failure.
func (c *Client) once(ctx context.Context, r request, out any) error {
	if l := c.limiter(r.op); l != nil {
		if err := l.Wait(ctx); err != nil {
			return err
		}
	}
	single := &Client{base: c.base, token: c.token, org: c.org, userAgent: c.userAgent, http: c.http,
		minBackoff: c.minBackoff, maxBackoff: c.maxBackoff, limiters: map[string]*rate.Limiter{}}
	return single.do(ctx, r, out)
}

func vmPath(id string) string { return "/v1/virtual-machines/" + url.PathEscape(id) }

func missingErr(what string) error {
	return &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: what + " not found"}
}

// CreateVirtualMachine orders a VM. It is sent once: on free plans the API
// ignores the idempotency key, so the caller must check for the VM before retrying.
func (c *Client) CreateVirtualMachine(ctx context.Context, in CreateVirtualMachineRequest) (*VirtualMachine, error) {
	var out struct {
		VirtualMachine *VirtualMachine `json:"virtualMachine"`
	}
	if err := c.once(ctx, request{method: http.MethodPost, op: "CreateVirtualMachine", path: "/v1/virtual-machines", body: in}, &out); err != nil {
		return nil, err
	}
	return out.VirtualMachine, nil
}

// GetVirtualMachine returns one VM with its add-ons and capabilities.
func (c *Client) GetVirtualMachine(ctx context.Context, id string) (*VirtualMachine, error) {
	var out VirtualMachine
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetVirtualMachine", path: vmPath(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, missingErr("virtual machine")
	}
	return &out, nil
}

// ListVirtualMachines returns every VM in the organization, newest first.
// The list omits add-ons and capabilities.
func (c *Client) ListVirtualMachines(ctx context.Context) ([]VirtualMachine, error) {
	var out struct {
		VirtualMachines []VirtualMachine `json:"virtualMachines"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListVirtualMachines", path: "/v1/virtual-machines", query: pageAll(), retryUnavailable: true}, &out)
	return out.VirtualMachines, err
}

// UpdateVirtualMachineTags adds and removes tags.
func (c *Client) UpdateVirtualMachineTags(ctx context.Context, id string, add, remove []string) (*VirtualMachine, error) {
	var out VirtualMachine
	body := map[string][]string{"addTags": nonNil(add), "removeTags": nonNil(remove)}
	if err := c.do(ctx, request{method: http.MethodPatch, op: "UpdateVirtualMachine", path: vmPath(id), body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// DeleteVirtualMachine deletes a VM and cancels its subscription.
func (c *Client) DeleteVirtualMachine(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteVirtualMachine", path: vmPath(id)}, nil)
}

// StartVirtualMachine asks for the VM to run.
func (c *Client) StartVirtualMachine(ctx context.Context, id string) (*VirtualMachine, error) {
	return c.vmAction(ctx, id, "start", "StartVirtualMachine", map[string]any{})
}

// StopVirtualMachine asks for the VM to stop.
func (c *Client) StopVirtualMachine(ctx context.Context, id string) (*VirtualMachine, error) {
	return c.vmAction(ctx, id, "stop", "StopVirtualMachine", map[string]any{})
}

// ReinstallVirtualMachine reinstalls the operating system. It wipes the disk,
// deletes snapshots and cancels the backup add-on.
func (c *Client) ReinstallVirtualMachine(ctx context.Context, id string, in ReinstallVirtualMachineRequest) (*VirtualMachine, error) {
	return c.vmAction(ctx, id, "reinstall", "ReinstallVirtualMachine", in)
}

func (c *Client) vmAction(ctx context.Context, id, action, op string, body any) (*VirtualMachine, error) {
	var out VirtualMachine
	if err := c.do(ctx, request{method: http.MethodPost, op: op, path: vmPath(id) + ":" + action, body: body}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AttachFirewall attaches a firewall ruleset, replacing any attached one.
func (c *Client) AttachFirewall(ctx context.Context, vmID, rulesetID string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "AttachVMFirewall", path: vmPath(vmID) + ":attach-firewall", body: map[string]string{"rulesetId": rulesetID}}, nil)
}

// DetachFirewall detaches the given ruleset.
func (c *Client) DetachFirewall(ctx context.Context, vmID, rulesetID string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "DetachVMFirewall", path: vmPath(vmID) + ":detach-firewall", body: map[string]string{"rulesetId": rulesetID}}, nil)
}

// ListVMTags returns every tag in use on the organization's VMs.
func (c *Client) ListVMTags(ctx context.Context) ([]string, error) {
	var out struct {
		Tags []string `json:"tags"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListVMTags", path: "/v1/virtual-machines/tags", retryUnavailable: true}, &out)
	return out.Tags, err
}

// EnableBackup turns on a backup add-on. Calling it again for an active
// add-on starts an extra backup run.
func (c *Client) EnableBackup(ctx context.Context, vmID, addonID string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "EnableBackup", path: vmPath(vmID) + ":enable-backup", body: map[string]string{"addonId": addonID}}, nil)
}

// DisableBackup cancels a backup add-on immediately and deletes its backups.
func (c *Client) DisableBackup(ctx context.Context, vmID, addonID string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "DisableBackup", path: vmPath(vmID) + ":disable-backup",
		body: map[string]any{"addonId": addonID, "cancelImmediately": true}}, nil)
}

// EnableAdditionalIP adds (and bills) one more IPv4 address. It is sent once,
// because every call adds another address.
func (c *Client) EnableAdditionalIP(ctx context.Context, vmID string) (*EnableAdditionalIPResult, error) {
	var out EnableAdditionalIPResult
	if err := c.once(ctx, request{method: http.MethodPost, op: "EnableAdditionalIP", path: vmPath(vmID) + ":enable-additional-ip", body: map[string]any{}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DisableAdditionalIP releases an additional IPv4 address immediately.
func (c *Client) DisableAdditionalIP(ctx context.Context, vmID, ip string) error {
	return c.do(ctx, request{method: http.MethodPost, op: "DisableAdditionalIP", path: vmPath(vmID) + ":disable-additional-ip",
		body: map[string]any{"ipAddress": ip, "cancelImmediately": true}}, nil)
}

// ListBackups returns a VM's active backups.
func (c *Client) ListBackups(ctx context.Context, vmID string) ([]Backup, error) {
	var out struct {
		Backups []Backup `json:"backups"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListBackups", path: vmPath(vmID) + "/backups", query: pageAll(), retryUnavailable: true}, &out)
	return out.Backups, err
}

// CreateSnapshot starts a snapshot. It is sent once: there is no idempotency key.
func (c *Client) CreateSnapshot(ctx context.Context, vmID, description string) (*Snapshot, error) {
	var out struct {
		Snapshot *Snapshot `json:"snapshot"`
	}
	if err := c.once(ctx, request{method: http.MethodPost, op: "CreateSnapshot", path: vmPath(vmID) + "/snapshots", body: map[string]string{"description": description}}, &out); err != nil {
		return nil, err
	}
	return out.Snapshot, nil
}

// GetSnapshot returns one snapshot.
func (c *Client) GetSnapshot(ctx context.Context, vmID, id string) (*Snapshot, error) {
	var out struct {
		Snapshot *Snapshot `json:"snapshot"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetSnapshot", path: vmPath(vmID) + "/snapshots/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.Snapshot == nil || out.Snapshot.ID == "" {
		return nil, missingErr("snapshot")
	}
	return out.Snapshot, nil
}

// ListSnapshots returns a VM's active snapshots, newest first.
func (c *Client) ListSnapshots(ctx context.Context, vmID string) ([]Snapshot, error) {
	var out struct {
		Snapshots []Snapshot `json:"snapshots"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListSnapshots", path: vmPath(vmID) + "/snapshots", query: pageAll(), retryUnavailable: true}, &out)
	return out.Snapshots, err
}

// DeleteSnapshot deletes a snapshot.
func (c *Client) DeleteSnapshot(ctx context.Context, vmID, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteSnapshot", path: vmPath(vmID) + "/snapshots/" + url.PathEscape(id)}, nil)
}
