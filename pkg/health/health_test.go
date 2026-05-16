package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/health"
)

// fakePingable — implements health.Pingable for tests.
type fakePingable struct {
	err error
}

func (f *fakePingable) Ping(_ context.Context) error { return f.err }

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) health.Response {
	t.Helper()
	var resp health.Response
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp
}

// --- ServeHTTP ----------------------------------------------------------

func TestHandler_NoChecks_AlwaysReady(t *testing.T) {
	t.Parallel()
	h := &health.Handler{}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	resp := decodeBody(t, rec)
	assert.Equal(t, "ready", resp.Status)
	assert.Empty(t, resp.Checks)
}

func TestHandler_AllChecksOK(t *testing.T) {
	t.Parallel()
	h := &health.Handler{
		Checks: []health.Check{
			health.FuncCheck("a", true, func(_ context.Context) error { return nil }),
			health.FuncCheck("b", false, func(_ context.Context) error { return nil }),
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeBody(t, rec)
	assert.Equal(t, "ready", resp.Status)
	require.Len(t, resp.Checks, 2)
	assert.Equal(t, "ok", resp.Checks["a"].Status)
	assert.Equal(t, "ok", resp.Checks["b"].Status)
}

func TestHandler_CriticalFailReturns503(t *testing.T) {
	t.Parallel()
	h := &health.Handler{
		Checks: []health.Check{
			health.FuncCheck("db", true, func(_ context.Context) error { return errors.New("conn refused") }),
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	resp := decodeBody(t, rec)
	assert.Equal(t, "not_ready", resp.Status)
	got := resp.Checks["db"]
	assert.Equal(t, "unhealthy", got.Status)
	assert.Equal(t, "conn refused", got.Error)
	assert.True(t, got.Critical)
}

func TestHandler_NonCriticalFailStays200(t *testing.T) {
	t.Parallel()
	h := &health.Handler{
		Checks: []health.Check{
			health.FuncCheck("cache", false, func(_ context.Context) error { return errors.New("timeout") }),
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeBody(t, rec)
	assert.Equal(t, "ready", resp.Status, "non-critical fail must NOT trigger not_ready")
	got := resp.Checks["cache"]
	assert.Equal(t, "unhealthy", got.Status)
	assert.False(t, got.Critical)
}

func TestHandler_TimeoutEnforced(t *testing.T) {
	t.Parallel()
	// We check timeout is enforced — pass a check that respects ctx deadline.
	h := &health.Handler{
		Timeout: 20 * time.Millisecond,
		Checks: []health.Check{
			health.FuncCheck("slow", true, func(ctx context.Context) error {
				select {
				case <-time.After(500 * time.Millisecond):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}),
		},
	}
	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 200*time.Millisecond, "should not wait for full 500ms")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	resp := decodeBody(t, rec)
	assert.Equal(t, "unhealthy", resp.Checks["slow"].Status)
	assert.NotEmpty(t, resp.Checks["slow"].Error, "ctx timeout error should be reported")
}

func TestHandler_RunsChecksInParallel(t *testing.T) {
	t.Parallel()
	const n = 5
	var startCount int32
	gate := make(chan struct{})

	checks := make([]health.Check, 0, n)
	for i := 0; i < n; i++ {
		checks = append(checks, health.FuncCheck("c", false, func(_ context.Context) error {
			atomic.AddInt32(&startCount, 1)
			<-gate
			return nil
		}))
	}
	h := &health.Handler{Checks: checks, Timeout: time.Second}

	done := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		close(done)
	}()

	// Wait until all checks reached the gate (proves parallel start).
	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt32(&startCount) < int32(n) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d checks started in parallel", atomic.LoadInt32(&startCount), n)
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	<-done
}

func TestHandler_LatencyRecorded(t *testing.T) {
	t.Parallel()
	h := &health.Handler{
		Checks: []health.Check{
			health.FuncCheck("sleep", false, func(_ context.Context) error {
				time.Sleep(5 * time.Millisecond)
				return nil
			}),
		},
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	resp := decodeBody(t, rec)
	assert.GreaterOrEqual(t, resp.Checks["sleep"].LatencyMS, int64(0))
}

// --- DBCheck ------------------------------------------------------------

func TestDBCheck_NilPool(t *testing.T) {
	t.Parallel()
	c := health.DBCheck("db", nil)
	assert.Equal(t, "db", c.Name)
	assert.True(t, c.Critical)
	err := c.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

// --- PingableCheck ------------------------------------------------------

func TestPingableCheck_OK(t *testing.T) {
	t.Parallel()
	c := health.PingableCheck("redis", &fakePingable{err: nil}, true)
	assert.Equal(t, "redis", c.Name)
	assert.True(t, c.Critical)
	assert.NoError(t, c.Check(context.Background()))
}

func TestPingableCheck_PingError(t *testing.T) {
	t.Parallel()
	want := errors.New("ping fail")
	c := health.PingableCheck("redis", &fakePingable{err: want}, false)
	assert.False(t, c.Critical)
	assert.ErrorIs(t, c.Check(context.Background()), want)
}

func TestPingableCheck_NilPingable(t *testing.T) {
	t.Parallel()
	c := health.PingableCheck("nats", nil, true)
	err := c.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nats")
	assert.Contains(t, err.Error(), "not initialized")
}

// --- FuncCheck ----------------------------------------------------------

func TestFuncCheck_Builds(t *testing.T) {
	t.Parallel()
	called := false
	c := health.FuncCheck("x", true, func(_ context.Context) error {
		called = true
		return nil
	})
	assert.Equal(t, "x", c.Name)
	assert.True(t, c.Critical)
	require.NoError(t, c.Check(context.Background()))
	assert.True(t, called)
}
