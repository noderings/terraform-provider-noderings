package client

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Int64 decodes an int64 sent either as a JSON number or, as the API does
// for 64-bit fields, as a JSON string. It encodes as a number.
type Int64 int64

func (i *Int64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*i = Int64(v)
	return nil
}

// Timestamps holds create and update times.
type Timestamps struct {
	CreateTime *time.Time `json:"createTime,omitempty"`
	UpdateTime *time.Time `json:"updateTime,omitempty"`
}

// SSHKey is an organization SSH public key.
type SSHKey struct {
	ID             string     `json:"id"`
	PublicKey      string     `json:"publicKey"`
	KeyType        string     `json:"keyType"`
	Name           string     `json:"name"`
	Fingerprint    string     `json:"fingerprint"`
	OrganizationID string     `json:"organizationId"`
	Timestamps     Timestamps `json:"timestamps"`
}

// DNSZone is a hosted DNS zone.
type DNSZone struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	State               string     `json:"state"`
	AssignedNameservers []string   `json:"assignedNameservers"`
	UnicastNameservers  []string   `json:"unicastNameservers"`
	DelegationOK        bool       `json:"delegationOk"`
	DNSSECMode          string     `json:"dnssecMode"`
	GeoEnabled          bool       `json:"geoEnabled"`
	OrganizationID      string     `json:"organizationId"`
	Timestamps          Timestamps `json:"timestamps"`
}

// DNSRecord is one RRset: all values for a name and type.
type DNSRecord struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	TTL    Int64    `json:"ttl"`
	RData  []string `json:"rdata"`
	System bool     `json:"system,omitempty"`
}

// DNSGeoRecord answers differently by the resolver's location.
type DNSGeoRecord struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	TTL          Int64             `json:"ttl"`
	DefaultRData string            `json:"defaultRdata"`
	Continents   map[string]string `json:"continents,omitempty"`
	Countries    map[string]string `json:"countries,omitempty"`
	Regions      map[string]string `json:"regions,omitempty"`
}

// DNSSECInfo describes a zone's DNSSEC signing state.
type DNSSECInfo struct {
	Mode       string `json:"mode"`
	Algorithm  string `json:"algorithm"`
	KeyTag     Int64  `json:"keyTag"`
	DigestType Int64  `json:"digestType"`
	DS         string `json:"ds"`
	DNSKey     string `json:"dnskey"`
	DSAtParent bool   `json:"dsAtParent"`
	NSECMode   string `json:"nsecMode"`
}

// Organization is a NodeRings organization.
type Organization struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	IsVerified  bool   `json:"isVerified"`
}

// Distro is an operating system offered for virtual machines.
type Distro struct {
	Distro    string `json:"distro"`
	Family    string `json:"family"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Label     string `json:"label"`
	IsDefault bool   `json:"isDefault"`
}

// InstantApp is a one-click application image.
type InstantApp struct {
	ID               string   `json:"id"`
	Slug             string   `json:"slug"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Version          string   `json:"version"`
	Family           string   `json:"family"`
	Distro           string   `json:"distro"`
	Categories       []string `json:"categories"`
	MinVCPUs         int32    `json:"minVcpus"`
	MinRAMGB         int32    `json:"minRamGb"`
	MinDiskGB        int32    `json:"minDiskGb"`
	DocumentationURL string   `json:"documentationUrl"`
}

// RegionLocation is a city where something is for sale.
type RegionLocation struct {
	Country string `json:"country"`
	City    string `json:"city"`
}

// Raw passes a decoded JSON value through unchanged.
type Raw = json.RawMessage
