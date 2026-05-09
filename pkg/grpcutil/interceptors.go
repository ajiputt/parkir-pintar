// Package grpcutil — gRPC interceptor library:
// recovery, logging, request_id, timeout, retry, circuit breaker, tracing.
//
// Pakai di server:
//
//	grpcServer := grpc.NewServer(
//	  grpc.ChainUnaryInterceptor(
//	    grpcutil.RecoveryUnary(logger),
//	    grpcutil.RequestIDUnary(),
//	    grpcutil.LoggingUnary(logger),
//	  ),
//	)
//
// Pakai di client (untuk service-to-service):
//
//	conn, _ := grpc.Dial(addr, grpc.WithChainUnaryInterceptor(
//	  grpcutil.TimeoutUnary(2*time.Second),
//	  grpcutil.RetryUnary(3),
//	  grpcutil.BreakerUnary(breaker),
//	))
package grpcutil

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ajiperdana/parkir-pintar/pkg/logger"
)

const (
	HeaderRequestID      = "x-request-id"
	HeaderIdempotencyKey = "x-idempotency-key"
)

// RecoveryUnary — convert panic ke gRPC Internal error + log stack.
func RecoveryUnary(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered",
					zap.String("method", info.FullMethod),
					zap.Any("panic", r),
					zap.ByteString("stack", debug.Stack()),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// RequestIDUnary — propagate / generate request id, simpan di context.
func RequestIDUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		var rid string
		if vals := md.Get(HeaderRequestID); len(vals) > 0 {
			rid = vals[0]
		} else {
			rid = uuid.NewString()
		}
		ctx = logger.WithRequestID(ctx, rid)
		// echo ke response
		_ = grpc.SetHeader(ctx, metadata.Pairs(HeaderRequestID, rid))
		return handler(ctx, req)
	}
}

// LoggingUnary — log start/stop tiap RPC dengan latency.
func LoggingUnary(base *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		log := logger.FromContext(ctx, base).With(zap.String("method", info.FullMethod))
		log.Debug("rpc start")

		resp, err := handler(ctx, req)
		dur := time.Since(start)

		fields := []zap.Field{zap.Duration("latency", dur)}
		st, _ := status.FromError(err)
		fields = append(fields, zap.String("code", st.Code().String()))

		switch {
		case err == nil:
			log.Info("rpc ok", fields...)
		case st.Code() >= codes.Internal:
			log.Error("rpc error", append(fields, zap.Error(err))...)
		default:
			log.Warn("rpc client-error", append(fields, zap.Error(err))...)
		}
		return resp, err
	}
}

// IdempotencyKeyFromContext — ambil header idempotency key.
func IdempotencyKeyFromContext(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if vals := md.Get(HeaderIdempotencyKey); len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// TimeoutUnary — set timeout untuk client call.
func TimeoutUnary(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// RetryUnary — naive retry untuk error retryable. Backoff: 50ms, 100ms, 200ms.
func RetryUnary(maxAttempts int) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var lastErr error
		backoff := 50 * time.Millisecond
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			lastErr = invoker(ctx, method, req, reply, cc, opts...)
			if lastErr == nil || !isRetryable(lastErr) {
				return lastErr
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		return lastErr
	}
}

func isRetryable(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted:
		return true
	}
	return false
}

// NewBreaker — convenience factory untuk gobreaker.
func NewBreaker(name string) *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: 5,
		Interval:    30 * time.Second,
		Timeout:     10 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
	})
}

// BreakerUnary — wrap gRPC client call dengan circuit breaker.
func BreakerUnary(b *gobreaker.CircuitBreaker) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		_, err := b.Execute(func() (any, error) {
			return nil, invoker(ctx, method, req, reply, cc, opts...)
		})
		if err != nil && errors.Is(err, gobreaker.ErrOpenState) {
			return status.Error(codes.Unavailable, "upstream circuit open")
		}
		return err
	}
}
