// Reservation Service — main entrypoint.
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

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ajiperdana/parkir-pintar/pkg/clock"
	"github.com/ajiperdana/parkir-pintar/pkg/db"
	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/pkg/grpcutil"
	apphealth "github.com/ajiperdana/parkir-pintar/pkg/health"
	"github.com/ajiperdana/parkir-pintar/pkg/idempotency"
	"github.com/ajiperdana/parkir-pintar/pkg/lock"
	"github.com/ajiperdana/parkir-pintar/pkg/logger"
	"github.com/ajiperdana/parkir-pintar/pkg/tracing"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/adapter/billingclient"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/adapter/grpcserver"
	natsadapter "github.com/ajiperdana/parkir-pintar/services/reservation/internal/adapter/nats"
	pgadapter "github.com/ajiperdana/parkir-pintar/services/reservation/internal/adapter/postgres"
	redisadapter "github.com/ajiperdana/parkir-pintar/services/reservation/internal/adapter/redis"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	env := getenv("APP_ENV", "dev")
	log, err := logger.New("reservation", env, getenv("LOG_LEVEL", "info"))
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	defer log.Sync() //nolint:errcheck

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ----- Tracing
	shutdownTracing, err := tracing.Init(rootCtx, tracing.Config{
		ServiceName:  "reservation",
		Env:          env,
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})
	if err != nil {
		log.Warn("tracing init failed (continuing)", zap.Error(err))
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	// ----- Postgres
	dsn := os.Getenv("DB_URL")
	if dsn == "" {
		dsn = "postgres://parkir:parkir_dev_only@localhost:5432/parkirpintar?sslmode=disable&search_path=reservation"
	}
	pool, err := db.Open(rootCtx, dsn, 20, 4)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	// ----- Redis
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()
	var locker lock.Locker = lock.NewRedisLocker(rdb)
	if _, err := rdb.Ping(rootCtx).Result(); err != nil {
		log.Warn("redis ping failed — falling back to memory locker", zap.Error(err))
		locker = lock.NewMemoryLocker()
	}

	// ----- NATS
	natsURL := getenv("NATS_URL", "nats://localhost:4222")
	stream := getenv("NATS_STREAM", "PARKIRPINTAR")
	pub, _, err := eventbus.Connect(rootCtx, eventbus.NATSConfig{URL: natsURL, Stream: stream})
	if err != nil {
		log.Warn("nats connect failed (continuing without events)", zap.Error(err))
	}
	defer func() {
		if pub != nil {
			_ = pub.Close()
		}
	}()

	// ----- Adapters
	resRepo := pgadapter.NewReservationRepo(pool)
	spotRepo := pgadapter.NewSpotRepo(pool)
	idemStore := idempotency.NewPostgresStore(pool)

	var publisher usecase.EventPublisher
	if pub != nil {
		publisher = natsadapter.NewPublisher(pub)
	} else {
		publisher = natsadapter.NoopPublisher{}
	}

	// ----- Use cases
	holdDur := durationenv("RESERVATION_HOLD_DURATION", time.Hour)
	lockTTL := durationenv("SPOT_LOCK_TTL", 10*time.Second)
	clk := clock.New()

	// Billing client untuk overdue pre-check (ADR-0014). Kalau env kosong atau
	// dial gagal, pakai NoopChecker (graceful degradation: tidak block driver).
	var overdueChecker usecase.OverdueChecker = billingclient.NoopChecker{}
	if addr := getenv("BILLING_GRPC_ADDR_DIAL", ""); addr != "" {
		bc, dialErr := billingclient.New(addr)
		if dialErr != nil {
			log.Warn("billing client dial failed (continuing with no overdue check)", zap.Error(dialErr))
		} else {
			defer bc.Close()
			overdueChecker = bc
			log.Info("overdue checker wired (billing gRPC)", zap.String("addr", addr))
		}
	}

	createUC := &usecase.CreateReservation{
		Reservations: resRepo, Spots: spotRepo,
		Locker: redisadapter.NewLocker(locker),
		Events: publisher, Clock: clk,
		HoldDuration: holdDur, SpotLockTTL: lockTTL,
		OverdueChecker: overdueChecker,
	}
	checkInUC := &usecase.CheckIn{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk}
	checkOutUC := &usecase.CheckOut{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk}
	cancelUC := &usecase.Cancel{Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk}
	availUC := &usecase.GetAvailability{Spots: spotRepo}

	// ----- Background expiry worker
	worker := &usecase.ExpiryWorker{
		Reservations: resRepo, Spots: spotRepo, Events: publisher, Clock: clk,
		Interval:  durationenv("RESERVATION_EXPIRY_SCAN_INTERVAL", 30*time.Second),
		BatchSize: 100, Logger: log,
	}
	go worker.Run(rootCtx)

	// ----- gRPC server
	grpcAddr := getenv("RESERVATION_GRPC_ADDR", ":9091")
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			grpcutil.RecoveryUnary(log),
			grpcutil.RequestIDUnary(),
			grpcutil.LoggingUnary(log),
		),
	)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	srv := &grpcserver.Server{
		CreateRes:      createUC,
		CheckInUC:      checkInUC,
		CheckOutUC:     checkOutUC,
		CancelUC:       cancelUC,
		AvailabilityUC: availUC,
		Reservations:   resRepo,
		Spots:          spotRepo,
		Idempotency:    idemStore,
		IdemTTL:        24 * time.Hour,
		Logger:         log,
	}
	srv.Register(grpcServer)

	listener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// ----- HTTP for /healthz, /metrics
	httpAddr := getenv("RESERVATION_HTTP_ADDR", ":9191")
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := db.HealthCheck(rootCtx, pool); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db unavailable"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	// Readiness — full dependency check.
	readyHandler := &apphealth.Handler{
		Timeout: 2 * time.Second,
		Checks: []apphealth.Check{
			apphealth.DBCheck("postgres", pool),
			apphealth.FuncCheck("redis", true, func(ctx context.Context) error {
				return rdb.Ping(ctx).Err()
			}),
			apphealth.FuncCheck("nats", false, func(ctx context.Context) error {
				if pub == nil {
					return errors.New("publisher not initialized")
				}
				return nil
			}),
		},
	}
	mux.Handle("/readyz", readyHandler)
	httpSrv := &http.Server{Addr: httpAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		log.Info("http listening", zap.String("addr", httpAddr))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http error", zap.Error(err))
		}
	}()

	go func() {
		log.Info("grpc listening", zap.String("addr", grpcAddr))
		if err := grpcServer.Serve(listener); err != nil {
			log.Error("grpc error", zap.Error(err))
		}
	}()

	// ----- Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Info("shutdown signal received")

	cancel() // stop worker
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
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

// (NoopPublisher disediakan oleh internal/adapter/nats — graceful degradation
// jika NATS unreachable: reservation tetap bisa dibuat, billing nanti rebuild
// dari outbox/replay sesuai ADR-0006.)
