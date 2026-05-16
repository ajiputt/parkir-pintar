package ratelimit

import (
	"testing"
)

// matchPattern is unexported — internal test package.
func TestMatchPattern_ExactAndPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/v1/reservations", "/v1/reservations", true},
		{"/v1/reservations", "/v1/reservations/abc", false},
		{"/v1/reservations/*", "/v1/reservations/abc", true},
		{"/v1/reservations/*", "/v1/reservations/abc:checkin", true},
		// pattern "/v1/reservations/*" → trim "*" → "/v1/reservations/".
		// "/v1/reservations" does NOT have prefix "/v1/reservations/" (trailing slash).
		{"/v1/reservations/*", "/v1/reservations", false},
		{"/v1/reservations/*", "/v1/payments/abc", false},
		{"*", "/anything", true}, // catch-all (TrimSuffix("*","*") = "" → prefix of every string)
		{"/exact", "/exact", true},
		{"/exact", "/exacto", false},
	}
	for _, c := range cases {
		got := matchPattern(c.pattern, c.path)
		if got != c.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestConfigMatch_ReturnsFirstMatch(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Endpoints: []EndpointConfig{
			{Method: "POST", Pattern: "/v1/reservations", RPS: 5, Burst: 10, Key: "ip"},
			{Method: "POST", Pattern: "/v1/reservations/*", RPS: 30, Burst: 60, Key: "ip"},
		},
		Default: EndpointConfig{Method: "*", Pattern: "*", RPS: 50, Burst: 100, Key: "ip"},
	}

	// Exact match wins (first in list).
	got := cfg.match("POST", "/v1/reservations")
	if got.RPS != 5 {
		t.Errorf("expected first match (RPS=5), got %+v", got)
	}

	// Prefix pattern.
	got = cfg.match("POST", "/v1/reservations/abc:checkin")
	if got.RPS != 30 {
		t.Errorf("expected second match (RPS=30), got %+v", got)
	}

	// Falls through to Default.
	got = cfg.match("DELETE", "/v1/unknown")
	if got.RPS != 50 {
		t.Errorf("expected default (RPS=50), got %+v", got)
	}
}

func TestConfigMatch_MethodMismatch_FallsThrough(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Endpoints: []EndpointConfig{
			{Method: "POST", Pattern: "/v1/x", RPS: 5, Burst: 10, Key: "ip"},
		},
		Default: EndpointConfig{Method: "*", Pattern: "*", RPS: 50, Burst: 100, Key: "ip"},
	}
	got := cfg.match("GET", "/v1/x")
	if got.RPS != 50 {
		t.Errorf("GET on POST-only endpoint must fall to default; got %+v", got)
	}
}

func TestConfigMatch_StarMethodMatchesAny(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Endpoints: []EndpointConfig{
			{Method: "*", Pattern: "/v1/x", RPS: 99, Burst: 200, Key: "ip"},
		},
		Default: EndpointConfig{Method: "*", Pattern: "*", RPS: 50, Burst: 100, Key: "ip"},
	}
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		got := cfg.match(m, "/v1/x")
		if got.RPS != 99 {
			t.Errorf("method %q: expected RPS=99, got %d", m, got.RPS)
		}
	}
}

func TestDefaultConfig_HasExpectedTiers(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()

	if len(cfg.Endpoints) == 0 {
		t.Fatal("DefaultConfig must define endpoints")
	}
	if cfg.Default.RPS == 0 || cfg.Default.Burst == 0 {
		t.Errorf("Default endpoint must have non-zero rate; got %+v", cfg.Default)
	}

	// Heavy tier — POST /v1/reservations
	r := cfg.match("POST", "/v1/reservations")
	if r.RPS != 5 || r.Burst != 10 {
		t.Errorf("POST /v1/reservations expected RPS=5 Burst=10, got %+v", r)
	}

	// Lifecycle — POST /v1/reservations/{id}:checkin
	r = cfg.match("POST", "/v1/reservations/abc-123:checkin")
	if r.RPS != 30 || r.Burst != 60 {
		t.Errorf("lifecycle endpoint expected RPS=30 Burst=60, got %+v", r)
	}

	// Read-side — GET /v1/spots
	r = cfg.match("GET", "/v1/spots")
	if r.RPS != 100 || r.Burst != 200 {
		t.Errorf("GET /v1/spots expected RPS=100 Burst=200, got %+v", r)
	}

	// Webhook — POST /v1/payments/midtrans/notification.
	// /v1/payments (exact) does NOT match /v1/payments/midtrans/notification, so iterator
	// reaches the webhook entry (RPS=200, Burst=400).
	r = cfg.match("POST", "/v1/payments/midtrans/notification")
	if r.RPS != 200 || r.Burst != 400 {
		t.Errorf("webhook expected RPS=200 Burst=400, got %+v", r)
	}

	// Fallback default for unknown path.
	r = cfg.match("PATCH", "/v1/random")
	if r.RPS != 50 || r.Burst != 100 {
		t.Errorf("default fallback expected RPS=50 Burst=100, got %+v", r)
	}
}
