// Package lock — distributed lock interface.
// Default impl: Redis (Redlock single-instance untuk demo, multi untuk production).
package lock

import (
	"context"
	"errors"
	"time"
)

// Locker abstraction. Implementasi harus thread-safe.
type Locker interface {
	// Acquire mencoba lock dengan TTL. ok=false kalau gagal (sudah dipegang).
	// Caller wajib panggil Release walaupun ok=false (no-op).
	Acquire(ctx context.Context, key string, ttl time.Duration) (Handle, bool, error)
}

// Handle — token untuk release.
type Handle interface {
	// Release lepaskan lock. Idempotent.
	Release(ctx context.Context) error
	// Extend perpanjang TTL (opsional, untuk long-running operation).
	Extend(ctx context.Context, ttl time.Duration) error
}

// ErrNotAcquired — convenience error untuk caller yang ingin pakai errors.Is.
var ErrNotAcquired = errors.New("lock: not acquired")

// WithLock — helper: acquire, jalankan fn, release.
// Jika gagal acquire, return ErrNotAcquired wrapped.
func WithLock(ctx context.Context, l Locker, key string, ttl time.Duration, fn func(ctx context.Context) error) error {
	h, ok, err := l.Acquire(ctx, key, ttl)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAcquired
	}
	defer func() { _ = h.Release(ctx) }()
	return fn(ctx)
}
