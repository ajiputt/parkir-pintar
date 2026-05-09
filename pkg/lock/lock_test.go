package lock

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryLocker_AcquireRelease(t *testing.T) {
	l := NewMemoryLocker()
	h, ok, err := l.Acquire(context.Background(), "k", time.Second)
	require.NoError(t, err)
	require.True(t, ok)

	_, ok2, err := l.Acquire(context.Background(), "k", time.Second)
	require.NoError(t, err)
	assert.False(t, ok2, "second acquire must fail")

	require.NoError(t, h.Release(context.Background()))

	_, ok3, err := l.Acquire(context.Background(), "k", time.Second)
	require.NoError(t, err)
	assert.True(t, ok3, "after release must succeed")
}

func TestMemoryLocker_ExpiredAutoRelease(t *testing.T) {
	l := NewMemoryLocker()
	_, ok, err := l.Acquire(context.Background(), "k", 10*time.Millisecond)
	require.NoError(t, err)
	require.True(t, ok)

	time.Sleep(20 * time.Millisecond)

	_, ok2, err := l.Acquire(context.Background(), "k", time.Second)
	require.NoError(t, err)
	assert.True(t, ok2)
}

func TestWithLock_NotAcquired(t *testing.T) {
	l := NewMemoryLocker()
	_, _, _ = l.Acquire(context.Background(), "k", time.Second)
	err := WithLock(context.Background(), l, "k", time.Second, func(_ context.Context) error {
		t.Fatal("should not run")
		return nil
	})
	assert.ErrorIs(t, err, ErrNotAcquired)
}

// Concurrency: 50 goroutines race, exactly 1 should win the lock at a time.
func TestMemoryLocker_Concurrent(t *testing.T) {
	l := NewMemoryLocker()
	var inside int32
	var maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, ok, _ := l.Acquire(context.Background(), "k", time.Second)
			if !ok {
				return
			}
			defer h.Release(context.Background())
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&inside, -1)
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, atomic.LoadInt32(&maxInside), int32(1), "at most 1 holder at a time")
}
