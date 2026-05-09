package ratelimit

import "strings"

// EndpointConfig — rate limit policy per endpoint.
//
// Method: "POST", "GET", "*" (any)
// Pattern: exact path "/v1/reservations" atau prefix dengan "*" suffix
//
//	("/v1/reservations/*" → match "/v1/reservations/abc:checkin")
//
// Key: "ip" (default) atau "driver_id" (extract dari JWT claim — future)
type EndpointConfig struct {
	Method  string
	Pattern string
	RPS     int
	Burst   int
	Key     string
}

// Config — collection of endpoint configs + default fallback.
type Config struct {
	// Endpoints di-evaluate ordered, first match wins. Letakkan lebih spesifik di awal.
	Endpoints []EndpointConfig
	// Default — fallback kalau tidak ada match.
	Default EndpointConfig
}

// match — find endpoint config matching given method + path.
func (c *Config) match(method, path string) EndpointConfig {
	for _, ep := range c.Endpoints {
		if ep.Method != "*" && ep.Method != method {
			continue
		}
		if matchPattern(ep.Pattern, path) {
			return ep
		}
	}
	return c.Default
}

// matchPattern — exact match atau prefix kalau pattern berakhir dengan "*".
func matchPattern(pattern, path string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == path
}

// DefaultConfig — opinionated defaults untuk ParkirPintar.
//
// Tier:
//   - Heavy (5 rps): mutating operation high-cost (CreateReservation, CreatePayment)
//   - Medium (30 rps): lifecycle transition (CheckIn, CheckOut, Cancel)
//   - Light (100 rps): read-side (Availability, Spots, Get)
//   - Webhook: tinggi (provider re-deliver)
func DefaultConfig() Config {
	return Config{
		Endpoints: []EndpointConfig{
			// Heavy — 5/s, burst 10
			{Method: "POST", Pattern: "/v1/reservations", RPS: 5, Burst: 10, Key: "ip"},
			{Method: "POST", Pattern: "/v1/payments", RPS: 5, Burst: 10, Key: "ip"},

			// Lifecycle — 30/s, burst 60 (path /v1/reservations/{id}:checkin dst.)
			{Method: "POST", Pattern: "/v1/reservations/*", RPS: 30, Burst: 60, Key: "ip"},

			// Read-side — 100/s, burst 200
			{Method: "GET", Pattern: "/v1/spots", RPS: 100, Burst: 200, Key: "ip"},
			{Method: "GET", Pattern: "/v1/availability", RPS: 100, Burst: 200, Key: "ip"},
			{Method: "GET", Pattern: "/v1/reservations/*", RPS: 100, Burst: 200, Key: "ip"},
			{Method: "GET", Pattern: "/v1/invoices/*", RPS: 100, Burst: 200, Key: "ip"},
			{Method: "GET", Pattern: "/v1/payments/*", RPS: 100, Burst: 200, Key: "ip"},

			// Webhook — high (Midtrans push). 200/s, burst 400.
			{Method: "POST", Pattern: "/v1/payments/midtrans/notification", RPS: 200, Burst: 400, Key: "ip"},
		},
		// Default fallback — 50/s, burst 100
		Default: EndpointConfig{Method: "*", Pattern: "*", RPS: 50, Burst: 100, Key: "ip"},
	}
}
