// Package redis — adapter wrapper untuk pkg/lock.
package redis

import (
	"context"
	"time"

	"github.com/ajiperdana/parkir-pintar/pkg/lock"
)

// Locker — adapter implementing usecase.Locker (return release func, bukan Handle).
type Locker struct {
	inner lock.Locker
}

func NewLocker(inner lock.Locker) *Locker {
	return &Locker{inner: inner}
}

func (l *Locker) Acquire(ctx context.Context, key string, ttl time.Duration) (release func(context.Context) error, ok bool, err error) {
	h, ok, err := l.inner.Acquire(ctx, key, ttl)
	if err != nil || !ok {
		return func(context.Context) error { return nil }, ok, err
	}
	return h.Release, true, nil
}
