// Package health — reusable readiness probe handler.
//
// Pattern: setiap service compose list of Check yang dependency-spesifik
// (DB pool, Redis, NATS, downstream gRPC). Handler eksekusi parallel dengan
// timeout per-check, return JSON status + 503 kalau ada critical fail.
//
// Usage:
//
//	healthHandler := &health.Handler{
//	    Timeout: 2 * time.Second,
//	    Checks: []health.Check{
//	        health.DBCheck("db", pool),
//	        health.NATSCheck("nats", natsPublisher),
//	        {Name: "redis", Critical: false, Check: func(ctx context.Context) error {
//	            return rdb.Ping(ctx).Err()
//	        }},
//	    },
//	}
//	mux.Handle("/readyz", healthHandler)
//
// Liveness probe (/healthz) tetap simple — DB ping atau static "ok".
// Readiness probe (/readyz) cek dependency yang harus ready untuk serve traffic.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Check — single readiness check.
type Check struct {
	Name string
	// Check eksekusi probe. Return error kalau unhealthy.
	Check func(ctx context.Context) error
	// Critical = true → fail bikin status 503. Critical = false → warning,
	// status tetap 200 (mis. tracing exporter, optional cache).
	Critical bool
}

// Result — per-check outcome.
type Result struct {
	Status    string `json:"status"`             // "ok" | "unhealthy"
	Error     string `json:"error,omitempty"`    // diisi kalau unhealthy
	Critical  bool   `json:"critical,omitempty"` // copy dari Check, helpful untuk debug
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

// Response — full JSON body response.
type Response struct {
	Status string            `json:"status"` // "ready" | "not_ready"
	Checks map[string]Result `json:"checks"`
}

// Handler — implements http.Handler.
type Handler struct {
	// Timeout per individual check. Default 2 detik.
	Timeout time.Duration
	Checks  []Check
}

// ServeHTTP — execute checks in parallel, marshal JSON response.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}

	results := make(map[string]Result, len(h.Checks))
	var mu sync.Mutex
	var wg sync.WaitGroup
	healthy := true

	parentCtx := r.Context()
	for _, c := range h.Checks {
		wg.Add(1)
		go func(parentCtx context.Context, c Check) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(parentCtx, timeout)
			defer cancel()

			start := time.Now()
			err := c.Check(ctx)
			latency := time.Since(start).Milliseconds()

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[c.Name] = Result{
					Status:    "unhealthy",
					Error:     err.Error(),
					Critical:  c.Critical,
					LatencyMS: latency,
				}
				if c.Critical {
					healthy = false
				}
			} else {
				results[c.Name] = Result{
					Status:    "ok",
					Critical:  c.Critical,
					LatencyMS: latency,
				}
			}
		}(parentCtx, c)
	}
	wg.Wait()

	resp := Response{
		Status: "ready",
		Checks: results,
	}
	if !healthy {
		resp.Status = "not_ready"
	}

	w.Header().Set("Content-Type", "application/json")
	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ----- Common Check helpers -----

// DBCheck — ping postgres pool.
func DBCheck(name string, pool *pgxpool.Pool) Check {
	return Check{
		Name:     name,
		Critical: true,
		Check: func(ctx context.Context) error {
			if pool == nil {
				return errors.New("pool not initialized")
			}
			return pool.Ping(ctx)
		},
	}
}

//go:generate mockgen -package=mock_health -source=health.go -destination=../_mock/health/health_mock.go

// PingableCheck — generic check untuk anything dengan Ping(ctx) error method.
//
// Cocok untuk Redis (go-redis Cmdable.Ping), NATS (jetstream.Conn.Status),
// atau custom resource.
type Pingable interface {
	Ping(ctx context.Context) error
}

func PingableCheck(name string, p Pingable, critical bool) Check {
	return Check{
		Name:     name,
		Critical: critical,
		Check: func(ctx context.Context) error {
			if p == nil {
				return errors.New(name + " not initialized")
			}
			return p.Ping(ctx)
		},
	}
}

// FuncCheck — convenience untuk inline closure check.
func FuncCheck(name string, critical bool, fn func(ctx context.Context) error) Check {
	return Check{Name: name, Critical: critical, Check: fn}
}
