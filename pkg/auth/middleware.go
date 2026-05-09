package auth

import (
	"context"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// driverIDKey — context key untuk extracted driver_id.
type ctxKey string

const driverIDCtxKey ctxKey = "driver_id"

// DriverIDFromContext — getter untuk handler downstream.
func DriverIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(driverIDCtxKey).(string); ok {
		return v
	}
	return ""
}

// Middleware — verify Authorization: Bearer <jwt> header.
//
// Behavior:
//   - SkipPaths matched (prefix) → bypass (no auth required).
//   - Required (default): no/invalid token → 401.
//   - Verified: extract claims.Sub → inject ke context + forward header
//     `x-driver-id` ke gRPC metadata (via grpc-gateway header matcher).
//
// Untuk demo, kita pakai PassthroughIfNoToken=true (skip kalau header kosong)
// supaya Postman tanpa JWT tetap bisa test. Production: false untuk enforce.
type Middleware struct {
	Verifier             *Verifier
	SkipPaths            []string // prefix match
	PassthroughIfNoToken bool     // demo mode: kosongin Authorization → bypass
	Logger               *zap.Logger
}

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip paths
		for _, p := range m.SkipPaths {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			if m.PassthroughIfNoToken {
				next.ServeHTTP(w, r)
				return
			}
			writeAuthError(w, "missing Authorization header")
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeAuthError(w, "invalid Authorization scheme; expected Bearer")
			return
		}
		token := strings.TrimSpace(parts[1])

		claims, err := m.Verifier.Verify(token)
		if err != nil {
			if m.Logger != nil {
				m.Logger.Warn("jwt verify failed", zap.Error(err))
			}
			writeAuthError(w, "invalid or expired token")
			return
		}

		// Inject driver_id ke context + header (forwarded ke gRPC metadata).
		ctx := context.WithValue(r.Context(), driverIDCtxKey, claims.Sub)
		r = r.WithContext(ctx)
		r.Header.Set("X-Driver-ID", claims.Sub)

		next.ServeHTTP(w, r)
	})
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="parkirpintar"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","message":"` + msg + `"}`))
}
