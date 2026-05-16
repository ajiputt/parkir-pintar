package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders_CommonHeadersAlwaysSet(t *testing.T) {
	t.Parallel()
	called := false
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("next handler should be invoked")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	wantHeaders := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"Permissions-Policy":           "geolocation=(), microphone=(), camera=(), payment=()",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for k, v := range wantHeaders {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %q = %q, want %q", k, got, v)
		}
	}
}

func TestSecurityHeaders_HSTSNotSetForPlainHTTP(t *testing.T) {
	t.Parallel()
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	// No TLS, no X-Forwarded-Proto → HSTS must NOT be set.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should NOT be set over plain HTTP, got %q", got)
	}
}

func TestSecurityHeaders_HSTSSetForTLS(t *testing.T) {
	t.Parallel()
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.TLS = &tls.ConnectionState{} // simulate HTTPS
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("Strict-Transport-Security")
	want := "max-age=31536000; includeSubDomains"
	if got != want {
		t.Errorf("HSTS = %q, want %q", got, want)
	}
}

func TestSecurityHeaders_HSTSSetForForwardedHTTPS(t *testing.T) {
	t.Parallel()
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Errorf("HSTS should be set when X-Forwarded-Proto=https")
	}
}

func TestSecurityHeaders_HSTSNotSetForForwardedHTTP(t *testing.T) {
	t.Parallel()
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should NOT be set when X-Forwarded-Proto=http, got %q", got)
	}
}

func TestSecurityHeaders_PreservesNextStatusAndBody(t *testing.T) {
	t.Parallel()
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brew"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if rec.Body.String() != "brew" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "brew")
	}
}
