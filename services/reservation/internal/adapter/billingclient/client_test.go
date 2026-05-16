package billingclient

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_ValidAddr_CreatesClient — grpc.NewClient doesn't dial eagerly, so
// any valid passthrough/dns target builds a Client successfully.
func TestNew_ValidAddr_CreatesClient(t *testing.T) {
	c, err := New("passthrough:///localhost:50051")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NotNil(t, c.conn)
	require.NotNil(t, c.client)

	// Close releases the underlying conn — must not error.
	assert.NoError(t, c.Close())
}

// TestNew_EmptyAddr_Errors — grpc.NewClient rejects an empty target.
func TestNew_EmptyAddr_Errors(t *testing.T) {
	_, err := New("")
	assert.Error(t, err)
}

// TestClose_NilConn_NoError — defensive: a Client with nil conn should
// not panic and Close should return nil.
func TestClose_NilConn_NoError(t *testing.T) {
	c := &Client{}
	assert.NoError(t, c.Close())
}

// TestClose_Idempotent — calling Close twice should not panic. The second
// call may return an error (already-closed conn) or nil depending on grpc
// version; we just verify it does not crash.
func TestClose_Idempotent(t *testing.T) {
	c, err := New("passthrough:///localhost:0")
	require.NoError(t, err)

	_ = c.Close()
	// second Close — allow either nil or error, but no panic.
	assert.NotPanics(t, func() { _ = c.Close() })
}

// TestNoopChecker_ReturnsZero — fallback never reports overdue invoices.
func TestNoopChecker_ReturnsZero(t *testing.T) {
	n := NoopChecker{}
	got, err := n.CountOverdueByDriverID(context.Background(), "drv-1")
	assert.NoError(t, err)
	assert.Equal(t, 0, got)

	// Also works with empty driver id.
	got, err = n.CountOverdueByDriverID(context.Background(), "")
	assert.NoError(t, err)
	assert.Equal(t, 0, got)
}

// TestCountOverdueByDriverID_BadAddr_ReturnsErr — when the conn target
// can't actually serve RPCs, the call fails fast (DeadlineExceeded /
// Unavailable). We're not asserting the specific code, just that it errors
// rather than returning a bogus count.
//
// Uses a pre-cancelled context to force fast failure regardless of resolver
// timing.
func TestCountOverdueByDriverID_BadAddr_ReturnsErr(t *testing.T) {
	c, err := New("passthrough:///127.0.0.1:1")
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so RPC bails immediately

	got, err := c.CountOverdueByDriverID(ctx, "drv-1")
	assert.Error(t, err)
	assert.Equal(t, 0, got)
}
