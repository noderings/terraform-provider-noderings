package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// pageAll asks for every item in one response. The API accepts pageSize=-1 for that.
func pageAll() url.Values { return url.Values{"page": {"0"}, "pageSize": {"-1"}} }

// --- Organizations ---

// ListOrganizations returns the organizations the token can act in.
func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	var out struct {
		Organizations []Organization `json:"organizations"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListOrganizations", path: "/v1/organizations",
		query: url.Values{"page": {"0"}, "pageSize": {"100"}}, retryUnavailable: true}, &out)
	return out.Organizations, err
}

// GetOrganization returns the organization the request acts in.
func (c *Client) GetOrganization(ctx context.Context) (*Organization, error) {
	var out struct {
		Organization *Organization `json:"organization"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetOrganization", path: "/v1/organization", retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.Organization == nil {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: "organization not found"}
	}
	return out.Organization, nil
}

// --- SSH keys ---

// CreateSSHKey uploads a public key.
func (c *Client) CreateSSHKey(ctx context.Context, name, publicKey string) (*SSHKey, error) {
	var out struct {
		SSHKey *SSHKey `json:"sshKey"`
	}
	err := c.do(ctx, request{method: http.MethodPost, op: "CreateSSHKey", path: "/v1/ssh-keys",
		body: map[string]string{"name": name, "publicKey": publicKey}}, &out)
	if err != nil {
		return nil, err
	}
	return out.SSHKey, nil
}

// GetSSHKey returns one key.
func (c *Client) GetSSHKey(ctx context.Context, id string) (*SSHKey, error) {
	var out struct {
		SSHKey *SSHKey `json:"sshKey"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetSSHKey", path: "/v1/ssh-keys/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.SSHKey == nil {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: "SSH key not found"}
	}
	return out.SSHKey, nil
}

// ListSSHKeys returns every key in the organization.
func (c *Client) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	var out struct {
		SSHKeys []SSHKey `json:"sshKeys"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListSSHKeys", path: "/v1/ssh-keys", query: pageAll(), retryUnavailable: true}, &out)
	return out.SSHKeys, err
}

// DeleteSSHKey removes a key.
func (c *Client) DeleteSSHKey(ctx context.Context, id string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteSSHKey", path: "/v1/ssh-keys/" + url.PathEscape(id)}, nil)
}

// --- DNS zones ---

// CreateDNSZone hosts a new zone.
func (c *Client) CreateDNSZone(ctx context.Context, name string) (*DNSZone, error) {
	var out struct {
		Zone *DNSZone `json:"zone"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CreateDnsZone", path: "/v1/dns/zones",
		body: map[string]string{"name": name}}, &out); err != nil {
		return nil, err
	}
	return out.Zone, nil
}

// GetDNSZone returns one zone.
func (c *Client) GetDNSZone(ctx context.Context, id string) (*DNSZone, error) {
	var out struct {
		Zone *DNSZone `json:"zone"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetDnsZone", path: "/v1/dns/zones/" + url.PathEscape(id), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	if out.Zone == nil {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: "DNS zone not found"}
	}
	return out.Zone, nil
}

// ListDNSZones returns every zone in the organization.
func (c *Client) ListDNSZones(ctx context.Context) ([]DNSZone, error) {
	var out struct {
		Zones []DNSZone `json:"zones"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListDnsZones", path: "/v1/dns/zones", query: pageAll(), retryUnavailable: true}, &out)
	return out.Zones, err
}

// DeleteDNSZone deletes a zone. force is needed while DNSSEC is active and
// the parent zone still has the DS record.
func (c *Client) DeleteDNSZone(ctx context.Context, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteDnsZone", path: "/v1/dns/zones/" + url.PathEscape(id), query: q}, nil)
}

// CheckDNSZoneDelegation re-checks the zone's NS delegation now.
func (c *Client) CheckDNSZoneDelegation(ctx context.Context, id string) (*DNSZone, error) {
	var out struct {
		Zone *DNSZone `json:"zone"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, op: "CheckDnsZoneDelegation",
		path: "/v1/dns/zones/" + url.PathEscape(id) + "/check-delegation", body: map[string]string{}, retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	return out.Zone, nil
}

// --- DNS records ---

// ListDNSRecords lists RRsets. name is a case-insensitive substring filter and
// rrtype an exact type filter; both are optional.
func (c *Client) ListDNSRecords(ctx context.Context, zoneID, name, rrtype string) ([]DNSRecord, error) {
	var all []DNSRecord
	for page := 0; ; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "pageSize": {"100"}}
		if name != "" {
			q.Set("name", name)
		}
		if rrtype != "" {
			q.Set("type", rrtype)
		}
		var out struct {
			Records    []DNSRecord `json:"records"`
			TotalCount int         `json:"totalCount"`
		}
		if err := c.do(ctx, request{method: http.MethodGet, op: "ListDnsRecords",
			path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/records", query: q, retryUnavailable: true}, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Records...)
		if len(out.Records) < 100 || len(all) >= out.TotalCount {
			return all, nil
		}
	}
}

// UpsertDNSRecord creates or replaces an RRset.
func (c *Client) UpsertDNSRecord(ctx context.Context, zoneID string, r DNSRecord) (*DNSRecord, error) {
	var out DNSRecord
	if err := c.do(ctx, request{method: http.MethodPut, op: "UpsertDnsRecord",
		path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/records", body: map[string]any{"record": r}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDNSRecord deletes an RRset. Use "@" for the zone apex.
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, name, rrtype string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteDnsRecord",
		path:  "/v1/dns/zones/" + url.PathEscape(zoneID) + "/records",
		query: url.Values{"name": {name}, "type": {rrtype}}}, nil)
}

// --- DNS geo records ---

// GetDNSGeoRecord returns one geo record.
func (c *Client) GetDNSGeoRecord(ctx context.Context, zoneID, name, rrtype string) (*DNSGeoRecord, error) {
	var out DNSGeoRecord
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetDnsGeoRecord",
		path:  "/v1/dns/zones/" + url.PathEscape(zoneID) + "/geo-records:get",
		query: url.Values{"name": {name}, "type": {rrtype}}, retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDNSGeoRecords returns every geo record in a zone.
func (c *Client) ListDNSGeoRecords(ctx context.Context, zoneID string) ([]DNSGeoRecord, error) {
	var out struct {
		Records []DNSGeoRecord `json:"records"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListDnsGeoRecords",
		path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/geo-records", retryUnavailable: true}, &out)
	return out.Records, err
}

// UpsertDNSGeoRecord creates or replaces a geo record.
func (c *Client) UpsertDNSGeoRecord(ctx context.Context, zoneID string, r DNSGeoRecord) (*DNSGeoRecord, error) {
	var out DNSGeoRecord
	if err := c.do(ctx, request{method: http.MethodPut, op: "UpsertDnsGeoRecord",
		path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/geo-records", body: map[string]any{"record": r}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDNSGeoRecord deletes a geo record.
func (c *Client) DeleteDNSGeoRecord(ctx context.Context, zoneID, name, rrtype string) error {
	return c.do(ctx, request{method: http.MethodDelete, op: "DeleteDnsGeoRecord",
		path:  "/v1/dns/zones/" + url.PathEscape(zoneID) + "/geo-records",
		query: url.Values{"name": {name}, "type": {rrtype}}}, nil)
}

// --- DNSSEC ---

// GetDNSSEC returns a zone's DNSSEC state.
func (c *Client) GetDNSSEC(ctx context.Context, zoneID string) (*DNSSECInfo, error) {
	var out DNSSECInfo
	err := c.do(ctx, request{method: http.MethodGet, op: "GetDnssec", path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/dnssec", retryUnavailable: true}, &out)
	return &out, err
}

// EnableDNSSEC signs a zone.
func (c *Client) EnableDNSSEC(ctx context.Context, zoneID string) (*DNSSECInfo, error) {
	var out DNSSECInfo
	err := c.do(ctx, request{method: http.MethodPost, op: "EnableDnssec", path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/dnssec/enable", body: map[string]any{}}, &out)
	return &out, err
}

// DisableDNSSEC stops signing a zone. force is needed while the parent still has the DS record.
func (c *Client) DisableDNSSEC(ctx context.Context, zoneID string, force bool) (*DNSSECInfo, error) {
	var out DNSSECInfo
	err := c.do(ctx, request{method: http.MethodPost, op: "DisableDnssec", path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/dnssec/disable", body: map[string]any{"force": force}}, &out)
	return &out, err
}

// CheckDNSSECDS re-checks whether the parent zone has the DS record now.
func (c *Client) CheckDNSSECDS(ctx context.Context, zoneID string) (*DNSSECInfo, error) {
	var out DNSSECInfo
	err := c.do(ctx, request{method: http.MethodPost, op: "CheckDnssecDs", path: "/v1/dns/zones/" + url.PathEscape(zoneID) + "/dnssec/check-ds", body: map[string]any{}, retryUnavailable: true}, &out)
	return &out, err
}

// --- Catalog ---

// ListDistros returns the operating systems offered for virtual machines.
func (c *Client) ListDistros(ctx context.Context) ([]Distro, error) {
	var out struct {
		Distros []Distro `json:"distros"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListDistros", path: "/v1/distros", retryUnavailable: true}, &out)
	return out.Distros, err
}

// GetInstantAppBySlug returns one Instant App.
func (c *Client) GetInstantAppBySlug(ctx context.Context, slug string) (*InstantApp, error) {
	var out InstantApp
	if err := c.do(ctx, request{method: http.MethodGet, op: "GetInstantApp", path: "/v1/instantapps/slug/" + url.PathEscape(slug), retryUnavailable: true}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListMarketplaceRegions returns the cities where the asset type is for sale.
// assetType is an API enum such as ASSET_TYPE_VM or ASSET_TYPE_GAMESERVER, or empty for all.
func (c *Client) ListMarketplaceRegions(ctx context.Context, assetType string) ([]RegionLocation, error) {
	q := url.Values{}
	if assetType != "" {
		q.Set("filterAssetType", assetType)
	}
	var out struct {
		Regions []RegionLocation `json:"regions"`
	}
	err := c.do(ctx, request{method: http.MethodGet, op: "ListAvailableRegions", path: "/v1/marketplace/regions", query: q, retryUnavailable: true}, &out)
	return out.Regions, err
}
