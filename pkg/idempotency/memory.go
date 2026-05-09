package idempotency

import (
	"context"
	"sync"
	"time"
)

// MemoryStore — in-memory implementasi untuk testing dan dev local.
// Production wajib pakai Postgres atau Redis store.
type MemoryStore struct {
	mu    sync.Mutex
	items map[string]*Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]*Record)}
}

func (m *MemoryStore) Begin(_ context.Context, key, hash string, ttl time.Duration) (*Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.items[key]; ok {
		// Cek expiry
		if time.Now().After(rec.ExpiresAt) {
			delete(m.items, key)
		} else {
			return rec, true, nil
		}
	}
	now := time.Now()
	rec := &Record{
		Key:         key,
		RequestHash: hash,
		Status:      StatusProcessing,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
	m.items[key] = rec
	return nil, false, nil
}

func (m *MemoryStore) Complete(_ context.Context, key string, status int, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.items[key]
	if !ok {
		return ErrKeyNotFound
	}
	rec.Status = StatusCompleted
	rec.ResponseStatus = status
	rec.ResponseBody = append([]byte(nil), body...)
	return nil
}

func (m *MemoryStore) Fail(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.items[key]; ok {
		rec.Status = StatusFailed
	}
	return nil
}

func (m *MemoryStore) Get(_ context.Context, key string) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.items[key]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return rec, nil
}
