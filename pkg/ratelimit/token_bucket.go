// Package ratelimit — Redis-based distributed token bucket rate limiter.
//
// Pattern: per-key bucket di Redis hash. Refill rate (tokens/sec) + capacity
// (burst). Atomic refill+decrement via Lua script — multi-instance gateway
// safe.
//
// Auto-cleanup via TTL (default 1 jam). No memory leak.
//
// Lihat ADR-0015 untuk design detail dan trade-off vs in-memory limiter.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter — Redis-based token bucket.
type Limiter struct {
	client redis.Cmdable
	script *redis.Script
}

// luaScript — atomic refill + decrement.
//
// KEYS[1] = bucket key (mis. "ratelimit:reservations:ip:127.0.0.1")
// ARGV[1] = rate (tokens per second)
// ARGV[2] = capacity (max tokens / burst)
// ARGV[3] = now (unix milliseconds)
//
// Returns:
//
//	{1, tokens_left}  — allowed
//	{0, retry_after_sec} — rate limited
const luaScript = `
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])

local data = redis.call('HMGET', key, 'tokens', 'last_fill')
local tokens = tonumber(data[1])
local last_fill = tonumber(data[2])

if tokens == nil then
    tokens = capacity
    last_fill = now_ms
end

local elapsed_sec = (now_ms - last_fill) / 1000.0
if elapsed_sec < 0 then elapsed_sec = 0 end

tokens = math.min(capacity, tokens + elapsed_sec * rate)

if tokens < 1 then
    -- Rate limited. Compute retry_after.
    local need = 1 - tokens
    local retry_sec = math.ceil(need / rate)
    if retry_sec < 1 then retry_sec = 1 end
    redis.call('HMSET', key, 'tokens', tokens, 'last_fill', now_ms)
    redis.call('EXPIRE', key, 3600)
    return {0, retry_sec}
end

tokens = tokens - 1
redis.call('HMSET', key, 'tokens', tokens, 'last_fill', now_ms)
redis.call('EXPIRE', key, 3600)
return {1, tokens}
`

// New — construct limiter dengan loaded Lua script.
func New(client redis.Cmdable) *Limiter {
	return &Limiter{
		client: client,
		script: redis.NewScript(luaScript),
	}
}

// Decision — outcome dari Check.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration // diisi kalau Allowed=false
	Remaining  int           // tokens tersisa kalau Allowed=true
}

// Check — atomic refill + decrement. Aman di-call concurrent.
//
// rate: tokens/second (sustained rate)
// capacity: max tokens (burst limit)
//
// Kalau Redis error, fail-open (Allowed=true) untuk avoid deny-of-service kalau
// Redis flap. Trade-off: short window of unlimited request saat outage.
// Production preference: configure via env, default fail-open.
func (l *Limiter) Check(ctx context.Context, key string, rate, capacity int) (*Decision, error) {
	now := time.Now().UnixMilli()
	res, err := l.script.Run(ctx, l.client, []string{key},
		rate, capacity, now).Result()
	if err != nil {
		return &Decision{Allowed: true}, fmt.Errorf("ratelimit redis: %w", err)
	}

	arr, ok := res.([]interface{})
	if !ok || len(arr) < 2 {
		return &Decision{Allowed: true}, fmt.Errorf("ratelimit unexpected response: %v", res)
	}

	allowed, _ := arr[0].(int64)
	second, _ := arr[1].(int64)

	if allowed == 1 {
		return &Decision{Allowed: true, Remaining: int(second)}, nil
	}
	return &Decision{Allowed: false, RetryAfter: time.Duration(second) * time.Second}, nil
}
