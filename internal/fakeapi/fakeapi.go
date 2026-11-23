// Package fakeapi is an in-memory stand-in for the NodeRings API, used by the
// provider's tests. It follows the real API's wire format and the behaviour
// the provider depends on: error bodies, unique names, normalised record
// values, system records and DNSSEC state.
package fakeapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OrgID is the organization every token acts in.
const OrgID = "4f1d2c3b-0000-4000-8000-000000000001"

// Server is a running fake API.
type Server struct {
	*httptest.Server

	mu      sync.Mutex
	seq     int
	keys    map[string]map[string]any
	zones   map[string]*zone
	Calls   []string // "METHOD path" of every request, for assertions
	failing map[string]int
	ext     map[string]any
}

// handler serves one product's routes. It returns false when the request is
// not one of its routes. Product files register handlers in init().
type handler func(s *Server, w http.ResponseWriter, r *http.Request, p []string) bool

var handlers []handler

func register(h handler) { handlers = append(handlers, h) }

// state returns per-server state for a product, creating it on first use.
// Callers hold s.mu (every handler does).
func state[T any](s *Server, key string, init func() *T) *T {
	if v, ok := s.ext[key].(*T); ok {
		return v
	}
	v := init()
	s.ext[key] = v
	return v
}

// With runs f while holding the server lock, for tests that change state directly.
func (s *Server) With(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

type zone struct {
	data    map[string]any
	records map[string]*record // key name|TYPE
	geo     map[string]map[string]any
	dnssec  string
}

type record struct {
	Name  string
	Type  string
	TTL   int64
	RData []string
}

// New starts a fake API. Close it with Close.
func New() *Server {
	s := &Server{keys: map[string]map[string]any{}, zones: map[string]*zone{}, failing: map[string]int{}, ext: map[string]any{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// FailNext makes the next n requests to "METHOD path" return HTTP 429.
func (s *Server) FailNext(methodPath string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing[methodPath] = n
}

// DeleteZoneOutOfBand removes a zone as if someone deleted it in the console.
func (s *Server) DeleteZoneOutOfBand(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.zones, id)
}

// SetRecordOutOfBand replaces an RRset as if someone changed it in the console.
func (s *Server) SetRecordOutOfBand(zoneID, name, rrtype string, ttl int64, rdata []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if z := s.zones[zoneID]; z != nil {
		z.records[name+"|"+rrtype] = &record{Name: name, Type: rrtype, TTL: ttl, RData: rdata}
	}
}

// SetDSAtParent marks a zone's DS record as present at the registrar.
func (s *Server) SetDSAtParent(zoneID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if z := s.zones[zoneID]; z != nil && z.dnssec != "DNSSEC_MODE_UNSIGNED" {
		z.dnssec = "DNSSEC_MODE_SIGNED_ACTIVE"
	}
}

func (s *Server) id() string {
	s.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.seq)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// apiError writes the API's error body: {"code": <grpc code>, "message": ..., "details": []}.
func apiError(w http.ResponseWriter, status, code int, msg string) {
	writeJSON(w, status, map[string]any{"code": code, "message": msg, "details": []any{}})
}

func notFound(w http.ResponseWriter, what string) { apiError(w, 404, 5, what+" was not found.") }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	s.Calls = append(s.Calls, key)

	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || len(r.Header.Get("Authorization")) <= len("Bearer ") {
		apiError(w, 401, 16, "Authentication required. Please sign in again.")
		return
	}
	if org := r.Header.Get("X-Organization-ID"); org != "" && org != OrgID {
		apiError(w, 403, 7, "Invalid secondary organization.")
		return
	}
	if n := s.failing[key]; n > 0 {
		s.failing[key] = n - 1
		apiError(w, 429, 8, "Rate limit exceeded. Please try again later.")
		return
	}

	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/v1/organizations" && r.Method == http.MethodGet:
		if r.URL.Query().Get("pageSize") == "" || r.URL.Query().Get("pageSize") == "0" {
			apiError(w, 400, 3, "page_size: value is required")
			return
		}
		writeJSON(w, 200, map[string]any{"organizations": []any{org()}, "totalCount": 1})
	case r.URL.Path == "/v1/organization" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"organization": org()})
	case r.URL.Path == "/v1/distros":
		writeJSON(w, 200, map[string]any{"distros": []any{
			map[string]any{"distro": "DISTRO_UBUNTU_24_04", "family": "ubuntu", "name": "Ubuntu", "version": "24.04", "label": "Ubuntu 24.04 LTS", "isDefault": true},
			map[string]any{"distro": "DISTRO_DEBIAN_13", "family": "debian", "name": "Debian", "version": "13", "label": "Debian 13", "isDefault": false},
		}})
	case len(p) == 4 && p[1] == "instantapps" && p[2] == "slug":
		if p[3] != "wordpress" {
			notFound(w, "Instant App")
			return
		}
		writeJSON(w, 200, map[string]any{"id": "7a9e0000-0000-4000-8000-000000000001", "slug": "wordpress", "name": "WordPress", "version": "6.8",
			"family": "wordpress", "distro": "DISTRO_UBUNTU_24_04", "categories": []string{"cms"}, "minVcpus": 1, "minRamGb": 1, "minDiskGb": 20,
			"documentationUrl": "https://wordpress.org/documentation/"})
	case r.URL.Path == "/v1/marketplace/regions":
		// The real API returns regions in no particular order.
		writeJSON(w, 200, map[string]any{"regions": []any{
			map[string]any{"country": "Netherlands", "city": "Amsterdam"},
			map[string]any{"country": "Germany", "city": "Frankfurt"},
		}, "totalCount": 2})
	case len(p) >= 2 && p[1] == "ssh-keys":
		s.sshKeys(w, r, p)
	case len(p) >= 3 && p[1] == "dns" && p[2] == "zones":
		s.dns(w, r, p)
	default:
		for _, h := range handlers {
			if h(s, w, r, p) {
				return
			}
		}
		apiError(w, 404, 5, "Not Found")
	}
}

// decode reads a JSON request body into v and reports a 400 on failure.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		apiError(w, 400, 3, "invalid request body: "+err.Error())
		return false
	}
	return true
}

func org() map[string]any {
	return map[string]any{"id": OrgID, "name": "Example Org", "description": "", "type": "ORGANIZATION_TYPE_CLIENT", "isVerified": true}
}

func now() string { return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC).Format(time.RFC3339) }

// --- SSH keys ---

func (s *Server) sshKeys(w http.ResponseWriter, r *http.Request, p []string) {
	switch {
	case len(p) == 2 && r.Method == http.MethodPost:
		var in struct{ Name, PublicKey string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		fields := strings.Fields(in.PublicKey)
		if len(fields) < 2 {
			apiError(w, 400, 3, "Invalid SSH public key.")
			return
		}
		sum := sha256.Sum256([]byte(fields[1]))
		fp := "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
		for _, k := range s.keys {
			if name, _ := k["name"].(string); strings.EqualFold(name, in.Name) || k["fingerprint"] == fp {
				apiError(w, 409, 6, "SSH key with same fingerprint already exists.")
				return
			}
		}
		id := s.id()
		kt := strings.TrimPrefix(strings.TrimPrefix(fields[0], "ssh-"), "ecdsa-sha2-")
		k := map[string]any{"id": id, "name": in.Name, "publicKey": strings.Join(fields, " "), "keyType": kt,
			"fingerprint": fp, "organizationId": OrgID, "timestamps": map[string]any{"createTime": now(), "updateTime": now()}}
		s.keys[id] = k
		writeJSON(w, 200, map[string]any{"sshKey": k})
	case len(p) == 2 && r.Method == http.MethodGet:
		list := []any{}
		for _, k := range s.keys {
			list = append(list, k)
		}
		writeJSON(w, 200, map[string]any{"sshKeys": list, "totalCount": len(list)})
	case len(p) == 3 && r.Method == http.MethodGet:
		if k := s.keys[p[2]]; k != nil {
			writeJSON(w, 200, map[string]any{"sshKey": k})
			return
		}
		notFound(w, "SSH key")
	case len(p) == 3 && r.Method == http.MethodDelete:
		if s.keys[p[2]] == nil {
			notFound(w, "SSH key")
			return
		}
		delete(s.keys, p[2])
		writeJSON(w, 200, map[string]any{"success": true})
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

// --- DNS ---

func (s *Server) dns(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 3 {
		switch r.Method {
		case http.MethodPost:
			var in struct{ Name string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			name := strings.TrimSuffix(strings.ToLower(in.Name), ".")
			for _, z := range s.zones {
				if z.data["name"] == name {
					apiError(w, 409, 6, "Domain already hosted.")
					return
				}
			}
			id := s.id()
			z := &zone{
				data: map[string]any{"id": id, "name": name, "state": "DNS_ZONE_STATE_PENDING_NS",
					"assignedNameservers": []string{"ns1.example-dns.net", "ns2.example-dns.net"}, "unicastNameservers": []string{},
					"delegationOk": false, "geoEnabled": false, "organizationId": OrgID,
					"timestamps": map[string]any{"createTime": now(), "updateTime": now()}},
				records: map[string]*record{
					"@|SOA": {Name: "@", Type: "SOA", TTL: 3600, RData: []string{"ns1.example-dns.net. hostmaster." + name + ". 1 10800 3600 604800 3600"}},
					"@|NS":  {Name: "@", Type: "NS", TTL: 3600, RData: []string{"ns1.example-dns.net.", "ns2.example-dns.net."}},
				},
				geo:    map[string]map[string]any{},
				dnssec: "DNSSEC_MODE_UNSIGNED",
			}
			s.zones[id] = z
			writeJSON(w, 200, map[string]any{"zone": z.view()})
		case http.MethodGet:
			list := []any{}
			for _, z := range s.zones {
				list = append(list, z.view())
			}
			writeJSON(w, 200, map[string]any{"zones": list, "totalCount": len(list)})
		}
		return
	}
	z := s.zones[p[3]]
	if z == nil {
		notFound(w, "DNS zone")
		return
	}
	if len(p) == 4 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, map[string]any{"zone": z.view()})
		case http.MethodDelete:
			if z.dnssec == "DNSSEC_MODE_SIGNED_ACTIVE" && r.URL.Query().Get("force") != "true" {
				apiError(w, 400, 9, "Remove DS at the registrar first.")
				return
			}
			delete(s.zones, p[3])
			writeJSON(w, 200, map[string]any{"success": true})
		}
		return
	}
	switch p[4] {
	case "records":
		s.records(w, r, z)
	case "geo-records", "geo-records:get":
		s.geo(w, r, z, p[4])
	case "dnssec":
		s.dnssec(w, r, z, p)
	default:
		apiError(w, 404, 5, "Not Found")
	}
}

func (z *zone) view() map[string]any {
	v := map[string]any{}
	for k, x := range z.data {
		v[k] = x
	}
	v["dnssecMode"] = z.dnssec
	v["geoEnabled"] = len(z.geo) > 0
	return v
}

func ttlJSON(t int64) string { return strconv.FormatInt(t, 10) }

// flexInt accepts an integer sent as a JSON number or string, like the real API.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(strings.Trim(string(b), `"`), 10, 64)
	*f = flexInt(v)
	return err
}

func (s *Server) records(w http.ResponseWriter, r *http.Request, z *zone) {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		var out []*record
		for _, rec := range z.records {
			if n := q.Get("name"); n != "" && !strings.Contains(rec.Name, strings.ToLower(n)) {
				continue
			}
			if t := q.Get("type"); t != "" && rec.Type != t {
				continue
			}
			out = append(out, rec)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name+out[i].Type < out[j].Name+out[j].Type })
		total := len(out)
		size, _ := strconv.Atoi(q.Get("pageSize"))
		if size <= 0 {
			size = 25
		}
		page, _ := strconv.Atoi(q.Get("page"))
		start := page * size
		if start > len(out) {
			start = len(out)
		}
		end := min(start+size, len(out))
		list := []any{}
		for _, rec := range out[start:end] {
			system := rec.Type == "SOA" || (rec.Name == "@" && rec.Type == "NS")
			list = append(list, map[string]any{"name": rec.Name, "type": rec.Type, "ttl": ttlJSON(rec.TTL), "rdata": rec.RData, "system": system})
		}
		writeJSON(w, 200, map[string]any{"records": list, "totalCount": total, "page": page, "pageSize": size})
	case http.MethodPut:
		var in struct {
			Record struct {
				Name  string   `json:"name"`
				Type  string   `json:"type"`
				TTL   flexInt  `json:"ttl"`
				RData []string `json:"rdata"`
			} `json:"record"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			apiError(w, 400, 3, "invalid body: "+err.Error())
			return
		}
		rec := in.Record
		ttl := int64(rec.TTL)
		if ttl < 30 || ttl > 86400 {
			apiError(w, 400, 3, "TTL must be between 30 and 86400 seconds.")
			return
		}
		name := strings.ToLower(rec.Name)
		if name == "" {
			name = "@"
		}
		stored := make([]string, 0, len(rec.RData))
		for _, v := range rec.RData {
			switch rec.Type {
			case "TXT":
				if !strings.HasPrefix(v, `"`) {
					apiError(w, 503, 14, "DNS service is temporarily unavailable.")
					return
				}
				stored = append(stored, v)
			case "CNAME", "MX":
				// The real backend appends a trailing dot to CNAME and MX contents.
				stored = append(stored, strings.TrimSuffix(v, ".")+".")
			default:
				stored = append(stored, v)
			}
		}
		z.records[name+"|"+rec.Type] = &record{Name: name, Type: rec.Type, TTL: ttl, RData: stored}
		// Like the real API, the response echoes the request, not what was stored.
		writeJSON(w, 200, map[string]any{"name": rec.Name, "type": rec.Type, "ttl": ttlJSON(ttl), "rdata": rec.RData})
	case http.MethodDelete:
		name, t := q.Get("name"), q.Get("type")
		if name == "" || t == "" {
			apiError(w, 400, 3, "name and type are required")
			return
		}
		delete(z.records, strings.ToLower(name)+"|"+t)
		writeJSON(w, 200, map[string]any{"success": true})
	}
}

func (s *Server) geo(w http.ResponseWriter, r *http.Request, z *zone, op string) {
	q := r.URL.Query()
	switch {
	case op == "geo-records:get":
		g := z.geo[q.Get("name")+"|"+q.Get("type")]
		if g == nil {
			notFound(w, "Geo record")
			return
		}
		writeJSON(w, 200, g)
	case r.Method == http.MethodGet:
		list := []any{}
		for _, g := range z.geo {
			list = append(list, g)
		}
		writeJSON(w, 200, map[string]any{"records": list})
	case r.Method == http.MethodPut:
		var in struct {
			Record map[string]any `json:"record"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		g := in.Record
		ttl, _ := strconv.ParseInt(fmt.Sprint(g["ttl"]), 10, 64)
		if ttl == 0 {
			ttl = 60
		}
		if ttl < 30 || ttl > 60 {
			apiError(w, 400, 3, "Geo TTL must be between 30 and 60 seconds.")
			return
		}
		g["ttl"] = ttlJSON(ttl)
		for _, m := range []string{"continents", "countries", "regions"} {
			if g[m] == nil {
				g[m] = map[string]any{}
			}
		}
		z.geo[fmt.Sprint(g["name"])+"|"+fmt.Sprint(g["type"])] = g
		writeJSON(w, 200, g)
	case r.Method == http.MethodDelete:
		delete(z.geo, q.Get("name")+"|"+q.Get("type"))
		writeJSON(w, 200, map[string]any{"success": true})
	}
}

func (s *Server) dnssec(w http.ResponseWriter, r *http.Request, z *zone, p []string) {
	action := ""
	if len(p) > 5 {
		action = p[5]
	}
	switch action {
	case "enable":
		z.dnssec = "DNSSEC_MODE_SIGNED_DS_PENDING"
	case "disable":
		var in struct{ Force bool }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if z.dnssec == "DNSSEC_MODE_SIGNED_ACTIVE" && !in.Force {
			apiError(w, 400, 9, "Remove DS at the registrar first.")
			return
		}
		z.dnssec = "DNSSEC_MODE_UNSIGNED"
	}
	info := map[string]any{"mode": z.dnssec, "algorithm": "", "keyTag": "0", "digestType": "0", "ds": "", "dnskey": "",
		"dsAtParent": z.dnssec == "DNSSEC_MODE_SIGNED_ACTIVE", "nsecMode": "nsec3"}
	if z.dnssec != "DNSSEC_MODE_UNSIGNED" {
		info["algorithm"] = "13 (ECDSAP256SHA256)"
		info["keyTag"] = "12345"
		info["digestType"] = "2"
		info["ds"] = "12345 13 2 0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"
		info["dnskey"] = "257 3 13 dGVzdGtleQ=="
	}
	writeJSON(w, 200, info)
}
