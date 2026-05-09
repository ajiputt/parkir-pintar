// Notification service — subscribe NATS events, dispatch email via SES.
//
// Lihat docs/architecture/adr/0013-user-data-ownership.md untuk arsitektur
// detail (single-channel email, no retry → DLQ, embedded user data).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/ajiperdana/parkir-pintar/pkg/db"
	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/pkg/health"
	"github.com/ajiperdana/parkir-pintar/pkg/logger"

	natsadapter "github.com/ajiperdana/parkir-pintar/services/notification/internal/adapter/nats"
	pgadapter "github.com/ajiperdana/parkir-pintar/services/notification/internal/adapter/postgres"
	"github.com/ajiperdana/parkir-pintar/services/notification/internal/adapter/ses"
	tplloader "github.com/ajiperdana/parkir-pintar/services/notification/internal/adapter/template"
	"github.com/ajiperdana/parkir-pintar/services/notification/internal/usecase"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	env := getenv("APP_ENV", "dev")
	log, err := logger.New("notification", env, getenv("LOG_LEVEL", "info"))
	if err != nil {
		return err
	}
	defer log.Sync() //nolint:errcheck

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ----- Postgres
	dsn := getenv("DB_URL", "postgres://gopark:gopark@localhost:5432/gopark?sslmode=disable&search_path=notification")
	pool, err := db.Open(rootCtx, dsn, 10, 2)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	// ----- NATS (pub for DLQ + sub for events)
	pub, sub, err := eventbus.Connect(rootCtx, eventbus.NATSConfig{
		URL:    getenv("NATS_URL", "nats://localhost:4222"),
		Stream: getenv("NATS_STREAM", "PARKIRPINTAR"),
	})
	if err != nil {
		log.Warn("nats connect failed (continuing without subscriptions)", zap.Error(err))
	}
	defer func() {
		if pub != nil {
			_ = pub.Close()
		}
		if sub != nil {
			_ = sub.Close()
		}
	}()

	// ----- Template loader
	tplDir := getenv("NOTIFICATION_TEMPLATE_DIR", "services/notification/templates")
	if !filepath.IsAbs(tplDir) {
		// Relative to repo root — kalau service dijalankan dari services/notification, naik 2 level.
		// Try services/notification/templates first, then templates/.
		if _, err := os.Stat(tplDir); err != nil {
			alt := "templates"
			if _, err := os.Stat(alt); err == nil {
				tplDir = alt
			}
		}
	}
	renderer, err := tplloader.NewLoader(tplDir)
	if err != nil {
		return fmt.Errorf("template loader: %w", err)
	}
	log.Info("templates loaded", zap.String("dir", tplDir))

	// ----- SES sender
	mockSES, _ := strconv.ParseBool(getenv("NOTIFICATION_MOCK_SES", "true"))
	sender, err := ses.New(rootCtx,
		getenv("AWS_REGION", "ap-southeast-1"),
		getenv("SES_FROM", "noreply@parkirpintar.id"),
		getenv("SES_REPLY_TO", ""),
		mockSES,
		log,
	)
	if err != nil {
		return fmt.Errorf("ses init: %w", err)
	}
	if mockSES {
		log.Info("SES MOCK mode — emails di-log only, tidak benar-benar terkirim")
	} else {
		log.Info("SES LIVE mode",
			zap.String("region", getenv("AWS_REGION", "ap-southeast-1")),
			zap.String("from", getenv("SES_FROM", "noreply@parkirpintar.id")))
	}

	// ----- Repos
	contactRepo := pgadapter.NewContactRepo(pool)
	notifRepo := pgadapter.NewNotificationRepo(pool)

	// ----- DLQ publisher
	var dlq usecase.DLQPublisher = natsadapter.NoopDLQ{}
	if pub != nil {
		dlq = natsadapter.NewDLQPublisher(pub)
	}

	// ----- Dispatcher
	disp := &usecase.Dispatcher{
		Contacts:      contactRepo,
		Notifications: notifRepo,
		Sender:        sender,
		Renderer:      renderer,
		DLQ:           dlq,
		Logger:        log,
	}

	// ----- Subscribe NATS
	if sub != nil {
		subscriptions := []struct {
			subject string
			durable string
			handler eventbus.Handler
		}{
			{eventbus.SubjReservationConfirmed, "notif-res-confirmed", disp.HandleReservationConfirmed},
			{eventbus.SubjReservationExpired, "notif-res-expired", disp.HandleReservationExpired},
			{eventbus.SubjBillingInvoiceIssued, "notif-invoice-issued", disp.HandleInvoiceIssued},
			{eventbus.SubjBillingInvoiceOverdue, "notif-invoice-overdue", disp.HandleInvoiceOverdue},
			{eventbus.SubjPaymentSucceeded, "notif-payment-success", disp.HandlePaymentSucceeded},
			{eventbus.SubjPaymentFailed, "notif-payment-failed", disp.HandlePaymentFailed},
		}
		for _, s := range subscriptions {
			s := s
			log.Info("subscribing", zap.String("subject", s.subject), zap.String("durable", s.durable))
			if err := sub.Subscribe(rootCtx, s.subject, s.durable, s.handler); err != nil {
				log.Error("subscribe", zap.String("subject", s.subject), zap.Error(err))
			}
		}
	}

	// ----- HTTP probes
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := db.HealthCheck(rootCtx, pool); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db unavailable"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/readyz", &health.Handler{
		Timeout: 2 * time.Second,
		Checks: []health.Check{
			health.DBCheck("postgres", pool),
			health.FuncCheck("nats", false, func(ctx context.Context) error {
				if pub == nil {
					return errors.New("publisher not initialized")
				}
				return nil
			}),
			health.FuncCheck("templates", true, func(ctx context.Context) error {
				if renderer == nil {
					return errors.New("template loader not initialized")
				}
				return nil
			}),
		},
	})

	addr := getenv("NOTIFICATION_HTTP_ADDR", ":9196")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("notification listening", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", zap.Error(err))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Info("shutdown")
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	_ = srv.Shutdown(stopCtx)
	return nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
