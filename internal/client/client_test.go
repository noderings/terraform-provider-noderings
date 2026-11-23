package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL, Token: "tok", OrganizationID: "org-1", UserAgent: "ua/1", MaxRetries: 3,
		MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{Token: ""}); err == nil {
		t.Fatal("expected missing token error")
	}
	if _, err := New(Config{Endpoint: "not a url", Token: "t"}); err == nil {
		t.Fatal("expected invalid endpoint error")
	}
	c, err := New(Config{Token: "t"})
	if err != nil || c.base.String() != DefaultEndpoint {
		t.Fatalf("default endpoint: %v %v", c, err)
	}
}

func TestHeaders(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("X-Organization-ID") != "org-1" || r.Header.Get("User-Agent") != "ua/1" {
			t.Errorf("headers: %v", r.Header)
		}
		_, _ = w.Write([]byte(`{"sshKey":{"id":"k1","name":"a"}}`))
	})
	k, err := c.GetSSHKey(context.Background(), "k1")
	if err != nil || k.ID != "k1" {
		t.Fatalf("got %v %v", k, err)
	}
}

func TestRetriesRateLimit(t *testing.T) {
	var n atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"code":8,"message":"Rate limit exceeded.","details":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"distros":[{"distro":"DISTRO_DEBIAN_13"}]}`))
	})
	d, err := c.ListDistros(context.Background())
	if err != nil || len(d) != 1 || n.Load() != 3 {
		t.Fatalf("got %v %v after %d calls", d, err, n.Load())
	}
}

func TestDoesNotRetryQuota(t *testing.T) {
	var n atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"code":8,"message":"DNS zone limit reached.","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"DNS_ZONES_LIMIT_REACHED","metadata":{"limit_key":"client.max_dns_zones","current":"50","limit":"50"}}]}`))
	})
	_, err := c.CreateDNSZone(context.Background(), "example.com")
	e, ok := err.(*Error)
	if !ok || !e.IsQuota() || n.Load() != 1 {
		t.Fatalf("err %v calls %d", err, n.Load())
	}
	if want := "DNS zone limit reached. (quota client.max_dns_zones: 50 of 50 used)"; e.Error() != want {
		t.Fatalf("message %q", e.Error())
	}
}

func TestDNSWritesDoNotRetryUnavailable(t *testing.T) {
	var n atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"code":14,"message":"DNS service is temporarily unavailable","details":[]}`))
	})
	if _, err := c.UpsertDNSRecord(context.Background(), "z", DNSRecord{Name: "www", Type: "A", TTL: 300, RData: []string{"192.0.2.1"}}); err == nil {
		t.Fatal("expected error")
	}
	if n.Load() != 1 {
		t.Fatalf("upsert retried: %d calls", n.Load())
	}
	n.Store(0)
	if _, err := c.GetDNSZone(context.Background(), "z"); err == nil || n.Load() != 4 {
		t.Fatalf("reads should retry 503: %v after %d calls", err, n.Load())
	}
}

func TestErrorDecoding(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"code":5,"message":"DNS zone was not found.","details":[]}`))
	})
	_, err := c.GetDNSZone(context.Background(), "z")
	if !IsNotFound(err) || IsAlreadyExists(err) {
		t.Fatalf("got %v", err)
	}
	if e := decodeError(500, []byte("<html>oops</html>")); e.Message != "<html>oops</html>" || e.Code != 0 {
		t.Fatalf("non-JSON body: %+v", e)
	}
	if e := decodeError(409, []byte(`{}`)); e.Code != CodeAlreadyExists {
		t.Fatalf("status fallback: %+v", e)
	}
}

func TestInt64(t *testing.T) {
	var v struct {
		A Int64 `json:"a"`
		B Int64 `json:"b"`
		C Int64 `json:"c"`
	}
	if err := json.Unmarshal([]byte(`{"a":"300","b":60,"c":null}`), &v); err != nil || v.A != 300 || v.B != 60 || v.C != 0 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestListDNSRecordsPages(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		recs := []map[string]any{}
		start := 0
		if r.URL.Query().Get("page") == "1" {
			start = 100
		}
		for i := start; i < min(start+100, 150); i++ {
			recs = append(recs, map[string]any{"name": "r", "type": "A", "ttl": "300", "rdata": []string{"192.0.2.1"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"records": recs, "totalCount": 150})
	})
	recs, err := c.ListDNSRecords(context.Background(), "z", "", "")
	if err != nil || len(recs) != 150 {
		t.Fatalf("got %d %v", len(recs), err)
	}
}

func TestRedact(t *testing.T) {
	got := redact([]byte(`{"hostname":"web","password":"hunter2","record":{"value":"s3cret","rdata":["x"]},"items":[{"token":"t"}]}`))
	for _, secret := range []string{"hunter2", "s3cret", `"t"`} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %s leaked: %s", secret, got)
		}
	}
	if !strings.Contains(got, `"hostname":"web"`) {
		t.Fatalf("non-sensitive field dropped: %s", got)
	}
}
