package idempotency

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrap_FreshKey_CallsFn(t *testing.T) {
	store := NewMemoryStore()
	called := 0
	res, err := Wrap(context.Background(), store, "key1", "create", []byte(`{"a":1}`), time.Minute,
		func(_ context.Context) (int, []byte, error) {
			called++
			return 200, []byte(`{"ok":true}`), nil
		})
	require.NoError(t, err)
	assert.False(t, res.FromCache)
	assert.Equal(t, 1, called)
}

func TestWrap_RepeatSameKeySamePayload_ReturnsCached(t *testing.T) {
	store := NewMemoryStore()
	var called int32
	fn := func(_ context.Context) (int, []byte, error) {
		atomic.AddInt32(&called, 1)
		return 201, []byte(`{"id":"a"}`), nil
	}

	first, err := Wrap(context.Background(), store, "k", "op", []byte(`{"x":1}`), time.Minute, fn)
	require.NoError(t, err)
	assert.False(t, first.FromCache)

	second, err := Wrap(context.Background(), store, "k", "op", []byte(`{"x":1}`), time.Minute, fn)
	require.NoError(t, err)
	assert.True(t, second.FromCache)
	assert.Equal(t, []byte(`{"id":"a"}`), second.Body)
	assert.Equal(t, int32(1), atomic.LoadInt32(&called), "fn should only run once")
}

func TestWrap_SameKeyDifferentPayload_ReturnsConflict(t *testing.T) {
	store := NewMemoryStore()
	fn := func(_ context.Context) (int, []byte, error) { return 200, []byte("ok"), nil }
	_, err := Wrap(context.Background(), store, "k", "op", []byte(`{"x":1}`), time.Minute, fn)
	require.NoError(t, err)

	_, err = Wrap(context.Background(), store, "k", "op", []byte(`{"x":2}`), time.Minute, fn)
	assert.ErrorIs(t, err, ErrConflict)
}

func TestWrap_FailedFn_DoesNotCache(t *testing.T) {
	store := NewMemoryStore()
	customErr := errors.New("boom")
	fn := func(_ context.Context) (int, []byte, error) { return 500, nil, customErr }

	_, err := Wrap(context.Background(), store, "k", "op", []byte("x"), time.Minute, fn)
	assert.ErrorIs(t, err, customErr)

	rec, err := store.Get(context.Background(), "op:k")
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, rec.Status)
}

func TestWrap_NoKey_PassesThrough(t *testing.T) {
	store := NewMemoryStore()
	called := 0
	for i := 0; i < 3; i++ {
		_, err := Wrap(context.Background(), store, "", "op", []byte("x"), time.Minute,
			func(_ context.Context) (int, []byte, error) { called++; return 200, nil, nil })
		require.NoError(t, err)
	}
	assert.Equal(t, 3, called)
}

func TestHash_Stable(t *testing.T) {
	a := Hash([]byte(`{"x":1}`))
	b := Hash([]byte(`{"x":1}`))
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, Hash([]byte(`{"x":2}`)))
}
