package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATSPublisher — JetStream publisher.
type NATSPublisher struct {
	nc  *nats.Conn
	js  jetstream.JetStream
	str string
}

// NATSSubscriber — JetStream consumer.
type NATSSubscriber struct {
	nc  *nats.Conn
	js  jetstream.JetStream
	str string
}

// NATSConfig — knobs.
type NATSConfig struct {
	URL    string
	Stream string // mis. "PARKIRPINTAR"
}

// Connect — sambung ke NATS, ensure stream, return Publisher & Subscriber.
func Connect(ctx context.Context, cfg NATSConfig) (*NATSPublisher, *NATSSubscriber, error) {
	nc, err := nats.Connect(cfg.URL,
		nats.Name("parkir-pintar"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("eventbus.nats: connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("eventbus.nats: jetstream: %w", err)
	}

	// Ensure stream exists. Idempotent.
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      cfg.Stream,
		Subjects:  []string{cfg.Stream + ".>"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		Storage:   jetstream.FileStorage,
		Replicas:  1, // demo; production: 3
	})
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("eventbus.nats: ensure stream: %w", err)
	}

	return &NATSPublisher{nc: nc, js: js, str: cfg.Stream},
		&NATSSubscriber{nc: nc, js: js, str: cfg.Stream},
		nil
}

func (p *NATSPublisher) Publish(ctx context.Context, subject string, env Envelope) error {
	if subject == "" {
		return ErrEmptySubject
	}
	full := p.str + "." + subject
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = p.js.Publish(ctx, full, body,
		jetstream.WithMsgID(env.ID), // dedup di JetStream level
	)
	return err
}

func (p *NATSPublisher) Close() error {
	p.nc.Close()
	return nil
}

func (s *NATSSubscriber) Subscribe(ctx context.Context, subject, durableName string, handler Handler) error {
	full := s.str + "." + subject
	cons, err := s.js.CreateOrUpdateConsumer(ctx, s.str, jetstream.ConsumerConfig{
		Durable:       durableName,
		FilterSubject: full,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxAckPending: 256,
		MaxDeliver:    5,
	})
	if err != nil {
		return fmt.Errorf("eventbus.nats: consumer: %w", err)
	}

	_, err = cons.Consume(func(msg jetstream.Msg) {
		var env Envelope
		if err := json.Unmarshal(msg.Data(), &env); err != nil {
			_ = msg.Term() // bad payload, jangan redeliver
			return
		}
		if err := handler(ctx, env); err != nil {
			_ = msg.NakWithDelay(2 * time.Second)
			return
		}
		_ = msg.Ack()
	})
	return err
}

func (s *NATSSubscriber) Close() error {
	s.nc.Close()
	return nil
}

// Subjects — konstanta untuk konsistensi naming.
const (
	SubjReservationConfirmed  = "reservation.confirmed.v1"
	SubjReservationCheckedIn  = "reservation.checked_in.v1"
	SubjReservationCheckedOut = "reservation.checked_out.v1"
	SubjReservationCancelled  = "reservation.cancelled.v1"
	SubjReservationExpired    = "reservation.expired.v1"
	SubjBillingInvoiceIssued  = "billing.invoice.issued.v1"
	SubjBillingInvoiceOverdue = "billing.invoice.overdue.v1"
	SubjBillingInvoicePaid    = "billing.invoice.paid.v1"
	SubjPaymentSucceeded      = "payment.succeeded.v1"
	SubjPaymentFailed         = "payment.failed.v1"
)
