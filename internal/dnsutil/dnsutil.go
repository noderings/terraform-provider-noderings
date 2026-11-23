// Package dnsutil normalises DNS names and record values so that what
// Terraform stores matches what the NodeRings API returns.
package dnsutil

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// RecordTypes are the RRset types the API accepts.
var RecordTypes = []string{
	"A", "AAAA", "ALIAS", "CAA", "CERT", "CNAME", "DNAME", "DS", "HINFO", "HTTPS",
	"LOC", "MX", "NAPTR", "NS", "OPENPGPKEY", "PTR", "RP", "SMIMEA", "SRV", "SSHFP",
	"SVCB", "TLSA", "TXT",
}

// GeoRecordTypes are the types a geo record can have.
var GeoRecordTypes = []string{"A", "AAAA", "CNAME", "NAPTR", "SRV"}

// singleValueTypes may hold only one value per RRset.
var singleValueTypes = map[string]bool{"CNAME": true, "DNAME": true, "ALIAS": true}

// NormalizeZoneName lower-cases a zone name and removes a trailing dot.
func NormalizeZoneName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// RelativeName turns "", "@", "www", "www.example.com" or "www.example.com."
// into the API's form for a zone: lower-case, relative, "@" for the apex.
func RelativeName(name, zone string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	z := NormalizeZoneName(zone)
	if n == "" || n == "@" {
		return "@"
	}
	n = strings.TrimSuffix(n, ".")
	if z != "" {
		if n == z {
			return "@"
		}
		n = strings.TrimSuffix(n, "."+z)
	}
	return n
}

// SameName reports whether two owner names are the same within a zone.
func SameName(a, b, zone string) bool { return RelativeName(a, zone) == RelativeName(b, zone) }

// ValidateValues checks record values for the type before they reach the API.
func ValidateValues(rrtype string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("at least one record value is required")
	}
	if singleValueTypes[rrtype] && len(values) > 1 {
		return fmt.Errorf("%s records take exactly one value", rrtype)
	}
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("record values must not be empty")
		}
		switch rrtype {
		case "A":
			ip, err := netip.ParseAddr(v)
			if err != nil || !ip.Is4() {
				return fmt.Errorf("%q is not an IPv4 address", v)
			}
		case "AAAA":
			ip, err := netip.ParseAddr(v)
			if err != nil || !ip.Is6() || ip.Is4In6() {
				return fmt.Errorf("%q is not an IPv6 address", v)
			}
		case "ALIAS":
			if _, err := netip.ParseAddr(v); err == nil {
				return fmt.Errorf("ALIAS must point to a hostname, not an address")
			}
		case "TXT":
			if len(v) > 4096 {
				return fmt.Errorf("TXT values are limited to 4096 characters")
			}
		}
	}
	return nil
}

// ToAPI converts a value as written in Terraform into the API's presentation
// format. TXT values are written as plain text and quoted here; everything
// else is passed through.
func ToAPI(rrtype, v string) string {
	if rrtype == "TXT" {
		return quoteTXT(v)
	}
	return strings.TrimSpace(v)
}

// FromAPI converts a value returned by the API into the form Terraform stores.
func FromAPI(rrtype, v string) string {
	if rrtype == "TXT" {
		return unquoteTXT(v)
	}
	return strings.TrimSpace(v)
}

// Canonical returns a comparison key for a value in Terraform form, so that
// equivalent spellings (case, trailing dots, IPv6 compression, spacing)
// compare equal.
func Canonical(rrtype, v string) string {
	v = strings.TrimSpace(v)
	switch rrtype {
	case "TXT":
		return v
	case "A", "AAAA":
		if ip, err := netip.ParseAddr(v); err == nil {
			return ip.String()
		}
		return strings.ToLower(v)
	case "CNAME", "DNAME", "ALIAS", "NS", "PTR":
		return fqdn(v)
	case "MX":
		f := strings.Fields(v)
		if len(f) == 2 {
			return f[0] + " " + fqdn(f[1])
		}
	case "SRV":
		f := strings.Fields(v)
		if len(f) == 4 {
			return strings.Join(f[:3], " ") + " " + fqdn(f[3])
		}
	}
	return strings.Join(strings.Fields(strings.ToLower(v)), " ")
}

// SameValues reports whether two sets of Terraform-form values are equivalent.
func SameValues(rrtype string, a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ka, kb := keys(rrtype, a), keys(rrtype, b)
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

func keys(rrtype string, vs []string) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = Canonical(rrtype, v)
	}
	sort.Strings(out)
	return out
}

func fqdn(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" || h == "." {
		return h
	}
	return strings.TrimSuffix(h, ".") + "."
}

// quoteTXT quotes plain text as one or more DNS character-strings of at most
// 255 bytes each.
func quoteTXT(v string) string {
	var parts []string
	for len(v) > 255 {
		cut := 255
		for cut > 0 && !utf8Start(v[cut]) {
			cut--
		}
		parts = append(parts, v[:cut])
		v = v[cut:]
	}
	parts = append(parts, v)
	for i, p := range parts {
		p = strings.ReplaceAll(p, `\`, `\\`)
		p = strings.ReplaceAll(p, `"`, `\"`)
		parts[i] = `"` + p + `"`
	}
	return strings.Join(parts, " ")
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// unquoteTXT joins the character-strings of a presentation-format TXT value.
// A value with no quotes is returned as is.
func unquoteTXT(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, `"`) {
		return v
	}
	var b strings.Builder
	in := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '"':
			in = !in
		case c == '\\' && in && i+1 < len(v):
			i++
			b.WriteByte(v[i])
		case in:
			b.WriteByte(c)
		}
	}
	return b.String()
}
