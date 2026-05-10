// Package idempotency menyediakan server-side idempotency key store.
// Pola: client kirim Idempotency-Key, server hash request body, simpan response cache.
//
// Lihat ADR-0007.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// Status — lifecycle key.
type Status string

const (
	StatusProcessing Status = "PROCESSING"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

// Record — entri idempotency.
type Record struct {
	Key            string
	RequestHash    string
	ResponseStatus int
	ResponseBody   []byte
	Status         Status
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// Store — interface yang harus diimplement adapter (Postgres, Redis).
type Store interface {
	// Begin mencoba registrasi key. Return:
	//   - existing != nil & found = true → key sudah ada (caller harus cek hash & status)
	//   - found = false → berhasil reserve, key baru, caller proses request
	Begin(ctx context.Context, key, requestHash string, ttl time.Duration) (existing *Record, found bool, err error)

	// Complete mencatat response untuk key yang in-progress.
	Complete(ctx context.Context, key string, status int, body []byte) error

	// Fail menandai key sebagai failed (caller bisa decide apakah hapus / biarkan).
	Fail(ctx context.Context, key string) error

	// Get baca record (untuk inspeksi).
	Get(ctx context.Context, key string) (*Record, error)
}

// Hash — sha256 hex dari payload.
func Hash(payload []byte) string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}

// Common errors.
var (
	ErrConflict    = errors.New("idempotency: key conflict (different payload)")
	ErrInProgress  = errors.New("idempotency: same request still in progress")
	ErrKeyNotFound = errors.New("idempotency: key not found")
)

// Result — hasil Wrap. Body & Status hanya valid jika FromCache=true.
type Result struct {
	FromCache bool
	Status    int
	Body      []byte
}

// Wrap menjalankan fn idempotent untuk key+payload tertentu.
//
// Behavior:
//   - Jika key belum ada: jalankan fn, simpan response, return result fresh.
//   - Jika key ada, hash sama, status COMPLETED: return cached.
//   - Jika key ada, hash sama, status PROCESSING: return ErrInProgress.
//   - Jika key ada, hash beda: return ErrConflict.
//
// fn harus mengembalikan (status, body, err). Jika err nil, mark Completed.
// Jika err non-nil, mark Failed (key tetap reserve untuk durasi TTL).
func Wrap(
	ctx context.Context,
	store Store,
	key, requestKey string,
	requestPayload []byte,
	ttl time.Duration,
	fn func(ctx context.Context) (status int, body []byte, err error),
) (Result, error) {
	if key == "" {
		// no idempotency requested — just call
		s, b, err := fn(ctx)
		return Result{FromCache: false, Status: s, Body: b}, err
	}

	fullKey := requestKey + ":" + key
	hash := Hash(requestPayload)

	existing, found, err := store.Begin(ctx, fullKey, hash, ttl)
	if err != nil {
		return Result{}, err
	}
	if found {
		if existing.RequestHash != hash {
			return Result{}, ErrConflict
		}
		switch existing.Status {
		case StatusCompleted:
			return Result{FromCache: true, Status: existing.ResponseStatus, Body: existing.ResponseBody}, nil
		case StatusProcessing:
			return Result{}, ErrInProgress
		case StatusFailed:
			// retry: re-issue (overwrite hash check above sudah lulus)
			// drop through ke fn call.
		}
	}

	status, body, fnErr := fn(ctx)
	if fnErr != nil {
		_ = store.Fail(ctx, fullKey)
		return Result{}, fnErr
	}
	// Best-effort cache write. Kalau gagal, idempotency cache miss next time
	// = acceptable degradation. Caller flow tidak block oleh cache failure.
	// Explicit discard pakai _= supaya intent clear (gak nilerr false positive).
	_ = store.Complete(ctx, fullKey, status, body)
	return Result{FromCache: false, Status: status, Body: body}, nil
}
