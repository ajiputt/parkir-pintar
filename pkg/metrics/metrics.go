// Package metrics — Prometheus instrumentation untuk ParkirPintar.
//
// Provides:
//   - Default registry dengan Go runtime + process metrics
//   - HTTP middleware: request counter + latency histogram
//   - gRPC interceptors: server-side metrics
//   - Helper untuk register business-level metrics (counter, gauge)
//
// Convention metric names (Prometheus best practice):
//   - <namespace>_<subsystem>_<unit>_<suffix>
//   - http_requests_total (counter)
//   - http_request_duration_seconds (histogram)
//   - reservations_created_total (business counter)
//   - active_reservations (gauge)
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the default registry untuk semua metrics service.
// Pre-populated dengan Go runtime + process collectors.
var Registry = func() *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector())
	r.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return r
}()

// HTTP metrics.
var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests received, grouped by method, path, status.",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency distribution.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path", "status"},
	)

	httpRequestsInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Current number of in-flight HTTP requests.",
		},
	)

	// gRPC metrics — di-emit oleh grpcutil.MetricsUnary interceptor di backend
	// services (reservation, billing, payment, notification). Mirror HTTP metrics
	// structure supaya Grafana dashboard bisa share label semantics.
	GRPCRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_requests_total",
			Help: "Total gRPC requests received, grouped by service, method, code.",
		},
		[]string{"grpc_service", "grpc_method", "grpc_code"},
	)

	GRPCRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_request_duration_seconds",
			Help:    "gRPC unary handler latency distribution.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"grpc_service", "grpc_method", "grpc_code"},
	)
)

func init() {
	Registry.MustRegister(
		httpRequestsTotal, httpRequestDuration, httpRequestsInFlight,
		GRPCRequestsTotal, GRPCRequestDuration,
	)
}

// Handler returns http.Handler untuk endpoint /metrics.
// Pakai di service main.go:
//
//	mux.Handle("/metrics", metrics.Handler())
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// HTTPMiddleware wraps http.Handler dengan request count + latency tracking.
// Path normalization: collapse UUID/numeric IDs jadi "{id}" supaya cardinality
// gak meledak (kalau path raw, tiap reservation ID jadi label sendiri).
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		httpRequestsInFlight.Inc()
		defer httpRequestsInFlight.Dec()

		// Wrap ResponseWriter capture status code
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		path := normalizePath(r.URL.Path)
		status := strconv.Itoa(rw.status)
		duration := time.Since(start).Seconds()

		httpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path, status).Observe(duration)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// normalizePath collapse UUID + numeric segments jadi "{id}" untuk reduce cardinality.
// Examples:
//
//	/v1/reservations/abc-123-uuid          -> /v1/reservations/{id}
//	/v1/reservations/abc-123-uuid:checkin  -> /v1/reservations/{id}:checkin
//	/v1/invoices/inv-xxx                   -> /v1/invoices/{id}
func normalizePath(path string) string {
	// Simple heuristic: replace segments yang panjang > 8 char dan mengandung "-" / digit
	// Untuk demo cukup, production bisa pakai regex lebih ketat
	const maxIDLen = 8

	out := []byte{}
	segStart := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' || path[i] == ':' {
			seg := path[segStart:i]
			if isLikelyID(seg, maxIDLen) {
				out = append(out, "{id}"...)
			} else {
				out = append(out, seg...)
			}
			if i < len(path) {
				out = append(out, path[i])
			}
			segStart = i + 1
		}
	}
	return string(out)
}

func isLikelyID(s string, minLen int) bool {
	if len(s) < minLen {
		return false
	}
	hasDigit := false
	hasDash := false
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case c == '-':
			hasDash = true
		}
	}
	return hasDigit && (hasDash || len(s) > 16)
}

// ----- Business Metrics Helpers -----

// NewCounter creates + registers counter dengan default registry.
// Pakai untuk business events: reservations_created_total, payments_processed_total, dst.
func NewCounter(name, help string, labels []string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: name, Help: help},
		labels,
	)
	Registry.MustRegister(c)
	return c
}

// NewGauge creates + registers gauge.
// Pakai untuk current state: active_reservations, occupied_spots, dst.
func NewGauge(name, help string, labels []string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: name, Help: help},
		labels,
	)
	Registry.MustRegister(g)
	return g
}

// NewHistogram creates + registers histogram.
// Pakai untuk distribution: payment_amount_idr, reservation_duration_minutes.
func NewHistogram(name, help string, buckets []float64, labels []string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets},
		labels,
	)
	Registry.MustRegister(h)
	return h
}
