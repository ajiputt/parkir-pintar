package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/ratelimit"
)

// unreachableClient — Redis client pointing to closed port. Any Eval/EvalSha
// call returns a connection error → exercise fail-open path in Limiter.Check.
//
// Port 1 (tcpmux) is rarely listened to and connections refuse immediately on
// both Linux and Windows; combined with tight timeouts this keeps the test
// well under a second.
func unreachableClient(t *testing.T) *redis.Client {
	t.Helper()
	return redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond,
		MaxRetries:  -1, // disable retries — fail fast
	})
}

func TestNew_ReturnsNonNilLimiter(t *testing.T) {
	t.Parallel()
	rdb := unreachableClient(t)
	defer func() { _ = rdb.Close() }()

	l := ratelimit.New(rdb)
	assert.NotNil(t, l)
}

// Redis unreachable → fail-open: Allowed=true with non-nil error.
// ADR-0015 trade-off — avoid deny-of-service kalau Redis flap.
func TestCheck_FailsOpen_OnRedisError(t *testing.T) {
	t.Parallel()
	rdb := unreachableClient(t)
	defer func() { _ = rdb.Close() }()

	l := ratelimit.New(rdb)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	dec, err := l.Check(ctx, "ratelimit:test:fail-open", 10, 20)

	require.NotNil(t, dec)
	assert.True(t, dec.Allowed, "fail-open: must allow when Redis is down")
	assert.Error(t, err, "fail-open: should still surface the underlying error")
}

func TestDecision_StructDefaults(t *testing.T) {
	t.Parallel()
	d := &ratelimit.Decision{}
	assert.False(t, d.Allowed)
	assert.Equal(t, time.Duration(0), d.RetryAfter)
	assert.Equal(t, 0, d.Remaining)
}
