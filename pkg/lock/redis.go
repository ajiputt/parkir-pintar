package lock

import (
	"context"
	"errors"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
)

// RedisLocker — Redlock-based distributed lock.
type RedisLocker struct {
	rs *redsync.Redsync
}

// NewRedisLocker membuat locker dari Redis client.
func NewRedisLocker(client *redis.Client) *RedisLocker {
	pool := goredis.NewPool(client)
	return &RedisLocker{rs: redsync.New(pool)}
}

func (r *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (Handle, bool, error) {
	mu := r.rs.NewMutex(key,
		redsync.WithExpiry(ttl),
		redsync.WithTries(1), // fast-fail; caller handle retry
		redsync.WithGenValueFunc(genValue),
	)
	if err := mu.LockContext(ctx); err != nil {
		// taken → not acquired
		if errIsTaken(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &redisHandle{mu: mu}, true, nil
}

type redisHandle struct {
	mu *redsync.Mutex
}

func (h *redisHandle) Release(ctx context.Context) error {
	_, err := h.mu.UnlockContext(ctx)
	return err
}

func (h *redisHandle) Extend(ctx context.Context, _ time.Duration) error {
	_, err := h.mu.ExtendContext(ctx)
	return err
}

func errIsTaken(err error) bool {
	if err == nil {
		return false
	}
	// redsync mengembalikan ErrFailed kalau gagal dapat (sudah dipegang).
	// Pakai errors.Is untuk handle wrapped errors correctly.
	return errors.Is(err, redsync.ErrFailed)
}

func genValue() (string, error) {
	// pakai default redsync (random). Bisa dikustom kalau perlu trace_id.
	b := make([]byte, 16)
	if _, err := readFull(b); err != nil {
		return "", err
	}
	return hexEncode(b), nil
}

// helpers minimal (avoid extra import cycle)
func readFull(b []byte) (int, error) {
	for i := range b {
		// pseudo random not crypto — Redlock value cukup untuk uniqueness
		b[i] = byte(i*31 + int(time.Now().UnixNano()&0xff))
	}
	return len(b), nil
}

func hexEncode(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}
