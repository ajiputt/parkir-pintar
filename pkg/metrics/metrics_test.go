package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Registry ------------------------------------------------------------

func TestRegistry_IsNotNil(t *testing.T) {
	t.Parallel()
	require.NotNil(t, Registry)
}

// --- Handler -------------------------------------------------------------

func TestHandler_ReturnsValidPromOutput(t *testing.T) {
	t.Parallel()
	h := Handler()
	require.NotNil(t, h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	ct := rec.Header().Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/plain") || strings.Contains(ct, "openmetrics"),
		"Content-Type should be prometheus exposition format, got %q", ct)
	body := rec.Body.String()
	// HTTP metrics + Go runtime metrics should appear.
	assert.Contains(t, body, "http_requests_in_flight")
	assert.Contains(t, body, "go_goroutines")
}

// --- HTTPMiddleware ------------------------------------------------------

func TestHTTPMiddleware_RecordsRequest(t *testing.T) {
	t.Parallel()
	mw := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/things", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	// Scrape /metrics and verify the counter appeared with status=201.
	scrape := httptest.NewRecorder()
	Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	assert.Contains(t, body, "http_requests_total")
	// Should include status="201"
	assert.Contains(t, body, `status="201"`)
}

func TestHTTPMiddleware_DefaultStatusIs200(t *testing.T) {
	t.Parallel()
	// Handler that doesn't call WriteHeader should record 200.
	mw := HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/silent", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// --- statusRecorder ------------------------------------------------------

func TestStatusRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	inner := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: inner, status: http.StatusOK}
	sr.WriteHeader(http.StatusTeapot)
	assert.Equal(t, http.StatusTeapot, sr.status)
	assert.Equal(t, http.StatusTeapot, inner.Code, "should pass through to inner")
}

// --- normalizePath -------------------------------------------------------

func TestNormalizePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"/v1/reservations/abc-123-uuid", "/v1/reservations/{id}"},
		{"/v1/reservations/abc-123-uuid:checkin", "/v1/reservations/{id}:checkin"},
		{"/v1/healthz", "/v1/healthz"}, // short, no id
		{"/", "/"},
		{"", ""},
		// long alphanumeric without dash but >16 chars → isLikelyID requires hasDigit
		{"/a/abcdefghijklmnopqrst1", "/a/{id}"}, // >16 + has digit
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, normalizePath(c.in))
		})
	}
}

func TestIsLikelyID(t *testing.T) {
	t.Parallel()
	// too short
	assert.False(t, isLikelyID("abc", 8))
	// 8+ chars but no digit and no dash → false
	assert.False(t, isLikelyID("abcdefgh", 8))
	// 8+ chars with digit AND dash → true
	assert.True(t, isLikelyID("abc-1234", 8))
	// no dash, has digit, > 16 chars → true (the "len > 16" branch)
	assert.True(t, isLikelyID("abcdefghij12345678", 8))
	// has digit, no dash, len exactly 16 → false (needs > 16)
	assert.False(t, isLikelyID("abcdefghij123456", 8))
}

// --- NewCounter / NewGauge / NewHistogram --------------------------------

func TestNewCounter_RegistersAndIncrements(t *testing.T) {
	t.Parallel()
	c := NewCounter("test_counter_total_a", "help", []string{"k"})
	require.NotNil(t, c)
	c.WithLabelValues("v").Inc()

	scrape := httptest.NewRecorder()
	Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	assert.Contains(t, body, "test_counter_total_a")
}

func TestNewGauge_RegistersAndSets(t *testing.T) {
	t.Parallel()
	g := NewGauge("test_gauge_a", "help", []string{"k"})
	require.NotNil(t, g)
	g.WithLabelValues("v").Set(42)

	scrape := httptest.NewRecorder()
	Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Contains(t, scrape.Body.String(), "test_gauge_a")
}

func TestNewHistogram_RegistersAndObserves(t *testing.T) {
	t.Parallel()
	h := NewHistogram("test_hist_a", "help", []float64{0.1, 0.5, 1}, []string{"k"})
	require.NotNil(t, h)
	h.WithLabelValues("v").Observe(0.3)

	scrape := httptest.NewRecorder()
	Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	assert.Contains(t, body, "test_hist_a_bucket")
	assert.Contains(t, body, "test_hist_a_count")
}
