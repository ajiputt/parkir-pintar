// Package logger — structured logger berbasis zap.
// Setiap log berisi service, env, trace_id (jika ada di context), dan request_id.
package logger

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyTraceID
	ctxKeyDriverID
)

// New membangun *zap.Logger sesuai env (dev=console, prod=json).
func New(service, env, level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	if env == "dev" {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}
	if lvl, err := zapcore.ParseLevel(level); err == nil {
		cfg.Level = zap.NewAtomicLevelAt(lvl)
	}
	cfg.InitialFields = map[string]any{
		"service": service,
		"env":     env,
	}
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.MessageKey = "msg"

	return cfg.Build(zap.AddCallerSkip(0))
}

// WithRequestID inject request id ke context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestID ambil request id dari context.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// WithTraceID inject trace id ke context (otel sudah handle ini sebenarnya, ini fallback).
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyTraceID, id)
}

// TraceID ambil trace id dari context.
func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTraceID).(string)
	return v
}

// FromContext — return logger dengan field request_id & trace_id auto-injected.
// Wajib ada base logger yang sudah diset via WithLogger.
func FromContext(ctx context.Context, base *zap.Logger) *zap.Logger {
	l := base
	if rid := RequestID(ctx); rid != "" {
		l = l.With(zap.String("request_id", rid))
	}
	if tid := TraceID(ctx); tid != "" {
		l = l.With(zap.String("trace_id", tid))
	}
	if did, _ := ctx.Value(ctxKeyDriverID).(string); did != "" {
		l = l.With(zap.String("driver_id", did))
	}
	return l
}

// WithDriverID — set driver id ke context (untuk audit trail).
func WithDriverID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyDriverID, id)
}
