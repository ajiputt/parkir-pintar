// Unit tests untuk OTel span name formatter di gateway.
// Sengaja di-extract jadi function (otelSpanName) supaya bisa di-test tanpa
// harus boot full otelhttp pipeline.
package main

import (
	"net/http"
	"net/url"
	"testing"
)

func TestOtelSpanName_FormatsMethodAndPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"GET availability", http.MethodGet, "/v1/availability", "GET /v1/availability"},
		{"POST reservation", http.MethodPost, "/v1/reservations", "POST /v1/reservations"},
		{"PUT with ID", http.MethodPut, "/v1/reservations/123", "PUT /v1/reservations/123"},
		{"DELETE simple", http.MethodDelete, "/v1/payments/abc", "DELETE /v1/payments/abc"},
		{"root path", http.MethodGet, "/", "GET /"},
		{"OPTIONS preflight", http.MethodOptions, "/v1/reservations", "OPTIONS /v1/reservations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{Method: tc.method, URL: &url.URL{Path: tc.path}}
			got := otelSpanName("operation-name-ignored", r)
			if got != tc.want {
				t.Fatalf("otelSpanName: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOtelSpanName_NilRequest(t *testing.T) {
	t.Parallel()
	// Defensive: panic-free saat request nil (paranoid edge case).
	got := otelSpanName("op", nil)
	if got != "HTTP" {
		t.Fatalf("nil request: got %q, want %q", got, "HTTP")
	}
}

func TestOtelSpanName_NilURL(t *testing.T) {
	t.Parallel()
	// Request tanpa URL — return method only (no trailing space).
	r := &http.Request{Method: http.MethodGet, URL: nil}
	got := otelSpanName("op", r)
	if got != "GET" {
		t.Fatalf("nil URL: got %q, want %q", got, "GET")
	}
}

func TestOtelSpanName_EmptyMethod(t *testing.T) {
	t.Parallel()
	// Empty method → fall back ke "HTTP" placeholder.
	r := &http.Request{Method: "", URL: &url.URL{Path: "/v1/health"}}
	got := otelSpanName("op", r)
	if got != "HTTP /v1/health" {
		t.Fatalf("empty method: got %q, want %q", got, "HTTP /v1/health")
	}
}

func TestOtelSpanName_EmptyPath(t *testing.T) {
	t.Parallel()
	// Empty path — return method only.
	r := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: ""}}
	got := otelSpanName("op", r)
	if got != "GET" {
		t.Fatalf("empty path: got %q, want %q", got, "GET")
	}
}

func TestOtelSpanName_IgnoresQueryString(t *testing.T) {
	t.Parallel()
	// Query string SHOULD NOT pollute span name — cardinality control.
	// Path saja yang masuk ke span name (URL.Path tidak include query).
	r := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/v1/availability", RawQuery: "lot_id=xyz&page=2"},
	}
	got := otelSpanName("op", r)
	if got != "GET /v1/availability" {
		t.Fatalf("query string leaked: got %q, want %q", got, "GET /v1/availability")
	}
}

func TestOtelSpanName_OperationParamUnused(t *testing.T) {
	t.Parallel()
	// First param (operation name dari otelhttp default) di-ignore.
	// Verifikasi behavior: passing different op names tidak ubah output.
	r := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/v1/health"}}
	a := otelSpanName("HTTP GET", r)
	b := otelSpanName("gateway-handler", r)
	if a != b {
		t.Fatalf("operation param leaked: %q vs %q", a, b)
	}
}
