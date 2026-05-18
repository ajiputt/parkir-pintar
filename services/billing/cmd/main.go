// Billing Service — main entrypoint.
//
// Subscribe NATS events dari reservation+payment, project ke invoice & invoice_item.
// Expose gRPC API (BillingService.GetInvoice, GetInvoiceByReservation, ...) untuk
// dipanggil payment service melalui billingclient gRPC. HTTP server hanya untuk
// /healthz dan /readyz (probes K8s/ECS).
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ajiperdana/parkir-pintar/pkg/closeutil"
	"github.com/ajiperdana/parkir-pintar/pkg/db"
	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/pkg/grpcutil"
	apphealth "github.com/ajiperdana/parkir-pintar/pkg/health"
	"github.com/ajiperdana/parkir-pintar/pkg/logger"
	"github.com/ajiperdana/parkir-pintar/pkg/metrics"
	"github.com/ajiperdana/parkir-pintar/pkg/pricing"
	"github.com/ajiperdana/parkir-pintar/pkg/tracing"

	"github.com/ajiperdana/parkir-pintar/services/billing/internal/adapter/grpcserver"
	natsadapter "github.com/ajiperdana/parkir-pintar/services/billing/internal/adapter/nats"
	pgadapter "github.com/ajiperdana/parkir-pintar/services/billing/internal/adapter/postgres"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/usecase"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	env := getenv("APP_ENV", "dev")
	log, err := logger.New("billing", env, getenv("LOG_LEVEL", "info"))
	if err != nil {
		return err
	}
	defer log.Sync() //nolint:errcheck

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownTracing, err := tracing.Init(rootCtx, tracing.Config{
		ServiceName:  "billing",
		Env:          env,
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})
	if err != nil {
		log.Warn("tracing init", zap.Error(err))
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	// Fail-fast: DB_URL wajib di-set. Hardcoded fallback dengan password di-hapus
	// per Sonar security finding (CWE: hardcoded credentials).
	dsn := os.Getenv("DB_URL")
	if dsn == "" {
		return fmt.Errorf("DB_URL env var required (no fallback for security)")
	}
	pool, err := db.Open(rootCtx, dsn, 10, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	natsURL := getenv("NATS_URL", "nats://localhost:4222")
	stream := getenv("NATS_STREAM", "PARKIRPINTAR")
	pub, sub, err := eventbus.Connect(rootCtx, eventbus.NATSConfig{URL: natsURL, Stream: stream})
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer closeutil.Quiet(pub)
	defer closeutil.Quiet(sub)

	// ----- Wire
	repo := pgadapter.NewInvoiceRepo(pool)
	eventsLog := pgadapter.NewEventLog(pool)
	publisher := natsadapter.NewPublisher(pub)
	engine := pricing.Default()

	svc := &usecase.Service{
		Invoices: repo,
		Events:   eventsLog,
		Engine:   engine,
		Pub:      publisher,
	}

	// ----- Subscribe NATS subjects
	subs := []struct {
		subject  string
		consumer string
		handler  eventbus.Handler
	}{
		{eventbus.SubjReservationConfirmed, "billing-res-confirmed", svc.HandleReservationConfirmed},
		{eventbus.SubjReservationCheckedOut, "billing-res-checkout", svc.HandleReservationCheckedOut},
		{eventbus.SubjReservationExpired, "billing-res-expired", svc.HandleReservationExpired},
		{eventbus.SubjReservationCancelled, "billing-res-cancelled", svc.HandleReservationCancelled},
		{eventbus.SubjPaymentSucceeded, "billing-payment-success", svc.HandlePaymentSucceeded},
	}
	for _, s := range subs {
		log.Info("subscribing", zap.String("subject", s.subject), zap.String("durable", s.consumer))
		if err := sub.Subscribe(rootCtx, s.subject, s.consumer, s.handler); err != nil {
			return fmt.Errorf("subscribe %s: %w", s.subject, err)
		}
	}

	// ----- gRPC server
	grpcAddr := getenv("BILLING_GRPC_ADDR", ":9092")
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			grpcutil.RecoveryUnary(log),
			grpcutil.RequestIDUnary(),
			grpcutil.MetricsUnary(),
			grpcutil.LoggingUnary(log),
		),
	)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	srv := &grpcserver.Server{Service: svc, Logger: log}
	srv.Register(grpcServer)

	listener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	go func() {
		log.Info("grpc listening", zap.String("addr", grpcAddr))
		if err := grpcServer.Serve(listener); err != nil {
			log.Error("grpc", zap.Error(err))
		}
	}()

	// ----- Overdue worker (Tier 2 ADR-0014)
	overdueWorker := &usecase.OverdueWorker{
		Invoices:    repo,
		Pub:         publisher,
		Interval:    durationenv("BILLING_OVERDUE_INTERVAL", 5*time.Minute),
		GracePeriod: durationenv("BILLING_OVERDUE_GRACE", 15*time.Minute),
		BatchSize:   100,
		Logger:      log,
	}
	go overdueWorker.Run(rootCtx)

	// ----- HTTP probes
	httpAddr := getenv("BILLING_HTTP_ADDR", ":9192")
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := db.HealthCheck(rootCtx, pool); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	// Readiness — check DB. NATS optional (kalau down, service tetap kerja
	// kecuali untuk subscriber/publisher; mark non-critical untuk tolerance).
	readyHandler := &apphealth.Handler{
		Timeout: 2 * time.Second,
		Checks: []apphealth.Check{
			apphealth.DBCheck("postgres", pool),
			apphealth.FuncCheck("nats", false, func(ctx context.Context) error {
				if pub == nil {
					return errors.New("publisher not initialized")
				}
				return nil // best-effort: NATS publisher handle reconnect internally
			}),
		},
	}
	mux.Handle("/readyz", readyHandler)
	mux.Handle("/metrics", metrics.Handler())

	httpSrv := &http.Server{Addr: httpAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("http listening", zap.String("addr", httpAddr))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", zap.Error(err))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Info("shutdown")
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	_ = httpSrv.Shutdown(stopCtx)
	grpcServer.GracefulStop()
	return nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func durationenv(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
