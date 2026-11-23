// Package client is a small HTTP client for the NodeRings public API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	"golang.org/x/time/rate"
)

// DefaultEndpoint is the public NodeRings API.
const DefaultEndpoint = "https://api.noderings.com"

// Config configures a Client.
type Config struct {
	// Endpoint is the API base URL, for example https://api.noderings.com.
	Endpoint string
	// Token is a NodeRings service-account token.
	Token string
	// OrganizationID, when set, is sent as X-Organization-ID on every request.
	OrganizationID string
	// UserAgent identifies the caller.
	UserAgent string
	// MaxRetries is how many times a retryable request is retried.
	MaxRetries int
	// RequestsPerSecond limits requests per API operation (method + path pattern).
	RequestsPerSecond float64
	// HTTPTimeout bounds a single HTTP request.
	HTTPTimeout time.Duration
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
	// MinBackoff and MaxBackoff bound the retry delay. Zero uses the defaults.
	MinBackoff time.Duration
	MaxBackoff time.Duration
}

// Client calls the NodeRings API.
type Client struct {
	base       *url.URL
	token      string
	org        string
	userAgent  string
	maxRetries int
	minBackoff time.Duration
	maxBackoff time.Duration
	http       *http.Client
	rps        float64

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// New returns a Client.
func New(cfg Config) (*Client, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	base, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: must be an absolute URL such as %s", cfg.Endpoint, DefaultEndpoint)
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("a NodeRings API token is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.HTTPTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		hc = &http.Client{Timeout: timeout}
	}
	c := &Client{
		base:       base,
		token:      strings.TrimSpace(cfg.Token),
		org:        strings.TrimSpace(cfg.OrganizationID),
		userAgent:  cfg.UserAgent,
		maxRetries: cfg.MaxRetries,
		minBackoff: cfg.MinBackoff,
		maxBackoff: cfg.MaxBackoff,
		http:       hc,
		rps:        cfg.RequestsPerSecond,
		limiters:   map[string]*rate.Limiter{},
	}
	if c.maxRetries < 0 {
		c.maxRetries = 0
	}
	if c.minBackoff <= 0 {
		c.minBackoff = 500 * time.Millisecond
	}
	if c.maxBackoff <= 0 {
		c.maxBackoff = 10 * time.Second
	}
	return c, nil
}

// OrganizationID returns the organization sent with each request, if any.
func (c *Client) OrganizationID() string { return c.org }

// limiter returns the token bucket for one API operation. The NodeRings API
// rate-limits each operation separately, so the client does the same.
func (c *Client) limiter(op string) *rate.Limiter {
	if c.rps <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.limiters[op]
	if !ok {
		burst := int(c.rps)
		if burst < 1 {
			burst = 1
		}
		l = rate.NewLimiter(rate.Limit(c.rps), burst)
		c.limiters[op] = l
	}
	return l
}

// request describes one API call.
type request struct {
	method string
	// op names the operation for rate limiting, e.g. "GET /v1/dns/zones/{id}".
	op    string
	path  string
	query url.Values
	body  any
	// retryUnavailable allows retrying HTTP 503. DNS writes turn record
	// validation errors into 503, so they must not be retried blindly.
	retryUnavailable bool
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	var payload []byte
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		payload = b
	}
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + r.path
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		if l := c.limiter(r.op); l != nil {
			if err := l.Wait(ctx); err != nil {
				return err
			}
		}
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, r.method, u.String(), reader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.org != "" {
			req.Header.Set("X-Organization-ID", c.org)
		}
		if c.userAgent != "" {
			req.Header.Set("User-Agent", c.userAgent)
		}

		fields := map[string]any{"method": r.method, "path": r.path, "operation": r.op, "attempt": attempt + 1}
		if payload != nil {
			tflog.Trace(ctx, "NodeRings API request body", map[string]any{"operation": r.op, "body": redact(payload)})
		}
		started := time.Now()
		resp, err := c.http.Do(req)
		fields["duration_ms"] = time.Since(started).Milliseconds()
		if err != nil {
			fields["error"] = err.Error()
			tflog.Debug(ctx, "NodeRings API request failed", fields)
			lastErr = err
			if ctx.Err() != nil || attempt >= c.maxRetries {
				return lastErr
			}
		} else {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
			_ = resp.Body.Close()
			if readErr != nil {
				return fmt.Errorf("reading response: %w", readErr)
			}
			fields["status"] = resp.StatusCode
			tflog.Debug(ctx, "NodeRings API request", fields)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if out == nil || len(bytes.TrimSpace(body)) == 0 {
					return nil
				}
				if err := json.Unmarshal(body, out); err != nil {
					return fmt.Errorf("decoding %s %s response: %w", r.method, r.path, err)
				}
				return nil
			}
			apiErr := decodeError(resp.StatusCode, body)
			lastErr = apiErr
			if !c.retryable(apiErr, r) || attempt >= c.maxRetries {
				return apiErr
			}
		}
		if err := sleep(ctx, c.backoff(attempt)); err != nil {
			return lastErr
		}
	}
}

func (c *Client) retryable(e *Error, r request) bool {
	switch e.HTTPStatus {
	case http.StatusTooManyRequests:
		// Quota errors share the 429 status but carry ErrorInfo; they never clear on retry.
		return !e.IsQuota()
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return true
	case http.StatusServiceUnavailable:
		return r.retryUnavailable
	}
	return false
}

func (c *Client) backoff(attempt int) time.Duration {
	d := c.minBackoff << min(attempt, 10)
	if d > c.maxBackoff || d <= 0 {
		d = c.maxBackoff
	}
	jitter := time.Duration(rand.Int64N(int64(d)/2 + 1))
	return d/2 + jitter
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// sensitiveKeys are JSON fields never written to logs.
var sensitiveKeys = map[string]bool{"password": true, "value": true, "token": true, "secret": true, "clientSecret": true}

// redact returns a request body for logging with sensitive fields masked.
func redact(body []byte) string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return "<non-JSON body omitted>"
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				if sensitiveKeys[k] {
					t[k] = "<redacted>"
					continue
				}
				t[k] = walk(val)
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		}
		return x
	}
	out, _ := json.Marshal(walk(v))
	return string(out)
}
