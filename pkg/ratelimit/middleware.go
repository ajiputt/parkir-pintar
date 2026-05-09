package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// Middleware — HTTP middleware factory. Per-endpoint rate limit via Redis.
//
// Headers di response:
//   - X-RateLimit-Limit       : capacity (burst)
//   - X-RateLimit-Remaining   : tokens left
//   - Retry-After             : seconds untuk wait (saat 429)
func Middleware(limiter *Limiter, cfg Config, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ep := cfg.match(r.Method, r.URL.Path)
			keyValue := extractKey(r, ep.Key)

			redisKey := fmt.Sprintf("ratelimit:%s:%s:%s:%s",
				strings.ToLower(ep.Method), strings.ReplaceAll(ep.Pattern, "/", "_"),
				ep.Key, keyValue)

			decision, err := limiter.Check(r.Context(), redisKey, ep.RPS, ep.Burst)
			if err != nil {
				// Fail-open: log warning tapi proceed. Lihat ADR-0015 trade-off.
				log.Warn("ratelimit redis error — fail open", zap.Error(err), zap.String("key", redisKey))
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(ep.Burst))

			if !decision.Allowed {
				retrySec := int(decision.RetryAfter.Seconds())
				if retrySec < 1 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retrySec))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(fmt.Sprintf(
					`{"error":"rate_limited","message":"too many requests","retry_after":%d}`,
					retrySec)))
				log.Info("rate limited",
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method),
					zap.String("key", keyValue),
					zap.Int("retry_after_sec", retrySec))
				return
			}

			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(decision.Remaining))
			next.ServeHTTP(w, r)
		})
	}
}

// extractKey — derive rate-limit key dari request.
//
// "ip"        → clientIP (X-Forwarded-For atau RemoteAddr)
// "driver_id" → X-Driver-ID header (di-inject oleh pkg/auth middleware
//
//	setelah JWT verify); fallback ke IP kalau header absent
//	(unauthenticated atau passthrough mode).
//
// default     → ip
//
// Catatan: future refactor — baca via auth.DriverIDFromContext(ctx)
// instead of header, supaya tidak bergantung implementasi auth middleware
// (more idiomatic Go context propagation). Header-based saat ini bekerja
// karena auth middleware run sebelum ratelimit di chain (lihat gateway/cmd/main.go).
func extractKey(r *http.Request, source string) string {
	switch source {
	case "driver_id":
		if id := r.Header.Get("X-Driver-ID"); id != "" {
			return id
		}
		return clientIP(r)
	default:
		return clientIP(r)
	}
}

// clientIP — best-effort extract client IP.
func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if i := strings.Index(xf, ","); i > 0 {
			return strings.TrimSpace(xf[:i])
		}
		return strings.TrimSpace(xf)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	// RemoteAddr is host:port
	if i := strings.LastIndex(r.RemoteAddr, ":"); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
