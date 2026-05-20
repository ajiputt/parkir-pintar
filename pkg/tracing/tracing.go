// Package tracing — inisialisasi OpenTelemetry tracer dengan OTLP gRPC exporter.
// Service tinggal panggil tracing.Init di main, defer Shutdown.
package tracing

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config — knobs untuk init.
type Config struct {
	ServiceName  string
	Env          string
	OTLPEndpoint string  // mis. "otel-collector:4317"
	SamplerRatio float64 // 0..1; 1.0 = trace semuanya
}

// Init memasang global tracer provider + propagator.
// Return shutdown fn yang harus dipanggil saat graceful shutdown.
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if cfg.OTLPEndpoint == "" {
		// noop tracer — OK untuk env tanpa observability stack.
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: init exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			attribute.String("env", cfg.Env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: init resource: %w", err)
	}

	ratio := cfg.SamplerRatio
	if ratio <= 0 {
		ratio = 0.1 // default 10% di prod
	}
	if cfg.Env == "dev" {
		ratio = 1.0
	}
	// Env var override — supaya bisa boost sampling rate tanpa rebuild.
	// e.g., OTEL_TRACES_SAMPLER_RATIO=1.0 → trace semuanya (untuk demo).
	if v := os.Getenv("OTEL_TRACES_SAMPLER_RATIO"); v != "" {
		if r, err := strconv.ParseFloat(v, 64); err == nil && r >= 0 && r <= 1 {
			ratio = r
		}
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}
