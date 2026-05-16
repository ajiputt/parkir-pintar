package nats

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// TestNoopPublisher_AllMethodsReturnNil verifies the noop publisher accepts
// any input (including nil) and never errors / panics.
func TestNoopPublisher_AllMethodsReturnNil(t *testing.T) {
	p := NoopPublisher{}
	ctx := context.Background()

	assert.NoError(t, p.PublishReservationConfirmed(ctx, nil))
	assert.NoError(t, p.PublishReservationCheckedIn(ctx, nil))
	assert.NoError(t, p.PublishReservationCheckedOut(ctx, nil))
	assert.NoError(t, p.PublishReservationCancelled(ctx, nil))
	assert.NoError(t, p.PublishReservationExpired(ctx, nil))

	// Also exercise with a non-nil reservation to confirm input doesn't matter.
	r := &domain.Reservation{}
	assert.NoError(t, p.PublishReservationConfirmed(ctx, r))
	assert.NoError(t, p.PublishReservationCheckedIn(ctx, r))
	assert.NoError(t, p.PublishReservationCheckedOut(ctx, r))
	assert.NoError(t, p.PublishReservationCancelled(ctx, r))
	assert.NoError(t, p.PublishReservationExpired(ctx, r))
}
