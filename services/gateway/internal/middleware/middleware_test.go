package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/ajiperdana/parkir-pintar/pkg/logger"
)

// okHandler — handler trivial untuk dipakai sebagai "next" di middleware chain.
func okHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

// --- RequestID -----------------------------------------------------------

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	t.Parallel()
	var capturedRID string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRID = logger.RequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-ID")
	if got == "" {
		t.Fatalf("expected X-Request-ID header to be set")
	}
	if capturedRID != got {
		t.Fatalf("context rid (%q) should match header rid (%q)", capturedRID, got)
	}
}

func TestRequestID_PropagatesExisting(t *testing.T) {
	t.Parallel()
	const incoming = "rid-from-caller-123"
	var capturedRID string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRID = logger.RequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", incoming)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != incoming {
		t.Fatalf("response X-Request-ID = %q, want %q", got, incoming)
	}
	if capturedRID != incoming {
		t.Fatalf("context rid = %q, want %q", capturedRID, incoming)
	}
}

// --- Logging -------------------------------------------------------------

func TestLogging_RecordsStatusAndMethod(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.InfoLevel)
	log := zap.New(core)

	mw := Logging(log)
	h := mw(okHandler(http.StatusTeapot, "hi"))

	req := httptest.NewRequest(http.MethodPost, "/foo/bar", nil)
	req.Header.Set("User-Agent", "test-ua")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if recorded.Len() != 1 {
		t.Fatalf("expected exactly 1 log entry, got %d", recorded.Len())
	}
	entry := recorded.All()[0]
	if entry.Message != "http" {
		t.Errorf("log message = %q, want %q", entry.Message, "http")
	}
	fields := entry.ContextMap()
	if fields["method"] != "POST" {
		t.Errorf("method field = %v, want POST", fields["method"])
	}
	if fields["path"] != "/foo/bar" {
		t.Errorf("path field = %v, want /foo/bar", fields["path"])
	}
	if v, ok := fields["status"].(int64); !ok || v != int64(http.StatusTeapot) {
		t.Errorf("status field = %v, want %d", fields["status"], http.StatusTeapot)
	}
	if fields["ua"] != "test-ua" {
		t.Errorf("ua field = %v, want test-ua", fields["ua"])
	}
}

func TestLogging_DefaultStatusWhenHandlerSilent(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.InfoLevel)
	log := zap.New(core)

	// Handler yang tidak panggil WriteHeader — recorder default ke 200.
	mw := Logging(log)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/silent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if recorded.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", recorded.Len())
	}
	fields := recorded.All()[0].ContextMap()
	if v, ok := fields["status"].(int64); !ok || v != 200 {
		t.Errorf("default status = %v, want 200", fields["status"])
	}
}

// --- statusRecorder ------------------------------------------------------

func TestStatusRecorder_CapturesWriteHeader(t *testing.T) {
	t.Parallel()
	inner := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: inner, status: 200}
	sr.WriteHeader(http.StatusNotFound)
	if sr.status != http.StatusNotFound {
		t.Fatalf("recorder.status = %d, want %d", sr.status, http.StatusNotFound)
	}
	if inner.Code != http.StatusNotFound {
		t.Fatalf("inner.Code = %d, want %d (should pass-through)", inner.Code, http.StatusNotFound)
	}
}

// --- CORS ----------------------------------------------------------------

func TestCORS_SetsHeadersAndCallsNext(t *testing.T) {
	t.Parallel()
	called := false
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("CORS middleware should pass through GET to next handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("Access-Control-Allow-Methods missing GET: %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Errorf("Access-Control-Allow-Headers missing Authorization: %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCORS_PreflightShortCircuits(t *testing.T) {
	t.Parallel()
	called := false
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatalf("OPTIONS should NOT reach inner handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

// --- RateLimit -----------------------------------------------------------

func TestRateLimit_AllowsWithinBurst(t *testing.T) {
	t.Parallel()
	mw := RateLimit(1, 3) // 1 rps, burst 3
	h := mw(okHandler(http.StatusOK, "ok"))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "1.2.3.4:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
}

func TestRateLimit_BlocksWhenExhausted(t *testing.T) {
	t.Parallel()
	mw := RateLimit(1, 2) // burst 2 — request ke-3 langsung harus 429
	h := mw(okHandler(http.StatusOK, "ok"))

	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "9.9.9.9:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	r1 := doReq()
	r2 := doReq()
	r3 := doReq()

	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("first 2 requests should pass: r1=%d r2=%d", r1.Code, r2.Code)
	}
	if r3.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request should be 429, got %d", r3.Code)
	}
	if ra := r3.Header().Get("Retry-After"); ra != "1" {
		t.Errorf("Retry-After = %q, want 1", ra)
	}
}

func TestRateLimit_SeparateBucketsPerIP(t *testing.T) {
	t.Parallel()
	mw := RateLimit(1, 1)
	h := mw(okHandler(http.StatusOK, "ok"))

	// Habiskan bucket IP A
	reqA := httptest.NewRequest(http.MethodGet, "/x", nil)
	reqA.RemoteAddr = "10.0.0.1:1111"
	recA1 := httptest.NewRecorder()
	h.ServeHTTP(recA1, reqA)
	if recA1.Code != http.StatusOK {
		t.Fatalf("IP A first: status = %d", recA1.Code)
	}
	recA2 := httptest.NewRecorder()
	h.ServeHTTP(recA2, reqA)
	if recA2.Code != http.StatusTooManyRequests {
		t.Fatalf("IP A second: status = %d, want 429", recA2.Code)
	}

	// IP B baru — harus tetap dapat 1 request
	reqB := httptest.NewRequest(http.MethodGet, "/x", nil)
	reqB.RemoteAddr = "10.0.0.2:1111"
	recB := httptest.NewRecorder()
	h.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("IP B fresh: status = %d, want 200", recB.Code)
	}
}

func TestRateLimit_RefillsAfterTime(t *testing.T) {
	t.Parallel()
	// rps 50 — supaya cuma butuh ~20ms refill, test cepat.
	mw := RateLimit(50, 1)
	h := mw(okHandler(http.StatusOK, "ok"))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "7.7.7.7:1111"

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("rec1 status = %d", rec1.Code)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("rec2 status = %d, want 429", rec2.Code)
	}

	time.Sleep(50 * time.Millisecond) // refill 50 * 0.05 = 2.5 token

	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("rec3 (after refill) status = %d, want 200", rec3.Code)
	}
}

// --- minF / clientIP -----------------------------------------------------

func TestMinF(t *testing.T) {
	t.Parallel()
	if got := minF(1.0, 2.0); got != 1.0 {
		t.Errorf("minF(1,2) = %v, want 1", got)
	}
	if got := minF(5.0, 2.0); got != 2.0 {
		t.Errorf("minF(5,2) = %v, want 2", got)
	}
	if got := minF(3.0, 3.0); got != 3.0 {
		t.Errorf("minF(3,3) = %v, want 3", got)
	}
}

func TestClientIP_FromRemoteAddr(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "192.168.1.1:54321"
	if got := clientIP(req); got != "192.168.1.1:54321" {
		t.Errorf("clientIP = %q, want %q", got, "192.168.1.1:54321")
	}
}

func TestClientIP_SingleXForwardedFor(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := clientIP(req); got != "203.0.113.7" {
		t.Errorf("clientIP = %q, want %q", got, "203.0.113.7")
	}
}

func TestClientIP_MultiXForwardedFor(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	// Klien original di posisi paling kiri.
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1, 10.0.0.2")
	if got := clientIP(req); got != "203.0.113.7" {
		t.Errorf("clientIP = %q, want %q", got, "203.0.113.7")
	}
}

// --- Authn ---------------------------------------------------------------

func TestAuthn_PassThrough(t *testing.T) {
	t.Parallel()
	called := false
	mw := Authn(AuthnConfig{Secret: "s", SkipPaths: []string{"/health"}})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("Authn (demo passthrough) should forward to next handler")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- AsContext -----------------------------------------------------------

func TestAsContext_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	if ctx := AsContext(nil); ctx == nil {
		t.Fatalf("AsContext returned nil context")
	}
}
