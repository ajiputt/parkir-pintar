package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

// seedConfirmed creates a CONFIRMED reservation in the fake repo with
// matching spot. Returns the reservation pointer.
func seedConfirmed(t *testing.T, repo *fakeReservationRepo, spots *fakeSpotRepo) *domain.Reservation {
	t.Helper()
	spotID := uuid.New()
	startAt := time.Now()
	r, err := domain.New("drv-1", "B 1234 ABC", domain.VehicleCar,
		domain.AssignmentSystem, domain.PaymentAuto,
		spotID, startAt, startAt.Add(time.Hour), "key-1")
	require.NoError(t, err)
	repo.byID[r.ID] = r
	spots.put(&domain.Spot{ID: spotID, VehicleType: domain.VehicleCar, Status: domain.SpotHeld})
	return r
}

// ----- CheckIn -----

func TestCheckIn_HappyPath(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}
	clk := &fakeClock{t: time.Now()}
	r := seedConfirmed(t, repo, spots)

	uc := &usecase.CheckIn{Reservations: repo, Spots: spots, Events: pub, Clock: clk, TxRunner: newFakeTxRunner()}
	got, err := uc.Execute(context.Background(), r.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateCheckedIn, got.State)
	assert.NotEmpty(t, repo.updateCalls)
	assert.Contains(t, spots.markOccupiedCalls, r.SpotID)
	assert.Len(t, pub.checkedIn, 1)
}

func TestCheckIn_NotFound_ReturnsErr(t *testing.T) {
	repo := newFakeReservationRepo()
	uc := &usecase.CheckIn{Reservations: repo, Spots: newFakeSpotRepo(), Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrReservationNotFound)
}

func TestCheckIn_ExpiredHold_RejectsBeforeUpdate(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	r := seedConfirmed(t, repo, spots)
	clk := &fakeClock{t: r.ExpiresAt.Add(time.Minute)} // past expiry

	uc := &usecase.CheckIn{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: clk, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID)
	assert.ErrorIs(t, err, domain.ErrHoldExpired)
	assert.Empty(t, repo.updateCalls, "should not persist on failed transition")
}

func TestCheckIn_UpdateStateError_Propagates(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	r := seedConfirmed(t, repo, spots)
	boom := errors.New("db down")
	repo.updateErr = boom

	uc := &usecase.CheckIn{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID)
	assert.ErrorIs(t, err, boom)
}

func TestCheckIn_MarkOccupiedError_Propagates(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	r := seedConfirmed(t, repo, spots)
	spots.markOccupiedErr = errors.New("spot stale")

	uc := &usecase.CheckIn{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID)
	assert.Error(t, err)
}

func TestCheckIn_PublishError_StillReturnsSuccess(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{checkInErr: errors.New("nats down")}
	r := seedConfirmed(t, repo, spots)

	uc := &usecase.CheckIn{Reservations: repo, Spots: spots, Events: pub, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	got, err := uc.Execute(context.Background(), r.ID)
	require.NoError(t, err, "publish error must not fail the use case")
	assert.Equal(t, domain.StateCheckedIn, got.State)
}

// ----- CheckOut -----

func TestCheckOut_HappyPath(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}
	clk := &fakeClock{t: time.Now()}

	r := seedConfirmed(t, repo, spots)
	require.NoError(t, r.CheckIn(clk.t))

	uc := &usecase.CheckOut{Reservations: repo, Spots: spots, Events: pub, Clock: clk, TxRunner: newFakeTxRunner()}
	got, err := uc.Execute(context.Background(), r.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StateCheckedOut, got.State)
	assert.Contains(t, spots.markAvailableCalls, r.SpotID)
	assert.Len(t, pub.checkedOut, 1)
}

func TestCheckOut_NotFound_ReturnsErr(t *testing.T) {
	repo := newFakeReservationRepo()
	uc := &usecase.CheckOut{Reservations: repo, Spots: newFakeSpotRepo(), Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrReservationNotFound)
}

func TestCheckOut_InvalidState_Rejects(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	r := seedConfirmed(t, repo, spots) // still CONFIRMED, not CheckedIn

	uc := &usecase.CheckOut{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID)
	assert.ErrorIs(t, err, domain.ErrInvalidStateTransition)
}

func TestCheckOut_UpdateError_Propagates(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	clk := &fakeClock{t: time.Now()}
	r := seedConfirmed(t, repo, spots)
	require.NoError(t, r.CheckIn(clk.t))
	repo.updateErr = errors.New("db down")

	uc := &usecase.CheckOut{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: clk, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID)
	assert.Error(t, err)
}

func TestCheckOut_MarkAvailableError_Propagates(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	clk := &fakeClock{t: time.Now()}
	r := seedConfirmed(t, repo, spots)
	require.NoError(t, r.CheckIn(clk.t))
	spots.markAvailableErr = errors.New("stale spot")

	uc := &usecase.CheckOut{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: clk, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID)
	assert.Error(t, err)
}

// ----- Cancel -----

func TestCancel_HappyPath(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{}
	r := seedConfirmed(t, repo, spots)

	uc := &usecase.Cancel{Reservations: repo, Spots: spots, Events: pub, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	got, err := uc.Execute(context.Background(), r.ID, "changed mind")
	require.NoError(t, err)
	assert.Equal(t, domain.StateCancelled, got.State)
	assert.Contains(t, spots.markAvailableCalls, r.SpotID)
	assert.Len(t, pub.cancelled, 1)
}

func TestCancel_NotFound_ReturnsErr(t *testing.T) {
	repo := newFakeReservationRepo()
	uc := &usecase.Cancel{Reservations: repo, Spots: newFakeSpotRepo(), Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), uuid.New(), "")
	assert.ErrorIs(t, err, domain.ErrReservationNotFound)
}

func TestCancel_AlreadyCancelled_Rejects(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	r := seedConfirmed(t, repo, spots)
	require.NoError(t, r.Cancel(time.Now()))

	uc := &usecase.Cancel{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID, "")
	assert.ErrorIs(t, err, domain.ErrInvalidStateTransition)
}

func TestCancel_UpdateError_Propagates(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	r := seedConfirmed(t, repo, spots)
	repo.updateErr = errors.New("db down")

	uc := &usecase.Cancel{Reservations: repo, Spots: spots, Events: &fakeEventPublisher{}, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	_, err := uc.Execute(context.Background(), r.ID, "")
	assert.Error(t, err)
}

func TestCancel_PublishError_StillReturnsSuccess(t *testing.T) {
	repo := newFakeReservationRepo()
	spots := newFakeSpotRepo()
	pub := &fakeEventPublisher{cancelErr: errors.New("nats down")}
	r := seedConfirmed(t, repo, spots)

	uc := &usecase.Cancel{Reservations: repo, Spots: spots, Events: pub, Clock: &fakeClock{t: time.Now()}, TxRunner: newFakeTxRunner()}
	got, err := uc.Execute(context.Background(), r.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StateCancelled, got.State)
}
