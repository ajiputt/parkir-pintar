// Gateway — public REST API. Translate REST → gRPC backend via grpc-gateway runtime.
//
// Routing semua endpoint business di-derive otomatis dari proto annotation
// `option (google.api.http) = { ... }`. Tidak ada hardcoded route mapping —
// definisi single source of truth ada di proto/{service}/v1/*.proto.
//
// Special routes (di luar grpc-gateway):
//   - /healthz, /readyz                        : probes
//   - /docs, /openapi.json                     : Swagger UI
//   - /v1/payments/midtrans/notification       : raw HTTP webhook → forward verbatim ke payment HTTP
//
// Middleware chain (outer → inner):
//
//	CORS → RequestID → Logging → RateLimit → Authn → mux
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/ajiperdana/parkir-pintar/pkg/auth"
	"github.com/ajiperdana/parkir-pintar/pkg/health"
	"github.com/ajiperdana/parkir-pintar/pkg/logger"
	"github.com/ajiperdana/parkir-pintar/pkg/metrics"
	"github.com/ajiperdana/parkir-pintar/pkg/ratelimit"
	"github.com/ajiperdana/parkir-pintar/pkg/tracing"

	billingv1 "github.com/ajiperdana/parkir-pintar/proto/gen/billing/v1"
	paymentv1 "github.com/ajiperdana/parkir-pintar/proto/gen/payment/v1"
	reservationv1 "github.com/ajiperdana/parkir-pintar/proto/gen/reservation/v1"

	"github.com/ajiperdana/parkir-pintar/services/gateway/internal/middleware"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

//nolint:gocyclo // bootstrap orchestrator; intentional sequential setup of subsystems
func run() error {
	env := getenv("APP_ENV", "dev")
	log, err := logger.New("gateway", env, getenv("LOG_LEVEL", "info"))
	if err != nil {
		return err
	}
	defer log.Sync() //nolint:errcheck

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownTracing, _ := tracing.Init(rootCtx, tracing.Config{
		ServiceName: "gateway", Env: env,
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})
	defer func() { _ = shutdownTracing(context.Background()) }()

	// ----- Backend addresses (gRPC dial targets) -----
	reservationAddr := getenv("RESERVATION_GRPC_ADDR_DIAL", "localhost:9091")
	billingAddr := getenv("BILLING_GRPC_ADDR_DIAL", "localhost:9092")
	paymentAddr := getenv("PAYMENT_GRPC_ADDR_DIAL", "localhost:9093")

	// ----- grpc-gateway runtime mux -----
	//
	// Custom IncomingHeaderMatcher: forward Idempotency-Key + X-Request-ID dari
	// HTTP request ke gRPC metadata (default grpc-gateway hanya forward grpcgateway- prefix).
	//
	// JSON marshaler: snake_case input + output, accept both forms saat unmarshal.
	//   - UseProtoNames=true       → output field "driver_id" (proto name) bukan "driverId" (camelCase default)
	//   - DiscardUnknown=true      → tolerate kelebihan field di JSON (forward compat)
	//   - EmitUnpopulated=false    → omit empty fields untuk payload lebih ringkas
	jsonMarshaler := &runtime.JSONPb{
		MarshalOptions: protojson.MarshalOptions{
			UseProtoNames:   true,
			EmitUnpopulated: false,
		},
		UnmarshalOptions: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
	}
	gwMux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(headerMatcher),
		runtime.WithErrorHandler(runtime.DefaultHTTPErrorHandler),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, jsonMarshaler),
	)

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	if err := reservationv1.RegisterReservationServiceHandlerFromEndpoint(rootCtx, gwMux, reservationAddr, dialOpts); err != nil {
		return fmt.Errorf("register reservation handler: %w", err)
	}
	if err := billingv1.RegisterBillingServiceHandlerFromEndpoint(rootCtx, gwMux, billingAddr, dialOpts); err != nil {
		return fmt.Errorf("register billing handler: %w", err)
	}
	if err := paymentv1.RegisterPaymentServiceHandlerFromEndpoint(rootCtx, gwMux, paymentAddr, dialOpts); err != nil {
		return fmt.Errorf("register payment handler: %w", err)
	}

	// ----- Top-level mux -----
	rootMux := http.NewServeMux()

	// Health
	rootMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	// Readiness — dial test ke setiap backend gRPC.
	rootMux.Handle("/readyz", &health.Handler{
		Timeout: 3 * time.Second,
		Checks: []health.Check{
			gatewayBackendCheck("reservation", reservationAddr),
			gatewayBackendCheck("billing", billingAddr),
			gatewayBackendCheck("payment", paymentAddr),
		},
	})

	// Swagger / OpenAPI
	rootMux.HandleFunc("/docs", swaggerUI)
	rootMux.HandleFunc("/openapi.json", openapiHandler)

	// Prometheus metrics — di-scrape oleh Prometheus server via ServiceMonitor.
	rootMux.Handle("/metrics", metrics.Handler())

	// Webhook — raw HTTP forward (grpc-gateway tidak cocok karena raw bytes + signature header)
	paymentHTTP := getenv("PAYMENT_HTTP_URL", "http://localhost:9193")
	rootMux.HandleFunc("/v1/payments/midtrans/notification", makeWebhookProxy(paymentHTTP, log))

	// Semua endpoint business di-route via grpc-gateway runtime.
	rootMux.Handle("/v1/", gwMux)

	// ----- Rate limiter — Redis-based distributed (ADR-0015).
	//
	// Multi-instance gateway safe via atomic Lua script di Redis. Per-endpoint
	// tier (heavy/medium/light/webhook). Override via env via numerical RPS
	// (lihat overrideConfigFromEnv).
	redisAddr := getenv("GATEWAY_REDIS_ADDR", "localhost:6379")
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()
	if _, err := rdb.Ping(rootCtx).Result(); err != nil {
		log.Warn("rate-limit Redis ping failed — fail-open mode", zap.Error(err))
	} else {
		log.Info("rate-limit Redis connected", zap.String("addr", redisAddr))
	}

	rlConfig := overrideConfigFromEnv(ratelimit.DefaultConfig())
	rateLimiter := ratelimit.New(rdb)

	// ----- JWT Auth (HS256). Secret dari env. Demo: passthrough kalau token kosong.
	jwtSecret := getenv("JWT_SECRET", "demo-secret-change-me-in-production")
	jwtIssuer := getenv("JWT_ISSUER", "parkirpintar")
	jwtTTL := durationenv("JWT_TTL", time.Hour)
	authPassthrough, _ := strconv.ParseBool(getenv("AUTH_PASSTHROUGH_NO_TOKEN", "true"))
	jwtVerifier := auth.NewVerifier(jwtSecret, jwtIssuer)
	jwtSigner := auth.NewSigner(jwtSecret, jwtIssuer, jwtTTL)
	authMiddleware := &auth.Middleware{
		Verifier: jwtVerifier,
		SkipPaths: []string{
			"/healthz", "/readyz", "/docs", "/openapi.json",
			"/v1/availability", "/v1/spots",
			"/v1/payments/midtrans/notification",
			"/v1/auth/dev-token", // self-service dev token
		},
		PassthroughIfNoToken: authPassthrough,
		Logger:               log,
	}
	if authPassthrough {
		log.Info("auth middleware: passthrough mode — token optional (demo)")
	} else {
		log.Info("auth middleware: enforce mode — Bearer token required")
	}

	// Dev-token endpoint (only saat APP_ENV != prod). Bypass auth via SkipPaths.
	if env != "prod" {
		rootMux.HandleFunc("/v1/auth/dev-token", makeDevTokenHandler(jwtSigner, log))
	}

	// ----- Middleware chain -----
	// Order: outer-most → inner-most.
	// Metrics → SecurityHeaders → CORS → RequestID → Logging → RateLimit → Auth → mux
	// Metrics wrap di paling luar supaya capture semua request termasuk yang
	// di-block oleh rate-limit / auth (status 429/401 tetap terhitung).
	handler := metrics.HTTPMiddleware(
		middleware.SecurityHeaders(
			middleware.CORS(
				middleware.RequestID(
					middleware.Logging(log)(
						ratelimit.Middleware(rateLimiter, rlConfig, log)(
							authMiddleware.Wrap(rootMux),
						),
					),
				),
			),
		),
	)

	addr := getenv("GATEWAY_HTTP_PORT", ":8080")
	if !strings.HasPrefix(addr, ":") {
		addr = ":" + addr
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go func() {
		log.Info("gateway listening",
			zap.String("addr", addr),
			zap.String("reservation", reservationAddr),
			zap.String("billing", billingAddr),
			zap.String("payment", paymentAddr),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", zap.Error(err))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Info("shutdown")
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	_ = srv.Shutdown(stopCtx)
	return nil
}

// headerMatcher — HTTP header → gRPC metadata. Forward Idempotency-Key,
// X-Request-ID, Authorization. Header lain di-skip kecuali grpcgateway- prefix
// (default behavior dari grpc-gateway).
func headerMatcher(key string) (string, bool) {
	switch strings.ToLower(key) {
	case "idempotency-key":
		return "x-idempotency-key", true
	case "x-request-id":
		return "x-request-id", true
	case "authorization":
		return "authorization", true
	default:
		return runtime.DefaultHeaderMatcher(key)
	}
}

// makeWebhookProxy — forward POST /v1/payments/midtrans/notification verbatim
// ke payment service HTTP. Midtrans verify signature pada raw body, jadi tidak
// boleh decode/re-encode JSON.
func makeWebhookProxy(targetBase string, log *zap.Logger) http.HandlerFunc {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	target := strings.TrimRight(targetBase, "/") + "/v1/payments/midtrans/notification"
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			http.Error(w, `{"error":"build request"}`, http.StatusInternalServerError)
			return
		}
		// Propagate semua header (Midtrans signature header dll).
		for k, vs := range r.Header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			log.Warn("webhook proxy", zap.Error(err))
			http.Error(w, `{"error":"upstream"}`, http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(resp.Body)
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// overrideConfigFromEnv — env override per-endpoint tier RPS.
//
// Supported envs:
//
//	GATEWAY_RATELIMIT_HEAVY_RPS, GATEWAY_RATELIMIT_HEAVY_BURST
//	GATEWAY_RATELIMIT_MEDIUM_RPS, GATEWAY_RATELIMIT_MEDIUM_BURST
//	GATEWAY_RATELIMIT_LIGHT_RPS, GATEWAY_RATELIMIT_LIGHT_BURST
//	GATEWAY_RATELIMIT_WEBHOOK_RPS, GATEWAY_RATELIMIT_WEBHOOK_BURST
//	GATEWAY_RATELIMIT_DEFAULT_RPS, GATEWAY_RATELIMIT_DEFAULT_BURST
//
//nolint:gocyclo // config mapping, intentionally explicit per-env-var
func overrideConfigFromEnv(c ratelimit.Config) ratelimit.Config {
	if v, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_DEFAULT_RPS", "")); v > 0 {
		c.Default.RPS = v
	}
	if v, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_DEFAULT_BURST", "")); v > 0 {
		c.Default.Burst = v
	}
	// Apply tier override based on heuristic Pattern matching.
	heavyRPS, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_HEAVY_RPS", ""))
	heavyBurst, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_HEAVY_BURST", ""))
	mediumRPS, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_MEDIUM_RPS", ""))
	mediumBurst, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_MEDIUM_BURST", ""))
	lightRPS, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_LIGHT_RPS", ""))
	lightBurst, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_LIGHT_BURST", ""))
	webhookRPS, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_WEBHOOK_RPS", ""))
	webhookBurst, _ := strconv.Atoi(getenv("GATEWAY_RATELIMIT_WEBHOOK_BURST", ""))

	for i := range c.Endpoints {
		ep := &c.Endpoints[i]
		switch {
		case ep.Pattern == "/v1/reservations" && ep.Method == "POST",
			ep.Pattern == "/v1/payments" && ep.Method == "POST":
			if heavyRPS > 0 {
				ep.RPS = heavyRPS
			}
			if heavyBurst > 0 {
				ep.Burst = heavyBurst
			}
		case ep.Pattern == "/v1/reservations/*" && ep.Method == "POST":
			if mediumRPS > 0 {
				ep.RPS = mediumRPS
			}
			if mediumBurst > 0 {
				ep.Burst = mediumBurst
			}
		case ep.Method == "GET":
			if lightRPS > 0 {
				ep.RPS = lightRPS
			}
			if lightBurst > 0 {
				ep.Burst = lightBurst
			}
		case ep.Pattern == "/v1/payments/midtrans/notification":
			if webhookRPS > 0 {
				ep.RPS = webhookRPS
			}
			if webhookBurst > 0 {
				ep.Burst = webhookBurst
			}
		}
	}
	return c
}

// makeDevTokenHandler — POST /v1/auth/dev-token { driver_id, ttl_sec? }
// Issue HS256 token untuk testing di Postman. ONLY enabled saat APP_ENV != prod.
func makeDevTokenHandler(signer *auth.Signer, log *zap.Logger) http.HandlerFunc {
	type req struct {
		DriverID string `json:"driver_id"`
		TTLSec   int64  `json:"ttl_sec,omitempty"`
	}
	type resp struct {
		Token     string `json:"token"`
		ExpiresIn int64  `json:"expires_in"`
		Sub       string `json:"sub"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, `{"error":"invalid_json"}`, http.StatusBadRequest)
			return
		}
		if in.DriverID == "" {
			http.Error(w, `{"error":"driver_id required"}`, http.StatusBadRequest)
			return
		}
		c := auth.Claims{Sub: in.DriverID}
		if in.TTLSec > 0 {
			c.Exp = time.Now().Unix() + in.TTLSec
		}
		token, err := signer.Sign(c)
		if err != nil {
			log.Error("sign token", zap.Error(err))
			http.Error(w, `{"error":"sign_failed"}`, http.StatusInternalServerError)
			return
		}
		ttl := in.TTLSec
		if ttl == 0 {
			ttl = 3600 // 1h default
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp{
			Token:     token,
			ExpiresIn: ttl,
			Sub:       in.DriverID,
		})
		log.Info("dev token issued", zap.String("sub", in.DriverID))
	}
}

// durationenv — duration getenv helper.
func durationenv(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// gatewayBackendCheck — TCP dial test ke backend gRPC. Critical karena gateway
// jadi entry point — kalau backend unreachable, traffic akan fail anyway.
func gatewayBackendCheck(name, addr string) health.Check {
	return health.FuncCheck(name, true, func(ctx context.Context) error {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	})
}

// --- Swagger UI: minimal embed page yang load /openapi.json ---
const swaggerHTML = `<!DOCTYPE html><html><head>
<title>ParkirPintar API</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css"></head>
<body><div id="ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({url:'/openapi.json',dom_id:'#ui'});</script></body></html>`

func swaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerHTML))
}

// openapiHandler — serve generated OpenAPI spec. `make proto` menulis ke
// docs/api/openapi.json. Kalau file belum ada, return minimal placeholder.
func openapiHandler(w http.ResponseWriter, _ *http.Request) {
	specPath := getenv("OPENAPI_SPEC_PATH", "docs/api/openapi.json")
	if data, err := os.ReadFile(specPath); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"openapi": "3.0.0",
		"info": map[string]any{
			"title":       "ParkirPintar API",
			"version":     "1.0.0",
			"description": "Smart Parking Marketplace — generated dari .proto setelah `make proto`",
		},
		"paths": map[string]any{
			"/v1/availability":      map[string]any{"get": map[string]any{"summary": "Get availability"}},
			"/v1/reservations":      map[string]any{"post": map[string]any{"summary": "Create reservation"}},
			"/v1/reservations/{id}": map[string]any{"get": map[string]any{"summary": "Get reservation"}},
			"/v1/payments":          map[string]any{"post": map[string]any{"summary": "Create payment"}},
			"/v1/payments/{id}":     map[string]any{"get": map[string]any{"summary": "Get payment"}},
			"/v1/invoices/{id}":     map[string]any{"get": map[string]any{"summary": "Get invoice"}},
		},
	})
}
