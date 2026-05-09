// Package middleware — HTTP middleware untuk gateway: auth, request_id, rate limit, CORS.
package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ajiperdana/parkir-pintar/pkg/logger"
)

// RequestID — generate atau propagate X-Request-ID header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", rid)
		ctx := logger.WithRequestID(r.Context(), rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logging — wrap handler dengan log start/stop + status + latency.
func Logging(base *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			log := logger.FromContext(r.Context(), base)
			log.Info("http",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rw.status),
				zap.Duration("latency", time.Since(start)),
				zap.String("ua", r.Header.Get("User-Agent")),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(s int) {
	r.status = s
	r.ResponseWriter.WriteHeader(s)
}

// CORS — sederhana untuk demo.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,PATCH,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, Idempotency-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimit — token bucket per-IP sederhana (in-memory).
// Production: pakai Redis-based rate limiter atau API Gateway native (Kong, Envoy).
func RateLimit(rps int, burst int) func(http.Handler) http.Handler {
	type bucket struct {
		tokens   float64
		lastFill time.Time
	}
	var (
		mu      sync.Mutex
		buckets = map[string]*bucket{}
	)
	rate := float64(rps)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			mu.Lock()
			b, ok := buckets[ip]
			now := time.Now()
			if !ok {
				b = &bucket{tokens: float64(burst), lastFill: now}
				buckets[ip] = b
			} else {
				elapsed := now.Sub(b.lastFill).Seconds()
				b.tokens = minF(float64(burst), b.tokens+elapsed*rate)
				b.lastFill = now
			}
			if b.tokens < 1 {
				mu.Unlock()
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
				return
			}
			b.tokens--
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
	}
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if i := strings.Index(xf, ","); i > 0 {
			return strings.TrimSpace(xf[:i])
		}
		return strings.TrimSpace(xf)
	}
	return r.RemoteAddr
}

// AuthN — bearer JWT verification (HS256). Demo: kalau tidak ada Authorization,
// lewatkan (untuk endpoint public). Production: paksa di endpoint sensitif.
type AuthnConfig struct {
	Secret    string
	SkipPaths []string // mis. ["/healthz", "/v1/availability", "/v1/payments/midtrans/notification"]
}

// Authn middleware — minimal JWT verification (HS256). Untuk demo kita tidak
// implementasi parser penuh; cukup contoh kerangka untuk presentasi.
func Authn(_ AuthnConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Untuk demo: passthrough. Production: verify JWT signature, expiry, scope.
			// Contoh implementasi penuh ada di docs/runbooks/auth.md
			_ = r
			_ = w
			next.ServeHTTP(w, r)
		})
	}
}

// AsContext — placeholder helper.
func AsContext(_ context.Context) context.Context { return context.TODO() }

var _ = errors.New // placeholder
