package nats

import (
	"context"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/usecase"
)

type Publisher struct{ bus eventbus.Publisher }

func NewPublisher(bus eventbus.Publisher) *Publisher { return &Publisher{bus: bus} }

func (p *Publisher) PublishPaymentSucceeded(ctx context.Context, pay *domain.Payment) error {
	return p.publish(ctx, eventbus.SubjPaymentSucceeded, "payment.succeeded.v1", pay)
}

func (p *Publisher) PublishPaymentFailed(ctx context.Context, pay *domain.Payment) error {
	return p.publish(ctx, eventbus.SubjPaymentFailed, "payment.failed.v1", pay)
}

func (p *Publisher) publish(ctx context.Context, subject, eventType string, pay *domain.Payment) error {
	pl := usecase.HelperEncode(pay)
	env, err := eventbus.Encode(uuid.NewString(), eventType, "payment", pay.ID.String(), pl)
	if err != nil {
		return err
	}
	return p.bus.Publish(ctx, subject, env)
}

type Noop struct{}

func (Noop) PublishPaymentSucceeded(_ context.Context, _ *domain.Payment) error { return nil }
func (Noop) PublishPaymentFailed(_ context.Context, _ *domain.Payment) error    { return nil }
