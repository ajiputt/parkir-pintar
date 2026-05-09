package lock

import (
	"context"
	"sync"
	"time"
)

// MemoryLocker — in-memory locker untuk testing & dev tanpa Redis.
type MemoryLocker struct {
	mu    sync.Mutex
	locks map[string]time.Time
}

func NewMemoryLocker() *MemoryLocker {
	return &MemoryLocker{locks: make(map[string]time.Time)}
}

func (m *MemoryLocker) Acquire(_ context.Context, key string, ttl time.Duration) (Handle, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if exp, ok := m.locks[key]; ok && time.Now().Before(exp) {
		return nil, false, nil
	}
	m.locks[key] = time.Now().Add(ttl)
	return &memHandle{m: m, key: key}, true, nil
}

type memHandle struct {
	m   *MemoryLocker
	key string
}

func (h *memHandle) Release(_ context.Context) error {
	h.m.mu.Lock()
	delete(h.m.locks, h.key)
	h.m.mu.Unlock()
	return nil
}

func (h *memHandle) Extend(_ context.Context, ttl time.Duration) error {
	h.m.mu.Lock()
	if _, ok := h.m.locks[h.key]; ok {
		h.m.locks[h.key] = time.Now().Add(ttl)
	}
	h.m.mu.Unlock()
	return nil
}
