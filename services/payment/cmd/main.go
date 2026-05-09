// Payment Service — main entrypoint.
//
// Bertanggung jawab atas:
//   - CreatePayment   → call Midtrans CoreAPI (QRIS), simpan record (gRPC)
//   - GetPayment      → query status by id (gRPC)
//   - Webhook handler → terima notifikasi Midtrans, verify signature, update state, publish event (HTTP raw — Midtrans push)
//
// Dual protocol rationale:
//   - gRPC: business APIs (CreatePayment, GetPayment) — dipanggil gateway via grpc-gateway dan client lain.
//   - HTTP: hanya untuk /healthz, /readyz, dan webhook (Midtrans signs raw body — paling
//     cocok di-handle di HTTP layer dengan akses raw bytes + headers).
//
// Mode operasional via env PAYMENT_MOCK_MODE:
//
//	true  → mock Midtrans (offline-friendly, demo cepat)
//	false → real call ke api.sandbox.midtrans.com (butuh MIDTRANS_SERVER_KEY)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

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
	"github.com/ajiperdana/parkir-pintar/pkg/logger"
	"github.com/ajiperdana/parkir-pintar/pkg/tracing"

	"github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/billingclient"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/grpcserver"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/midtrans"
	natsadapter "github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/nats"
	pgadapter "github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/postgres"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/usecase"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	env := getenv("APP_ENV", "dev")
	log, err := logger.New("payment", env, getenv("LOG_LEVEL", "info"))
	if err != nil {
		return err
	}
	defer log.Sync() //nolint:errcheck

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownTracing, _ := tracing.Init(rootCtx, tracing.Config{
		ServiceName: "payment", Env: env,
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})
	defer func() { _ = shutdownTracing(context.Background()) }()

	// ----- Postgres
	dsn := getenv("DB_URL", "postgres://parkir:parkir_dev_only@localhost:5432/parkirpintar?sslmode=disable&search_path=payment")
	pool, err := db.Open(rootCtx, dsn, 10, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	// ----- NATS
	pub, sub, err := eventbus.Connect(rootCtx, eventbus.NATSConfig{
		URL:    getenv("NATS_URL", "nats://localhost:4222"),
		Stream: getenv("NATS_STREAM", "PARKIRPINTAR"),
	})
	if err != nil {
		log.Warn("nats connect failed (continuing without events)", zap.Error(err))
	}
	defer func() {
		if pub != nil {
			_ = pub.Close()
		}
		if sub != nil {
			_ = sub.Close()
		}
	}()

	// ----- Adapters
	repo := pgadapter.New(pool)
	idemStore := idempotency.NewPostgresStore(pool)

	// Billing client — gRPC (default) atau stub. HTTP mode dihapus karena
	// service-to-service komunikasi sekarang full gRPC.
	var invoiceLookup usecase.InvoiceLookup
	billingGRPC := getenv("BILLING_GRPC_ADDR", "")
	if useStub, _ := strconv.ParseBool(getenv("BILLING_STUB", "false")); useStub || billingGRPC == "" {
		log.Warn("billing client in STUB mode — fixed amount 30000 IDR (set BILLING_GRPC_ADDR untuk live gRPC)")
		invoiceLookup = billingclient.NewStub("demo-driver", 30_000)
	} else {
		client, dialErr := billingclient.NewGRPC(billingGRPC)
		if dialErr != nil {
			return fmt.Errorf("billing grpc dial: %w", dialErr)
		}
		defer client.Close()
		invoiceLookup = client
		log.Info("billing client wired (gRPC)", zap.String("addr", billingGRPC))
	}

	// Midtrans client
	mockMode, _ := strconv.ParseBool(getenv("PAYMENT_MOCK_MODE", "true"))
	mtServerKey := getenv("MIDTRANS_SERVER_KEY", "")
	mtClient := &midtrans.Client{
		BaseURL:   getenv("MIDTRANS_BASE_URL", "https://api.sandbox.midtrans.com"),
		ServerKey: mtServerKey,
		Mock:      mockMode,
	}
	if mockMode {
		log.Info("midtrans MOCK mode — no real API call")
	} else {
		log.Info("midtrans LIVE sandbox", zap.String("base_url", mtClient.BaseURL))
		if mtServerKey == "" {
			log.Warn("MIDTRANS_SERVER_KEY empty — calls will fail; set in env or Secrets Manager")
		}
	}

	var publisher usecase.EventPublisher = natsadapter.Noop{}
	if pub != nil {
		publisher = natsadapter.NewPublisher(pub)
	}

	autoPay, _ := strconv.ParseBool(getenv("PAYMENT_AUTO_PAY", "false"))
	if autoPay {
		log.Info("auto-pay mode ENABLED — will charge mock on billing.invoice.issued events")
	}

	svc := &usecase.Service{
		Payments:    repo,
		Invoices:    invoiceLookup,
		Pub:         publisher,
		Gateway:     mtClient,
		Idempotency: idemStore,
		Clock:       clock.New(),
		ServerKey:   mtServerKey,
		IdemTTL:     24 * time.Hour,
		AutoPayMode: autoPay,
	}

	// ----- Subscribe NATS event "billing.invoice.issued.v1" untuk auto-pay.
	// Aktif baik di mode auto-pay maupun manual — handler skip kalau AutoPayMode=false.
	if sub != nil {
		const consumer = "payment-invoice-issued"
		log.Info("subscribing", zap.String("subject", eventbus.SubjBillingInvoiceIssued), zap.String("durable", consumer))
		if err := sub.Subscribe(rootCtx, eventbus.SubjBillingInvoiceIssued, consumer, svc.HandleInvoiceIssued); err != nil {
			return fmt.Errorf("subscribe %s: %w", eventbus.SubjBillingInvoiceIssued, err)
		}
	}

	// ----- gRPC server
	grpcAddr := getenv("PAYMENT_GRPC_ADDR", ":9093")
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

	gsrv := &grpcserver.Server{
		Service:     svc,
		Idempotency: idemStore,
		IdemTTL:     24 * time.Hour,
		Logger:      log,
	}
	gsrv.Register(grpcServer)

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

	// ----- HTTP server (webhook + health only)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := db.HealthCheck(rootCtx, pool); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db unavailable"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	// Readiness — DB + NATS + billing client (kalau wired).
	readyChecks := []apphealth.Check{
		apphealth.DBCheck("postgres", pool),
		apphealth.FuncCheck("nats", false, func(ctx context.Context) error {
			if pub == nil {
				return errors.New("publisher not initialized")
			}
			return nil
		}),
	}
	mux.Handle("/readyz", &apphealth.Handler{Timeout: 2 * time.Second, Checks: readyChecks})
	mux.HandleFunc("/v1/payments/midtrans/notification", makeNotificationHandler(svc, log))

	httpAddr := getenv("PAYMENT_HTTP_ADDR", ":9193")
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	go func() {
		log.Info("payment http listening", zap.String("addr", httpAddr))
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

// ----- HTTP webhook handler -----
//
// Midtrans push raw HTTP dengan signature verification. Kept di HTTP karena
// raw body + headers paling natural di HTTP. Internal call (test harness atau
// replay) bisa pakai gRPC HandleWebhook RPC instead.
func makeNotificationHandler(svc *usecase.Service, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, `{"error":"read"}`, http.StatusBadRequest)
			return
		}
		if err := svc.HandleMidtransNotification(r.Context(), body); err != nil {
			if errors.Is(err, domain.ErrInvalidSignature) {
				log.Warn("invalid webhook signature", zap.String("ip", r.RemoteAddr))
				http.Error(w, `{"error":"invalid_signature"}`, http.StatusUnauthorized)
				return
			}
			log.Warn("notification error", zap.Error(err))
			http.Error(w, `{"error":"webhook"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
