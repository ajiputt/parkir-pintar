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
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ajiperdana/parkir-pintar/pkg/logger"
	"github.com/ajiperdana/parkir-pintar/pkg/metrics"
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

// MetricsUnary — emit Prometheus counter + histogram per RPC.
// Labels: grpc_service, grpc_method, grpc_code (OK / NotFound / etc).
//
// info.FullMethod format: "/<package>.<Service>/<Method>" — kita split jadi
// 2 label terpisah supaya Grafana bisa group dengan benar.
//
// Pakai bareng RecoveryUnary supaya panic ke-convert dulu jadi Internal code,
// bukan crash sebelum metric ke-emit.
func MetricsUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start).Seconds()

		service, method := splitFullMethod(info.FullMethod)
		code := status.Code(err).String() // "OK" untuk nil error, "NotFound" / etc selainnya

		metrics.GRPCRequestsTotal.WithLabelValues(service, method, code).Inc()
		metrics.GRPCRequestDuration.WithLabelValues(service, method, code).Observe(duration)

		return resp, err
	}
}

// splitFullMethod — "/parkirpintar.reservation.v1.ReservationService/CreateReservation"
// → ("parkirpintar.reservation.v1.ReservationService", "CreateReservation").
// Fallback: kalau format aneh, return ("unknown", fullMethod).
func splitFullMethod(fullMethod string) (service, method string) {
	if !strings.HasPrefix(fullMethod, "/") {
		return "unknown", fullMethod
	}
	parts := strings.SplitN(fullMethod[1:], "/", 2)
	if len(parts) != 2 {
		return "unknown", fullMethod
	}
	return parts[0], parts[1]
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
	default:
		return false
	}
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
