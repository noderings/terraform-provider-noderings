package dnsutil

import (
	"strings"
	"testing"
)

func TestRelativeName(t *testing.T) {
	for in, want := range map[string]string{
		"": "@", "@": "@", "WWW": "www", "www.example.com": "www", "www.example.com.": "www",
		"example.com": "@", "a.b": "a.b", "_dmarc": "_dmarc",
	} {
		if got := RelativeName(in, "Example.com."); got != want {
			t.Errorf("RelativeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateValues(t *testing.T) {
	ok := map[string][]string{
		"A": {"192.0.2.1"}, "AAAA": {"2001:db8::1"}, "CNAME": {"target.example.com."},
		"MX": {"10 mail.example.com."}, "TXT": {"v=spf1 -all"}, "ALIAS": {"lb.example.net."},
	}
	for typ, vals := range ok {
		if err := ValidateValues(typ, vals); err != nil {
			t.Errorf("%s %v: %v", typ, vals, err)
		}
	}
	bad := map[string][]string{
		"A": {"2001:db8::1"}, "AAAA": {"192.0.2.1"}, "CNAME": {"a.", "b."}, "ALIAS": {"192.0.2.1"},
		"TXT": {strings.Repeat("x", 4097)},
	}
	for typ, vals := range bad {
		if err := ValidateValues(typ, vals); err == nil {
			t.Errorf("%s %v: expected an error", typ, vals)
		}
	}
	if err := ValidateValues("A", nil); err == nil {
		t.Error("empty values accepted")
	}
}

func TestTXTRoundTrip(t *testing.T) {
	for _, v := range []string{"v=spf1 -all", `say "hi" \o/`, strings.Repeat("k", 600), "ünïcødé " + strings.Repeat("é", 200)} {
		q := ToAPI("TXT", v)
		if !strings.HasPrefix(q, `"`) {
			t.Fatalf("not quoted: %s", q)
		}
		for _, part := range strings.Split(q, `" "`) {
			if len(part) > 257 {
				t.Fatalf("character-string longer than 255 bytes: %d", len(part))
			}
		}
		if got := FromAPI("TXT", q); got != v {
			t.Fatalf("round trip:\n got %q\nwant %q", got, v)
		}
	}
	if got := FromAPI("TXT", "plain"); got != "plain" {
		t.Fatalf("unquoted value changed: %q", got)
	}
}

func TestSameValues(t *testing.T) {
	cases := []struct {
		typ  string
		a, b []string
		same bool
	}{
		{"CNAME", []string{"Target.Example.com"}, []string{"target.example.com."}, true},
		{"MX", []string{"10 mail.example.com"}, []string{"10 mail.example.com."}, true},
		{"AAAA", []string{"2001:0db8:0000::1"}, []string{"2001:db8::1"}, true},
		{"A", []string{"192.0.2.1", "192.0.2.2"}, []string{"192.0.2.2", "192.0.2.1"}, true},
		{"A", []string{"192.0.2.1"}, []string{"192.0.2.2"}, false},
		{"TXT", []string{"Hello"}, []string{"hello"}, false},
		{"CAA", []string{`0  issue "letsencrypt.org"`}, []string{`0 issue "letsencrypt.org"`}, true},
		{"A", []string{"192.0.2.1"}, []string{"192.0.2.1", "192.0.2.2"}, false},
	}
	for _, c := range cases {
		if got := SameValues(c.typ, c.a, c.b); got != c.same {
			t.Errorf("SameValues(%s, %v, %v) = %v", c.typ, c.a, c.b, got)
		}
	}
}
