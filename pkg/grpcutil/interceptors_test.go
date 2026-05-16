package grpcutil_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ajiperdana/parkir-pintar/pkg/grpcutil"
	"github.com/ajiperdana/parkir-pintar/pkg/logger"
)

// ---------- server-side helpers ----------

func newUnaryInfo(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

func okHandler(resp any) grpc.UnaryHandler {
	return func(ctx context.Context, req any) (any, error) {
		return resp, nil
	}
}

// ---------- RecoveryUnary ----------

func TestRecoveryUnary_NoPanic_ForwardsResponse(t *testing.T) {
	core, _ := observer.New(zap.ErrorLevel)
	log := zap.New(core)

	interceptor := grpcutil.RecoveryUnary(log)
	resp, err := interceptor(context.Background(), "req", newUnaryInfo("/svc.X/Foo"), okHandler("hello"))

	require.NoError(t, err)
	assert.Equal(t, "hello", resp)
}

func TestRecoveryUnary_RecoversPanic(t *testing.T) {
	core, recorded := observer.New(zap.ErrorLevel)
	log := zap.New(core)

	panicHandler := grpc.UnaryHandler(func(ctx context.Context, req any) (any, error) {
		panic("boom")
	})

	interceptor := grpcutil.RecoveryUnary(log)
	resp, err := interceptor(context.Background(), "req", newUnaryInfo("/svc.X/Boom"), panicHandler)

	require.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())

	// We logged at least one Error-level entry mentioning the method.
	entries := recorded.All()
	require.NotEmpty(t, entries)
	assert.Contains(t, entries[0].Message, "panic recovered")
}

func TestRecoveryUnary_HandlerError_PassedThrough(t *testing.T) {
	log := zap.NewNop()
	interceptor := grpcutil.RecoveryUnary(log)

	wantErr := status.Error(codes.NotFound, "missing")
	h := grpc.UnaryHandler(func(ctx context.Context, req any) (any, error) {
		return nil, wantErr
	})

	resp, err := interceptor(context.Background(), "req", newUnaryInfo("/svc.X/Y"), h)
	assert.Nil(t, resp)
	assert.Equal(t, wantErr, err)
}

// ---------- RequestIDUnary ----------

func TestRequestIDUnary_UsesIncomingHeader(t *testing.T) {
	interceptor := grpcutil.RequestIDUnary()

	md := metadata.New(map[string]string{grpcutil.HeaderRequestID: "rid-incoming"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var seenRID string
	h := grpc.UnaryHandler(func(ctx context.Context, req any) (any, error) {
		seenRID = logger.RequestID(ctx)
		return "ok", nil
	})

	resp, err := interceptor(ctx, "req", newUnaryInfo("/svc.X/Y"), h)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
	assert.Equal(t, "rid-incoming", seenRID)
}

func TestRequestIDUnary_GeneratesWhenMissing(t *testing.T) {
	interceptor := grpcutil.RequestIDUnary()

	// No incoming metadata.
	ctx := context.Background()

	var seenRID string
	h := grpc.UnaryHandler(func(ctx context.Context, req any) (any, error) {
		seenRID = logger.RequestID(ctx)
		return nil, nil
	})

	_, err := interceptor(ctx, "req", newUnaryInfo("/svc.X/Y"), h)
	require.NoError(t, err)
	assert.NotEmpty(t, seenRID, "should generate a request id")
	// UUID v4 has 36 chars including dashes.
	assert.Len(t, seenRID, 36)
}

// ---------- LoggingUnary ----------

func TestLoggingUnary_LogsRPCOK(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	log := zap.New(core)
	interceptor := grpcutil.LoggingUnary(log)

	resp, err := interceptor(context.Background(), "req", newUnaryInfo("/svc.X/Foo"), okHandler("response"))
	require.NoError(t, err)
	assert.Equal(t, "response", resp)

	infoEntries := recorded.FilterMessage("rpc ok").All()
	require.NotEmpty(t, infoEntries, "expected rpc ok log entry")
	// Verify method field is attached.
	entry := infoEntries[0]
	found := false
	for _, f := range entry.Context {
		if f.Key == "method" && f.String == "/svc.X/Foo" {
			found = true
			break
		}
	}
	assert.True(t, found, "method field should be attached: %#v", entry.Context)
}

func TestLoggingUnary_LogsClientError(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	log := zap.New(core)
	interceptor := grpcutil.LoggingUnary(log)

	h := grpc.UnaryHandler(func(ctx context.Context, req any) (any, error) {
		return nil, status.Error(codes.NotFound, "missing")
	})

	_, err := interceptor(context.Background(), "req", newUnaryInfo("/svc.X/Foo"), h)
	require.Error(t, err)

	warnEntries := recorded.FilterMessage("rpc client-error").All()
	assert.NotEmpty(t, warnEntries, "expected rpc client-error log entry for NotFound")
}

func TestLoggingUnary_LogsServerError(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	log := zap.New(core)
	interceptor := grpcutil.LoggingUnary(log)

	h := grpc.UnaryHandler(func(ctx context.Context, req any) (any, error) {
		return nil, status.Error(codes.Internal, "boom")
	})

	_, err := interceptor(context.Background(), "req", newUnaryInfo("/svc.X/Foo"), h)
	require.Error(t, err)

	errEntries := recorded.FilterMessage("rpc error").All()
	assert.NotEmpty(t, errEntries, "expected rpc error log entry for Internal")
}

// ---------- IdempotencyKeyFromContext ----------

func TestIdempotencyKeyFromContext_Present(t *testing.T) {
	md := metadata.New(map[string]string{grpcutil.HeaderIdempotencyKey: "idem-1"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	assert.Equal(t, "idem-1", grpcutil.IdempotencyKeyFromContext(ctx))
}

func TestIdempotencyKeyFromContext_Missing(t *testing.T) {
	assert.Equal(t, "", grpcutil.IdempotencyKeyFromContext(context.Background()))
}

// ---------- TimeoutUnary (client) ----------

func TestTimeoutUnary_AppliesTimeout(t *testing.T) {
	interceptor := grpcutil.TimeoutUnary(50 * time.Millisecond)

	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		// Verify deadline is set on ctx.
		_, ok := ctx.Deadline()
		if !ok {
			return errors.New("expected deadline")
		}
		return nil
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	require.NoError(t, err)
}

func TestTimeoutUnary_PropagatesInvokerError(t *testing.T) {
	interceptor := grpcutil.TimeoutUnary(time.Second)
	wantErr := errors.New("invoker failure")
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return wantErr
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	assert.Equal(t, wantErr, err)
}

// ---------- RetryUnary (client) ----------

func TestRetryUnary_NoRetryOnSuccess(t *testing.T) {
	interceptor := grpcutil.RetryUnary(3)
	var calls atomic.Int32
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		calls.Add(1)
		return nil
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRetryUnary_NoRetryOnNonRetryable(t *testing.T) {
	interceptor := grpcutil.RetryUnary(3)
	var calls atomic.Int32
	nonRetryable := status.Error(codes.NotFound, "missing")
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		calls.Add(1)
		return nonRetryable
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	assert.Equal(t, nonRetryable, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRetryUnary_RetriesOnRetryable(t *testing.T) {
	interceptor := grpcutil.RetryUnary(3)
	var calls atomic.Int32
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		n := calls.Add(1)
		if n < 3 {
			return status.Error(codes.Unavailable, "try again")
		}
		return nil
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	require.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
}

func TestRetryUnary_GivesUpAfterMaxAttempts(t *testing.T) {
	interceptor := grpcutil.RetryUnary(2)
	var calls atomic.Int32
	retryable := status.Error(codes.Unavailable, "down")
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		calls.Add(1)
		return retryable
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	require.Error(t, err)
	assert.Equal(t, int32(2), calls.Load())
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestRetryUnary_RespectsContextCancellation(t *testing.T) {
	interceptor := grpcutil.RetryUnary(5)
	var calls atomic.Int32
	retryable := status.Error(codes.Unavailable, "down")

	ctx, cancel := context.WithCancel(context.Background())
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		calls.Add(1)
		// Cancel after first call so the retry loop bails out at the select.
		cancel()
		return retryable
	})

	err := interceptor(ctx, "/svc.X/Y", nil, nil, nil, invoker)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	// At most 1 attempt, since cancel happens inside the first call.
	assert.Equal(t, int32(1), calls.Load())
}

func TestRetryUnary_NonStatusErrorIsNotRetried(t *testing.T) {
	interceptor := grpcutil.RetryUnary(3)
	var calls atomic.Int32
	plain := errors.New("non-status error")
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		calls.Add(1)
		return plain
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	// Note: status.FromError on a non-status error returns (Unknown, true),
	// so isRetryable returns false for Unknown. Should not retry.
	assert.Equal(t, plain, err)
	assert.Equal(t, int32(1), calls.Load())
}

// ---------- NewBreaker / BreakerUnary ----------

func TestNewBreaker_Defaults(t *testing.T) {
	b := grpcutil.NewBreaker("test-svc")
	require.NotNil(t, b)
}

func TestBreakerUnary_Success(t *testing.T) {
	b := grpcutil.NewBreaker("ok-svc")
	interceptor := grpcutil.BreakerUnary(b)

	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return nil
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	require.NoError(t, err)
}

func TestBreakerUnary_PassesThroughError(t *testing.T) {
	b := grpcutil.NewBreaker("err-svc")
	interceptor := grpcutil.BreakerUnary(b)

	wantErr := status.Error(codes.NotFound, "missing")
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return wantErr
	})

	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	assert.Equal(t, wantErr, err)
}

func TestBreakerUnary_OpensAfterFailures(t *testing.T) {
	// Default settings: ReadyToTrip when ConsecutiveFailures > 5.
	// We need 6 failures to trip. After tripping, the next call hits ErrOpenState.
	b := grpcutil.NewBreaker("trip-svc")
	interceptor := grpcutil.BreakerUnary(b)

	failErr := status.Error(codes.Internal, "boom")
	invoker := grpc.UnaryInvoker(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return failErr
	})

	// 6 failures to trip.
	for i := 0; i < 6; i++ {
		_ = interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	}

	// Next call should see breaker open → mapped to codes.Unavailable.
	err := interceptor(context.Background(), "/svc.X/Y", nil, nil, nil, invoker)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
	assert.Contains(t, st.Message(), "circuit open")
}
