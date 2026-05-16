package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ----- Unit tests for unexported helpers -----

func TestClientIP_XForwardedFor_TakesFirst(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2, 10.0.0.3")
	got := clientIP(r)
	assert.Equal(t, "10.0.0.1", got)
}

func TestClientIP_XForwardedFor_Single(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.Header.Set("X-Forwarded-For", "10.0.0.5")
	got := clientIP(r)
	assert.Equal(t, "10.0.0.5", got)
}

func TestClientIP_XForwardedFor_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.Header.Set("X-Forwarded-For", "  10.0.0.7  ,  10.0.0.8")
	got := clientIP(r)
	assert.Equal(t, "10.0.0.7", got)
}

func TestClientIP_XRealIP_Fallback(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.Header.Set("X-Real-IP", "192.168.1.10")
	got := clientIP(r)
	assert.Equal(t, "192.168.1.10", got)
}

func TestClientIP_RemoteAddr_StripsPort(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.RemoteAddr = "203.0.113.42:54321"
	got := clientIP(r)
	assert.Equal(t, "203.0.113.42", got)
}

func TestClientIP_RemoteAddr_NoPort_ReturnsAsIs(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.RemoteAddr = "203.0.113.42"
	got := clientIP(r)
	assert.Equal(t, "203.0.113.42", got)
}

func TestExtractKey_DriverID_FromHeader(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.Header.Set("X-Driver-ID", "drv-abc")
	r.RemoteAddr = "10.0.0.1:80"
	got := extractKey(r, "driver_id")
	assert.Equal(t, "drv-abc", got)
}

func TestExtractKey_DriverID_FallsBackToIP_WhenHeaderMissing(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.RemoteAddr = "10.0.0.1:80"
	got := extractKey(r, "driver_id")
	assert.Equal(t, "10.0.0.1", got, "missing X-Driver-ID must fall back to IP")
}

func TestExtractKey_IPSource(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	got := extractKey(r, "ip")
	assert.Equal(t, "1.2.3.4", got)
}

func TestExtractKey_UnknownSource_DefaultsToIP(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	r.Header.Set("X-Forwarded-For", "5.6.7.8")
	got := extractKey(r, "unrecognized")
	assert.Equal(t, "5.6.7.8", got)
}

// ----- Middleware integration test (fail-open Limiter) -----

// failOpenLimiter — Redis pointed to unreachable port → Check returns
// {Allowed:true, err≠nil}. Middleware must surface OK with fail-open behavior.
func failOpenLimiter(t *testing.T) (*Limiter, func()) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond,
		MaxRetries:  -1,
	})
	cleanup := func() { _ = rdb.Close() }
	return New(rdb), cleanup
}

func TestMiddleware_FailOpen_AllowsRequest(t *testing.T) {
	t.Parallel()
	l, cleanup := failOpenLimiter(t)
	defer cleanup()

	cfg := Config{
		Endpoints: []EndpointConfig{
			{Method: "GET", Pattern: "/v1/spots", RPS: 100, Burst: 200, Key: "ip"},
		},
		Default: EndpointConfig{Method: "*", Pattern: "*", RPS: 50, Burst: 100, Key: "ip"},
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mw := Middleware(l, cfg, zap.NewNop())(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/spots", nil)
	req.RemoteAddr = "10.0.0.1:80"
	// Bound request context to avoid waiting full default redis dial timeout.
	ctx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	require.True(t, called, "fail-open: next handler must be invoked")
	assert.Equal(t, http.StatusOK, rec.Code)
	// Limit header is set regardless of Redis error.
	assert.Equal(t, "200", rec.Header().Get("X-RateLimit-Limit"))
}

func TestMiddleware_UnknownEndpoint_UsesDefaultConfig(t *testing.T) {
	t.Parallel()
	l, cleanup := failOpenLimiter(t)
	defer cleanup()

	cfg := Config{
		Default: EndpointConfig{Method: "*", Pattern: "*", RPS: 50, Burst: 100, Key: "ip"},
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := Middleware(l, cfg, zap.NewNop())(next)

	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	req.RemoteAddr = "10.0.0.1:80"
	ctx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "100", rec.Header().Get("X-RateLimit-Limit"),
		"unknown endpoint must use Default.Burst=100")
}
