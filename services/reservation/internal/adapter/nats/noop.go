package nats

import (
	"context"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// NoopPublisher — implementasi yang tidak melakukan apa-apa.
// Dipakai sebagai fallback saat NATS tidak tersedia (graceful degradation
// kompetensi #1.6 / #2.7).
type NoopPublisher struct{}

func (NoopPublisher) PublishReservationConfirmed(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (NoopPublisher) PublishReservationCheckedIn(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (NoopPublisher) PublishReservationCheckedOut(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (NoopPublisher) PublishReservationCancelled(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (NoopPublisher) PublishReservationExpired(_ context.Context, _ *domain.Reservation) error {
	return nil
}
