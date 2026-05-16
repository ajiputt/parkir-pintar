// Package closeutil provides small helpers untuk gracefully close io.Closer
// resources di defer statement, dengan error logging optional.
//
// Motivation: `defer x.Close()` flagged by errcheck linter karena return value
// di-ignore. Wrapping inline (`defer func() { _ = x.Close() }()`) berulang di
// banyak file menyebabkan code duplication (Sonar flag). Helper ini sentralisasi
// pattern jadi: `defer closeutil.Quiet(x)`.
package closeutil

import (
	"io"

	"go.uber.org/zap"
)

// Quiet menutup c dan diam-diam discard error. Pakai untuk resource yang
// failure-on-close tidak actionable (e.g., HTTP response body, redis client
// shutdown di service teardown).
//
// Usage:
//
//	defer closeutil.Quiet(rdb)
//	defer closeutil.Quiet(resp.Body)
func Quiet(c io.Closer) {
	if c == nil {
		return
	}
	_ = c.Close()
}

// Log menutup c dan log error via zap kalau Close() gagal. Pakai untuk resource
// yang failure-on-close perlu di-surface (e.g., DB connection di shutdown,
// audit log).
//
// Usage:
//
//	defer closeutil.Log(rdb, log, "redis-client")
func Log(c io.Closer, log *zap.Logger, resource string) {
	if c == nil {
		return
	}
	if err := c.Close(); err != nil && log != nil {
		log.Warn("close failed", zap.String("resource", resource), zap.Error(err))
	}
}
