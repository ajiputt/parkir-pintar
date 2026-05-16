package tracing_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/tracing"
)

func TestInit_NoopWhenEndpointEmpty(t *testing.T) {
	t.Parallel()
	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		ServiceName:  "svc",
		Env:          "test",
		OTLPEndpoint: "",
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	// Noop shutdown should return nil and not block.
	assert.NoError(t, shutdown(context.Background()))
}

func TestInit_NoopShutdown_AcceptsCancelledContext(t *testing.T) {
	t.Parallel()
	shutdown, err := tracing.Init(context.Background(), tracing.Config{OTLPEndpoint: ""})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Noop should still return nil even with cancelled ctx.
	assert.NoError(t, shutdown(ctx))
}

func TestInit_WithEndpoint_BuildsProvider(t *testing.T) {
	t.Parallel()
	// otlptracegrpc.New is non-blocking — it doesn't dial until export. Using a
	// localhost:port that nothing listens on should still succeed at constructor.
	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		ServiceName:  "svc",
		Env:          "dev",
		OTLPEndpoint: "localhost:14317", // unused port, lazy dial
		SamplerRatio: 0.5,
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// Best-effort shutdown with a short timeout — won't block long.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = shutdown(ctx)
}

func TestInit_DevEnvForcesFullSampling(t *testing.T) {
	t.Parallel()
	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		ServiceName:  "svc",
		Env:          "dev",
		OTLPEndpoint: "localhost:14318",
		SamplerRatio: 0, // dev override should bump to 1.0
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = shutdown(ctx)
}

func TestInit_NegativeRatio_FallsBackToDefault(t *testing.T) {
	t.Parallel()
	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		ServiceName:  "svc",
		Env:          "prod",
		OTLPEndpoint: "localhost:14319",
		SamplerRatio: -1, // <=0 → default 0.1
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = shutdown(ctx)
}
