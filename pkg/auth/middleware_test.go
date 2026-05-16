package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/auth"
)

// okHandler is a downstream http.Handler that records what it sees.
type recordedRequest struct {
	called   bool
	driverID string // from context
	header   string // X-Driver-ID header
	path     string
}

func newOKHandler(rec *recordedRequest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.called = true
		rec.driverID = auth.DriverIDFromContext(r.Context())
		rec.header = r.Header.Get("X-Driver-ID")
		rec.path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestDriverIDFromContext_Empty(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", auth.DriverIDFromContext(ctx))
}

func TestMiddleware_ValidToken_InjectsDriverID(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)
	token, err := signer.Sign(auth.Claims{Sub: "driver-42"})
	require.NoError(t, err)

	mw := &auth.Middleware{Verifier: verifier}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	req := httptest.NewRequest(http.MethodGet, "/v1/reservations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, rec.called)
	assert.Equal(t, "driver-42", rec.driverID)
	assert.Equal(t, "driver-42", rec.header)
}

func TestMiddleware_ValidToken_LowercaseBearer(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)
	token, err := signer.Sign(auth.Claims{Sub: "driver-9"})
	require.NoError(t, err)

	mw := &auth.Middleware{Verifier: verifier}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Authorization", "bearer "+token) // EqualFold should accept this
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "driver-9", rec.driverID)
}

func TestMiddleware_MissingAuthHeader_Unauthorized(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)
	mw := &auth.Middleware{Verifier: verifier}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, rec.called)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("WWW-Authenticate"), "Bearer")
	assert.Contains(t, w.Body.String(), "unauthorized")
	assert.Contains(t, w.Body.String(), "missing Authorization header")
}

func TestMiddleware_MissingAuthHeader_PassthroughIfNoToken(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)
	mw := &auth.Middleware{Verifier: verifier, PassthroughIfNoToken: true}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, rec.called)
	assert.Equal(t, "", rec.driverID) // no token → no driver id
}

func TestMiddleware_InvalidScheme(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)
	mw := &auth.Middleware{Verifier: verifier}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	cases := []string{
		"Basic abc123",
		"Token foo",
		"SingleWord",
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
			req.Header.Set("Authorization", h)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.False(t, rec.called)
			assert.Contains(t, w.Body.String(), "invalid Authorization scheme")
		})
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)
	mw := &auth.Middleware{Verifier: verifier}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Authorization", "Bearer this.is.not-a-valid-jwt")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, rec.called)
	assert.Contains(t, w.Body.String(), "invalid or expired token")
}

func TestMiddleware_ExpiredToken(t *testing.T) {
	signer := auth.NewSigner(testSecret, testIssuer, time.Hour)
	verifier := auth.NewVerifier(testSecret, testIssuer)
	past := time.Now().Add(-1 * time.Hour).Unix()
	token, err := signer.Sign(auth.Claims{Sub: "driver-1", Iat: past - 3600, Exp: past})
	require.NoError(t, err)

	mw := &auth.Middleware{Verifier: verifier}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, rec.called)
}

func TestMiddleware_SkipPaths(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, testIssuer)
	mw := &auth.Middleware{
		Verifier:  verifier,
		SkipPaths: []string{"/healthz", "/v1/public/"},
	}
	rec := &recordedRequest{}
	handler := mw.Wrap(newOKHandler(rec))

	cases := []struct {
		path       string
		shouldSkip bool
	}{
		{"/healthz", true},
		{"/healthz/extra", true}, // prefix match
		{"/v1/public/listings", true},
		{"/v1/private/foo", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			rec.called = false
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			// No Authorization header.
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if c.shouldSkip {
				assert.Equal(t, http.StatusOK, w.Code)
				assert.True(t, rec.called)
			} else {
				assert.Equal(t, http.StatusUnauthorized, w.Code)
				assert.False(t, rec.called)
			}
		})
	}
}
