package logger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/ajiperdana/parkir-pintar/pkg/logger"
)

// --- New ------------------------------------------------------------------

func TestNew_ProdConfig(t *testing.T) {
	t.Parallel()
	l, err := logger.New("svc", "prod", "info")
	require.NoError(t, err)
	require.NotNil(t, l)
}

func TestNew_DevConfig(t *testing.T) {
	t.Parallel()
	l, err := logger.New("svc", "dev", "debug")
	require.NoError(t, err)
	require.NotNil(t, l)
}

func TestNew_InvalidLevelFallsBackToDefault(t *testing.T) {
	t.Parallel()
	// Invalid level should NOT error — code path: ParseLevel error → skip.
	l, err := logger.New("svc", "prod", "not-a-level")
	require.NoError(t, err)
	require.NotNil(t, l)
}

func TestNew_EmptyLevelStillBuilds(t *testing.T) {
	t.Parallel()
	l, err := logger.New("svc", "dev", "")
	require.NoError(t, err)
	require.NotNil(t, l)
}

// --- WithRequestID / RequestID -------------------------------------------

func TestRequestID_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := logger.WithRequestID(context.Background(), "rid-42")
	assert.Equal(t, "rid-42", logger.RequestID(ctx))
}

func TestRequestID_Missing(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", logger.RequestID(context.Background()))
}

// --- WithTraceID / TraceID -----------------------------------------------

func TestTraceID_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := logger.WithTraceID(context.Background(), "trace-99")
	assert.Equal(t, "trace-99", logger.TraceID(ctx))
}

func TestTraceID_Missing(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", logger.TraceID(context.Background()))
}

// --- WithDriverID --------------------------------------------------------

func TestDriverID_PropagatesViaFromContext(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.InfoLevel)
	base := zap.New(core)
	ctx := logger.WithDriverID(context.Background(), "drv-007")

	l := logger.FromContext(ctx, base)
	l.Info("hello")

	require.Equal(t, 1, recorded.Len())
	fields := recorded.All()[0].ContextMap()
	assert.Equal(t, "drv-007", fields["driver_id"])
}

// --- FromContext ---------------------------------------------------------

func TestFromContext_NoFields_WhenContextEmpty(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.InfoLevel)
	base := zap.New(core)

	l := logger.FromContext(context.Background(), base)
	l.Info("msg")

	require.Equal(t, 1, recorded.Len())
	fields := recorded.All()[0].ContextMap()
	_, hasRID := fields["request_id"]
	_, hasTID := fields["trace_id"]
	_, hasDID := fields["driver_id"]
	assert.False(t, hasRID)
	assert.False(t, hasTID)
	assert.False(t, hasDID)
}

func TestFromContext_AllFieldsPresent(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.InfoLevel)
	base := zap.New(core)

	ctx := context.Background()
	ctx = logger.WithRequestID(ctx, "req-1")
	ctx = logger.WithTraceID(ctx, "tr-2")
	ctx = logger.WithDriverID(ctx, "drv-3")

	l := logger.FromContext(ctx, base)
	l.Info("msg")

	require.Equal(t, 1, recorded.Len())
	fields := recorded.All()[0].ContextMap()
	assert.Equal(t, "req-1", fields["request_id"])
	assert.Equal(t, "tr-2", fields["trace_id"])
	assert.Equal(t, "drv-3", fields["driver_id"])
}

func TestFromContext_OnlyRequestID(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.InfoLevel)
	base := zap.New(core)

	ctx := logger.WithRequestID(context.Background(), "only-rid")
	l := logger.FromContext(ctx, base)
	l.Info("msg")

	fields := recorded.All()[0].ContextMap()
	assert.Equal(t, "only-rid", fields["request_id"])
	_, hasTID := fields["trace_id"]
	_, hasDID := fields["driver_id"]
	assert.False(t, hasTID)
	assert.False(t, hasDID)
}
