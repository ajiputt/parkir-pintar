package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

// confirmedRes returns a fresh CONFIRMED reservation ready for Expire().
func confirmedRes(t *testing.T) *domain.Reservation {
	t.Helper()
	now := time.Now()
	r, err := domain.New("drv-1", "B 1234 ABC", domain.VehicleCar,
		domain.AssignmentSystem, domain.PaymentAuto,
		uuid.New(), now, now.Add(time.Minute), "k")
	require.NoError(t, err)
	return r
}

func newWorker(repo *fakeReservationRepo, spots *fakeSpotRepo, pub *fakeEventPublisher) *usecase.ExpiryWorker {
	return &usecase.ExpiryWorker{
		Reservations: repo,
		Spots:        spots,
		Events:       pub,
		Clock:        &fakeClock{t: time.Now()},
		TxRunner:     newFakeTxRunner(),
		Interval:     5 * time.Millisecond,
		BatchSize:    10,
		Logger:       zap.NewNop(),
	}
}

// TestExpiryWorker_Run_StopsOnContextCancel — verifies the loop exits cleanly.
func TestExpiryWorker_Run_StopsOnContextCancel(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}
	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker did not stop after ctx cancel")
	}
}

// TestExpiryWorker_Tick_ExpiresAndPublishes — happy path through tick().
func TestExpiryWorker_Tick_ExpiresAndPublishes(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}

	r := confirmedRes(t)
	repo.findExpiredRes = []*domain.Reservation{r}
	spots.put(&domain.Spot{ID: r.SpotID, VehicleType: domain.VehicleCar, Status: domain.SpotHeld})

	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	assert.Equal(t, domain.StateExpired, r.State)
	assert.NotEmpty(t, repo.updateCalls)
	assert.Contains(t, spots.markAvailableCalls, r.SpotID)
	assert.Len(t, pub.expired, 1)
}

// TestExpiryWorker_Tick_EmptyResult_NoOps — no-op branch covered.
func TestExpiryWorker_Tick_EmptyResult_NoOps(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}
	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	assert.Empty(t, repo.updateCalls)
	assert.Empty(t, pub.expired)
}

// TestExpiryWorker_Tick_FindError_LogsAndContinues — error from FindExpired
// must not crash the loop.
func TestExpiryWorker_Tick_FindError_LogsAndContinues(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}
	repo.findExpiredErr = errors.New("db down")
	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	assert.Empty(t, repo.updateCalls)
}

// TestExpiryWorker_Tick_SkipsInvalidState — Reservation already in non-Confirmed
// state should be silently skipped (Expire() returns ErrInvalidStateTransition).
func TestExpiryWorker_Tick_SkipsInvalidState(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}

	r := confirmedRes(t)
	require.NoError(t, r.CheckIn(time.Now())) // moves to CHECKED_IN — can no longer Expire
	repo.findExpiredRes = []*domain.Reservation{r}

	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	assert.Empty(t, repo.updateCalls, "invalid-state res should be skipped")
	assert.Empty(t, pub.expired)
}

// TestExpiryWorker_Tick_UpdateError_ContinuesNext — when UpdateState fails for
// one reservation, the worker should log and continue.
func TestExpiryWorker_Tick_UpdateError_ContinuesNext(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}

	r := confirmedRes(t)
	repo.findExpiredRes = []*domain.Reservation{r}
	repo.updateErr = errors.New("db down")

	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	// updateErr is set, so updateCalls won't be appended; spot must NOT be
	// marked available because the update failed (loop `continue`).
	assert.Empty(t, spots.markAvailableCalls)
}

// TestExpiryWorker_Tick_MarkAvailableError_StillPublishes — release-spot error
// is logged but flow continues to publish.
func TestExpiryWorker_Tick_MarkAvailableError_StillPublishes(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}

	r := confirmedRes(t)
	repo.findExpiredRes = []*domain.Reservation{r}
	spots.markAvailableErr = errors.New("spot stale")

	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	assert.NotEmpty(t, repo.updateCalls)
	assert.Len(t, pub.expired, 1, "publish should run even if spot release failed")
}

// TestExpiryWorker_Tick_PublishError_LogsOnly — publish error is logged, not
// propagated.
func TestExpiryWorker_Tick_PublishError_LogsOnly(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{expireErr: errors.New("nats down")}

	r := confirmedRes(t)
	repo.findExpiredRes = []*domain.Reservation{r}

	w := newWorker(repo, spots, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	assert.Equal(t, domain.StateExpired, r.State)
	assert.NotEmpty(t, repo.updateCalls)
}
