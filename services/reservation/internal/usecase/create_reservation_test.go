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

// helper — builds a stock CreateReservation use case with happy-path defaults.
type createDeps struct {
	repo    *fakeReservationRepo
	spots   *fakeSpotRepo
	pub     *fakeEventPublisher
	locker  *fakeLocker
	clock   *fakeClock
	checker *fakeOverdueChecker
}

func newCreateUC(t *testing.T) (*usecase.CreateReservation, *createDeps) {
	t.Helper()
	deps := &createDeps{
		repo:    newFakeReservationRepo(),
		spots:   newFakeSpotRepo(),
		pub:     &fakeEventPublisher{},
		locker:  &fakeLocker{acquireOK: true},
		clock:   &fakeClock{t: time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)},
		checker: &fakeOverdueChecker{},
	}
	uc := &usecase.CreateReservation{
		Reservations:   deps.repo,
		Spots:          deps.spots,
		Locker:         deps.locker,
		Events:         deps.pub,
		Clock:          deps.clock,
		HoldDuration:   15 * time.Minute,
		SpotLockTTL:    5 * time.Second,
		OverdueChecker: deps.checker,
	}
	return uc, deps
}

func validInput() usecase.CreateReservationInput {
	return usecase.CreateReservationInput{
		DriverID:       "drv-1",
		PlateNo:        "B 1234 ABC",
		VehicleType:    domain.VehicleCar,
		Mode:           domain.AssignmentSystem,
		PaymentMode:    domain.PaymentAuto,
		IdempotencyKey: "key-1",
	}
}

func seedAvailableSpot(spots *fakeSpotRepo, vt domain.VehicleType) *domain.Spot {
	s := &domain.Spot{
		ID:          uuid.New(),
		Code:        "F1-C-001",
		FloorLevel:  1,
		VehicleType: vt,
		Status:      domain.SpotAvailable,
	}
	spots.put(s)
	spots.pickReturn = s
	return s
}

// ----- happy paths -----

func TestCreateReservation_SystemMode_Success(t *testing.T) {
	uc, deps := newCreateUC(t)
	spot := seedAvailableSpot(deps.spots, domain.VehicleCar)

	out, err := uc.Execute(context.Background(), validInput())
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, spot.ID, out.Reservation.SpotID)
	assert.Equal(t, domain.StateConfirmed, out.Reservation.State)
	// lock acquired and released
	assert.Len(t, deps.locker.calls, 1)
	assert.Equal(t, 1, deps.locker.released)
	// reservation persisted, spot marked held, event published
	assert.Len(t, deps.repo.createCalls, 1)
	assert.Contains(t, deps.spots.markHeldCalls, spot.ID)
	assert.Len(t, deps.pub.confirmed, 1)
}

func TestCreateReservation_UserMode_Success(t *testing.T) {
	uc, deps := newCreateUC(t)
	spot := seedAvailableSpot(deps.spots, domain.VehicleCar)

	in := validInput()
	in.Mode = domain.AssignmentUser
	id := spot.ID
	in.SpotID = &id

	out, err := uc.Execute(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, spot.ID, out.Reservation.SpotID)
}

func TestCreateReservation_DefaultsStartAtToNow(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)

	in := validInput() // StartAt zero
	out, err := uc.Execute(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, deps.clock.t, out.Reservation.StartAt)
	assert.Equal(t, deps.clock.t.Add(15*time.Minute), out.Reservation.ExpiresAt)
}

func TestCreateReservation_RespectsExplicitStartAt(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)

	in := validInput()
	in.StartAt = deps.clock.t.Add(time.Hour)
	out, err := uc.Execute(context.Background(), in)
	require.NoError(t, err)
	assert.True(t, out.Reservation.StartAt.Equal(in.StartAt))
}

func TestCreateReservation_NoOverdueChecker_SkipsCheck(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	uc.OverdueChecker = nil

	_, err := uc.Execute(context.Background(), validInput())
	require.NoError(t, err)
}

func TestCreateReservation_OverdueCheckerError_SkipsCheckGracefully(t *testing.T) {
	// Per ADR-0014 — billing down should not block booking.
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.checker.err = errors.New("billing down")

	_, err := uc.Execute(context.Background(), validInput())
	require.NoError(t, err)
}

func TestCreateReservation_PublishError_DoesNotFail(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.pub.confirmErr = errors.New("nats down")

	out, err := uc.Execute(context.Background(), validInput())
	require.NoError(t, err)
	require.NotNil(t, out)
}

// ----- pre-check failures -----

func TestCreateReservation_DriverHasActiveReservation_Rejects(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.repo.activeByDriver["drv-1"] = &domain.Reservation{ID: uuid.New()}

	_, err := uc.Execute(context.Background(), validInput())
	assert.ErrorIs(t, err, domain.ErrDriverHasActiveReservation)
	// must not even acquire lock
	assert.Empty(t, deps.locker.calls)
}

func TestCreateReservation_FindActiveError_Propagates(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	boom := errors.New("db down")
	deps.repo.findActiveErr = boom

	_, err := uc.Execute(context.Background(), validInput())
	assert.ErrorIs(t, err, boom)
}

func TestCreateReservation_OverdueInvoice_Rejects(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.checker.count = 1

	_, err := uc.Execute(context.Background(), validInput())
	assert.ErrorIs(t, err, domain.ErrDriverHasOverdueInvoice)
}

// ----- resolveSpot branches -----

func TestCreateReservation_UserMode_MissingSpotID_Rejects(t *testing.T) {
	uc, _ := newCreateUC(t)
	in := validInput()
	in.Mode = domain.AssignmentUser
	in.SpotID = nil

	_, err := uc.Execute(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrSpotNotFound)
}

func TestCreateReservation_UserMode_MismatchedType_Rejects(t *testing.T) {
	uc, deps := newCreateUC(t)
	spot := &domain.Spot{
		ID:          uuid.New(),
		VehicleType: domain.VehicleMotor, // mismatch
		Status:      domain.SpotAvailable,
	}
	deps.spots.put(spot)

	in := validInput() // VehicleCar
	in.Mode = domain.AssignmentUser
	id := spot.ID
	in.SpotID = &id

	_, err := uc.Execute(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrSpotMismatchedType)
}

func TestCreateReservation_UserMode_NotAvailable_Rejects(t *testing.T) {
	uc, deps := newCreateUC(t)
	spot := &domain.Spot{
		ID:          uuid.New(),
		VehicleType: domain.VehicleCar,
		Status:      domain.SpotOccupied, // not available
	}
	deps.spots.put(spot)

	in := validInput()
	in.Mode = domain.AssignmentUser
	id := spot.ID
	in.SpotID = &id

	_, err := uc.Execute(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrSpotUnavailable)
}

func TestCreateReservation_UserMode_SpotGetError_Propagates(t *testing.T) {
	uc, deps := newCreateUC(t)
	deps.spots.getByIDErr = errors.New("db down")

	in := validInput()
	in.Mode = domain.AssignmentUser
	id := uuid.New()
	in.SpotID = &id

	_, err := uc.Execute(context.Background(), in)
	assert.Error(t, err)
}

func TestCreateReservation_SystemMode_NoSpotAvailable(t *testing.T) {
	uc, deps := newCreateUC(t)
	deps.spots.pickErr = domain.ErrSpotNotFound

	_, err := uc.Execute(context.Background(), validInput())
	assert.ErrorIs(t, err, domain.ErrNoSpotAvailable)
}

func TestCreateReservation_SystemMode_PickGenericError(t *testing.T) {
	uc, deps := newCreateUC(t)
	boom := errors.New("db meltdown")
	deps.spots.pickErr = boom

	_, err := uc.Execute(context.Background(), validInput())
	assert.ErrorIs(t, err, boom)
}

// ----- lock failures -----

func TestCreateReservation_LockNotAcquired_ReturnsContention(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.locker.acquireOK = false

	_, err := uc.Execute(context.Background(), validInput())
	assert.ErrorIs(t, err, domain.ErrLockContention)
}

func TestCreateReservation_LockerError_Propagates(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.locker.acquireErr = errors.New("redis down")

	_, err := uc.Execute(context.Background(), validInput())
	assert.Error(t, err)
}

// ----- post-lock re-check failures -----

func TestCreateReservation_StaleRead_SpotNoLongerAvailable(t *testing.T) {
	uc, deps := newCreateUC(t)
	spot := seedAvailableSpot(deps.spots, domain.VehicleCar)
	// flip status to simulate someone else grabbed it after first read
	deps.spots.byID[spot.ID].Status = domain.SpotOccupied

	_, err := uc.Execute(context.Background(), validInput())
	assert.ErrorIs(t, err, domain.ErrSpotUnavailable)
}

func TestCreateReservation_RepoCreateError_Propagates(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.repo.createErr = errors.New("unique violation")

	_, err := uc.Execute(context.Background(), validInput())
	assert.Error(t, err)
	// lock must still be released
	assert.Equal(t, 1, deps.locker.released)
}

func TestCreateReservation_MarkHeldError_Propagates(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)
	deps.spots.markHeldErr = errors.New("optimistic lock")

	_, err := uc.Execute(context.Background(), validInput())
	assert.Error(t, err)
}

func TestCreateReservation_InvalidInput_DomainError(t *testing.T) {
	uc, deps := newCreateUC(t)
	seedAvailableSpot(deps.spots, domain.VehicleCar)

	in := validInput()
	in.DriverID = "" // invalid

	_, err := uc.Execute(context.Background(), in)
	assert.ErrorIs(t, err, domain.ErrInvalidDriver)
}
