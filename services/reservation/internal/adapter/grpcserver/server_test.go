// Tests untuk Server (gRPC handler). Konstruksi server pakai concrete usecase
// types yang di-wire dengan fakes implementing port interfaces (sama pola
// dengan usecase tests). Tidak butuh bufconn — panggil handler langsung.
package grpcserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ajiperdana/parkir-pintar/pkg/grpcutil"
	"github.com/ajiperdana/parkir-pintar/pkg/idempotency"
	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
	reservationv1 "github.com/ajiperdana/parkir-pintar/proto/gen/reservation/v1"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

// ---------------- fakes (port impls) ----------------

type fakeResRepo struct {
	mu             sync.Mutex
	byID           map[uuid.UUID]*domain.Reservation
	activeByDriver map[string]*domain.Reservation
	getByIDErr     error
	updateErr      error
}

func newFakeResRepo() *fakeResRepo {
	return &fakeResRepo{
		byID:           map[uuid.UUID]*domain.Reservation{},
		activeByDriver: map[string]*domain.Reservation{},
	}
}

func (r *fakeResRepo) Create(_ context.Context, res *domain.Reservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[res.ID] = res
	return nil
}
func (r *fakeResRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	res, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrReservationNotFound
	}
	return res, nil
}
func (r *fakeResRepo) UpdateState(_ context.Context, res *domain.Reservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateErr != nil {
		return r.updateErr
	}
	r.byID[res.ID] = res
	return nil
}
func (r *fakeResRepo) FindActiveByDriverID(_ context.Context, driverID string) (*domain.Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if res, ok := r.activeByDriver[driverID]; ok {
		return res, nil
	}
	return nil, domain.ErrReservationNotFound
}
func (r *fakeResRepo) FindExpiredConfirmed(_ context.Context, _ time.Time, _ int) ([]*domain.Reservation, error) {
	return nil, nil
}

type fakeSpotRepo struct {
	mu              sync.Mutex
	byID            map[uuid.UUID]*domain.Spot
	pickReturn      *domain.Spot
	availability    *usecase.Availability
	availabilityErr error
	listResult      []*domain.Spot
	listTotal       int
	listErr         error
}

func newFakeSpotRepo() *fakeSpotRepo { return &fakeSpotRepo{byID: map[uuid.UUID]*domain.Spot{}} }

func (s *fakeSpotRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Spot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	spot, ok := s.byID[id]
	if !ok {
		return nil, domain.ErrSpotNotFound
	}
	cp := *spot
	return &cp, nil
}
func (s *fakeSpotRepo) PickAvailable(_ context.Context, _ domain.VehicleType) (*domain.Spot, error) {
	if s.pickReturn == nil {
		return nil, domain.ErrSpotNotFound
	}
	cp := *s.pickReturn
	return &cp, nil
}
func (s *fakeSpotRepo) List(_ context.Context, _ usecase.SpotFilter) ([]*domain.Spot, int, error) {
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	return s.listResult, s.listTotal, nil
}
func (s *fakeSpotRepo) MarkHeld(_ context.Context, id uuid.UUID, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if spot, ok := s.byID[id]; ok {
		spot.Status = domain.SpotHeld
	}
	return nil
}
func (s *fakeSpotRepo) MarkAvailable(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if spot, ok := s.byID[id]; ok {
		spot.Status = domain.SpotAvailable
	}
	return nil
}
func (s *fakeSpotRepo) MarkOccupied(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if spot, ok := s.byID[id]; ok {
		spot.Status = domain.SpotOccupied
	}
	return nil
}
func (s *fakeSpotRepo) GetAvailability(_ context.Context) (*usecase.Availability, error) {
	if s.availabilityErr != nil {
		return nil, s.availabilityErr
	}
	return s.availability, nil
}

type fakeEvents struct{}

func (e *fakeEvents) PublishReservationConfirmed(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (e *fakeEvents) PublishReservationCheckedIn(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (e *fakeEvents) PublishReservationCheckedOut(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (e *fakeEvents) PublishReservationCancelled(_ context.Context, _ *domain.Reservation) error {
	return nil
}
func (e *fakeEvents) PublishReservationExpired(_ context.Context, _ *domain.Reservation) error {
	return nil
}

type fakeLocker struct{ ok bool }

func (l *fakeLocker) Acquire(_ context.Context, _ string, _ time.Duration) (func(context.Context) error, bool, error) {
	if !l.ok {
		return nil, false, nil
	}
	return func(_ context.Context) error { return nil }, true, nil
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

// ---------------- helper builder ----------------

type serverFixture struct {
	srv      *Server
	resRepo  *fakeResRepo
	spotRepo *fakeSpotRepo
}

func newFixture(t *testing.T) *serverFixture {
	t.Helper()
	resRepo := newFakeResRepo()
	spotRepo := newFakeSpotRepo()
	events := &fakeEvents{}
	locker := &fakeLocker{ok: true}
	clk := &fakeClock{t: time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)}

	createUC := &usecase.CreateReservation{
		Reservations: resRepo,
		Spots:        spotRepo,
		Locker:       locker,
		Events:       events,
		Clock:        clk,
		HoldDuration: time.Hour,
		SpotLockTTL:  10 * time.Second,
	}
	checkInUC := &usecase.CheckIn{Reservations: resRepo, Spots: spotRepo, Events: events, Clock: clk}
	checkOutUC := &usecase.CheckOut{Reservations: resRepo, Spots: spotRepo, Events: events, Clock: clk}
	cancelUC := &usecase.Cancel{Reservations: resRepo, Spots: spotRepo, Events: events, Clock: clk}
	availUC := &usecase.GetAvailability{Spots: spotRepo}

	srv := &Server{
		CreateRes:      createUC,
		CheckInUC:      checkInUC,
		CheckOutUC:     checkOutUC,
		CancelUC:       cancelUC,
		AvailabilityUC: availUC,
		Reservations:   resRepo,
		Spots:          spotRepo,
		Idempotency:    idempotency.NewMemoryStore(),
		IdemTTL:        time.Hour,
		Logger:         zap.NewNop(),
	}
	return &serverFixture{srv: srv, resRepo: resRepo, spotRepo: spotRepo}
}

// putSpot inserts an AVAILABLE car spot at floor 2.
func putSpot(t *testing.T, f *serverFixture, vt domain.VehicleType) *domain.Spot {
	t.Helper()
	s := &domain.Spot{
		ID:          uuid.New(),
		FloorID:     uuid.New(),
		FloorLevel:  2,
		Code:        "F2-C-001",
		VehicleType: vt,
		Status:      domain.SpotAvailable,
		Version:     1,
	}
	f.spotRepo.byID[s.ID] = s
	f.spotRepo.pickReturn = s
	return s
}

// ctxWithIdem returns a ctx that carries x-idempotency-key in grpc metadata.
func ctxWithIdem(key string) context.Context {
	md := metadata.New(map[string]string{grpcutil.HeaderIdempotencyKey: key})
	return metadata.NewIncomingContext(context.Background(), md)
}

// ---------------- CreateReservation ----------------

func TestCreateReservation_HappyPath_System(t *testing.T) {
	f := newFixture(t)
	putSpot(t, f, domain.VehicleCar)

	req := &reservationv1.CreateReservationRequest{
		DriverId:    "drv-1",
		PlateNo:     "B1234AB",
		VehicleType: commonv1.VehicleType_CAR,
		Mode:        commonv1.AssignmentMode_SYSTEM,
		PaymentMode: commonv1.BillingMode_AUTO,
	}
	out, err := f.srv.CreateReservation(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Reservation.Id)
	assert.Equal(t, "drv-1", out.Reservation.DriverId)
	assert.Equal(t, "B1234AB", out.Reservation.PlateNo)
	assert.Equal(t, commonv1.VehicleType_CAR, out.Reservation.Spot.VehicleType)
	assert.Equal(t, commonv1.ReservationState_CONFIRMED, out.Reservation.State)
	assert.Equal(t, commonv1.BillingMode_AUTO, out.Reservation.PaymentMode)
	assert.Equal(t, BookingFeeIDR, out.BookingFee.Amount)
	assert.Equal(t, "IDR", out.BookingFee.Currency)
}

func TestCreateReservation_WithExplicitStartAt(t *testing.T) {
	f := newFixture(t)
	putSpot(t, f, domain.VehicleCar)

	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	req := &reservationv1.CreateReservationRequest{
		DriverId:    "drv-1",
		PlateNo:     "B1234AB",
		VehicleType: commonv1.VehicleType_CAR,
		PaymentMode: commonv1.BillingMode_AUTO,
		StartAt:     timestamppb.New(start),
	}
	out, err := f.srv.CreateReservation(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, out.Reservation.StartAt.AsTime().Equal(start))
}

func TestCreateReservation_UserMode_WithSpotID(t *testing.T) {
	f := newFixture(t)
	spot := putSpot(t, f, domain.VehicleCar)
	f.spotRepo.pickReturn = nil // force USER path

	req := &reservationv1.CreateReservationRequest{
		DriverId:    "drv-1",
		PlateNo:     "B1234AB",
		VehicleType: commonv1.VehicleType_CAR,
		Mode:        commonv1.AssignmentMode_USER,
		PaymentMode: commonv1.BillingMode_MANUAL,
	}
	sid := spot.ID.String()
	req.SpotId = &sid

	out, err := f.srv.CreateReservation(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, spot.ID.String(), out.Reservation.Spot.Id)
	assert.Equal(t, commonv1.BillingMode_MANUAL, out.Reservation.PaymentMode)
}

func TestCreateReservation_InvalidSpotID_ReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	putSpot(t, f, domain.VehicleCar)
	bad := "not-a-uuid"
	req := &reservationv1.CreateReservationRequest{
		DriverId:    "drv-1",
		PlateNo:     "B1234AB",
		VehicleType: commonv1.VehicleType_CAR,
		Mode:        commonv1.AssignmentMode_USER,
		PaymentMode: commonv1.BillingMode_AUTO,
		SpotId:      &bad,
	}
	_, err := f.srv.CreateReservation(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCreateReservation_InvalidVehicleType_ReturnsInvalidArgument(t *testing.T) {
	f := newFixture(t)
	req := &reservationv1.CreateReservationRequest{
		DriverId:    "drv-1",
		PlateNo:     "B1234AB",
		VehicleType: commonv1.VehicleType_VEHICLE_TYPE_UNSPECIFIED,
		PaymentMode: commonv1.BillingMode_AUTO,
	}
	_, err := f.srv.CreateReservation(context.Background(), req)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateReservation_InvalidPaymentMode_ReturnsInvalidArgument(t *testing.T) {
	f := newFixture(t)
	req := &reservationv1.CreateReservationRequest{
		DriverId:    "drv-1",
		PlateNo:     "B1234AB",
		VehicleType: commonv1.VehicleType_CAR,
		PaymentMode: commonv1.BillingMode_BILLING_MODE_UNSPECIFIED,
	}
	_, err := f.srv.CreateReservation(context.Background(), req)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateReservation_IdempotentReplay_ReturnsCachedSameResponse(t *testing.T) {
	f := newFixture(t)
	putSpot(t, f, domain.VehicleCar)

	ctx := ctxWithIdem("idem-key-1")
	req := &reservationv1.CreateReservationRequest{
		DriverId:    "drv-1",
		PlateNo:     "B1234AB",
		VehicleType: commonv1.VehicleType_CAR,
		PaymentMode: commonv1.BillingMode_AUTO,
	}
	r1, err := f.srv.CreateReservation(ctx, req)
	require.NoError(t, err)

	// Replay with same key.
	r2, err := f.srv.CreateReservation(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, r1.Reservation.Id, r2.Reservation.Id, "replay should return same reservation id")
}

// ---------------- GetReservation ----------------

func TestGetReservation_HappyPath(t *testing.T) {
	f := newFixture(t)
	spot := putSpot(t, f, domain.VehicleCar)
	r, err := domain.New("drv-1", "B1", domain.VehicleCar, domain.AssignmentSystem,
		domain.PaymentAuto, spot.ID, time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), "")
	require.NoError(t, err)
	f.resRepo.byID[r.ID] = r

	got, err := f.srv.GetReservation(context.Background(), &reservationv1.GetReservationRequest{Id: r.ID.String()})
	require.NoError(t, err)
	assert.Equal(t, r.ID.String(), got.Id)
	assert.Equal(t, "drv-1", got.DriverId)
}

func TestGetReservation_BadUUID(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.GetReservation(context.Background(), &reservationv1.GetReservationRequest{Id: "nope"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetReservation_NotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.GetReservation(context.Background(), &reservationv1.GetReservationRequest{Id: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetReservation_RepoError(t *testing.T) {
	f := newFixture(t)
	f.resRepo.getByIDErr = errors.New("db down")
	_, err := f.srv.GetReservation(context.Background(), &reservationv1.GetReservationRequest{Id: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------- CancelReservation ----------------

func TestCancelReservation_HappyPath(t *testing.T) {
	f := newFixture(t)
	spot := putSpot(t, f, domain.VehicleCar)
	r, err := domain.New("drv-1", "B1", domain.VehicleCar, domain.AssignmentSystem,
		domain.PaymentAuto, spot.ID, time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), "")
	require.NoError(t, err)
	f.resRepo.byID[r.ID] = r

	got, err := f.srv.CancelReservation(context.Background(), &reservationv1.CancelReservationRequest{
		Id: r.ID.String(), Reason: "changed mind",
	})
	require.NoError(t, err)
	assert.Equal(t, commonv1.ReservationState_CANCELLED, got.State)
}

func TestCancelReservation_BadUUID(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.CancelReservation(context.Background(), &reservationv1.CancelReservationRequest{Id: "x"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCancelReservation_NotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.CancelReservation(context.Background(), &reservationv1.CancelReservationRequest{Id: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---------------- CheckIn ----------------

func TestCheckIn_HappyPath(t *testing.T) {
	f := newFixture(t)
	spot := putSpot(t, f, domain.VehicleCar)
	r, err := domain.New("drv-1", "B1", domain.VehicleCar, domain.AssignmentSystem,
		domain.PaymentAuto, spot.ID, time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), "")
	require.NoError(t, err)
	f.resRepo.byID[r.ID] = r

	got, err := f.srv.CheckIn(context.Background(), &reservationv1.CheckInRequest{Id: r.ID.String()})
	require.NoError(t, err)
	assert.Equal(t, commonv1.ReservationState_CHECKED_IN, got.State)
	assert.NotNil(t, got.CheckinAt)
}

func TestCheckIn_BadUUID(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.CheckIn(context.Background(), &reservationv1.CheckInRequest{Id: "x"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCheckIn_NotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.CheckIn(context.Background(), &reservationv1.CheckInRequest{Id: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---------------- CheckOut ----------------

func TestCheckOut_HappyPath(t *testing.T) {
	f := newFixture(t)
	spot := putSpot(t, f, domain.VehicleCar)
	r, err := domain.New("drv-1", "B1", domain.VehicleCar, domain.AssignmentSystem,
		domain.PaymentAuto, spot.ID, time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), "")
	require.NoError(t, err)
	// promote to CHECKED_IN first so CheckOut transition is valid.
	require.NoError(t, r.CheckIn(time.Date(2026, 5, 16, 9, 30, 0, 0, time.UTC)))
	f.resRepo.byID[r.ID] = r

	got, err := f.srv.CheckOut(context.Background(), &reservationv1.CheckOutRequest{Id: r.ID.String()})
	require.NoError(t, err)
	assert.Equal(t, commonv1.ReservationState_CHECKED_OUT, got.State)
}

func TestCheckOut_BadUUID(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.CheckOut(context.Background(), &reservationv1.CheckOutRequest{Id: "x"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCheckOut_NotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.CheckOut(context.Background(), &reservationv1.CheckOutRequest{Id: uuid.NewString()})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---------------- GetAvailability ----------------

func TestGetAvailability_HappyPath(t *testing.T) {
	f := newFixture(t)
	areaID := uuid.New()
	f.spotRepo.availability = &usecase.Availability{
		ParkingAreaID:   areaID,
		ParkingAreaName: "Main",
		AsOf:            time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC),
		Floors: []usecase.FloorAvailability{
			{Level: 1, CarAvailable: 5, CarCapacity: 10, MotorAvailable: 3, MotorCapacity: 5},
			{Level: 2, CarAvailable: 2, CarCapacity: 8, MotorAvailable: 1, MotorCapacity: 4},
		},
	}
	out, err := f.srv.GetAvailability(context.Background(), &reservationv1.GetAvailabilityRequest{})
	require.NoError(t, err)
	assert.Equal(t, areaID.String(), out.ParkingAreaId)
	assert.Equal(t, "Main", out.ParkingAreaName)
	assert.Equal(t, int32(7), out.TotalCarAvailable)   // 5 + 2
	assert.Equal(t, int32(4), out.TotalMotorAvailable) // 3 + 1
	assert.Len(t, out.Floors, 2)
}

func TestGetAvailability_RepoError(t *testing.T) {
	f := newFixture(t)
	f.spotRepo.availabilityErr = errors.New("db error")
	_, err := f.srv.GetAvailability(context.Background(), &reservationv1.GetAvailabilityRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------- ListSpots ----------------

func TestListSpots_NoFilter_GroupedByFloor(t *testing.T) {
	f := newFixture(t)
	s1 := &domain.Spot{ID: uuid.New(), FloorLevel: 1, Code: "F1-C-001", VehicleType: domain.VehicleCar, Status: domain.SpotAvailable}
	s2 := &domain.Spot{ID: uuid.New(), FloorLevel: 1, Code: "F1-C-002", VehicleType: domain.VehicleCar, Status: domain.SpotHeld}
	s3 := &domain.Spot{ID: uuid.New(), FloorLevel: 2, Code: "F2-C-001", VehicleType: domain.VehicleCar, Status: domain.SpotAvailable}
	f.spotRepo.listResult = []*domain.Spot{s1, s2, s3}
	f.spotRepo.listTotal = 3

	out, err := f.srv.ListSpots(context.Background(), &reservationv1.ListSpotsRequest{})
	require.NoError(t, err)
	require.Len(t, out.Floors, 2)
	assert.Equal(t, int32(3), out.Total)
	// Floor 1 should have 1 available (only s1), capacity 2 (s1, s2).
	assert.Equal(t, int32(1), out.Floors[0].Level)
	assert.Equal(t, int32(2), out.Floors[0].Capacity)
	assert.Equal(t, int32(1), out.Floors[0].Available)
	// Floor 2: 1 available
	assert.Equal(t, int32(2), out.Floors[1].Level)
	assert.Equal(t, int32(1), out.Floors[1].Capacity)
	assert.Equal(t, int32(1), out.Floors[1].Available)
}

func TestListSpots_WithFilters(t *testing.T) {
	f := newFixture(t)
	f.spotRepo.listResult = nil
	f.spotRepo.listTotal = 0

	floor := int32(2)
	vt := commonv1.VehicleType_CAR
	st := commonv1.SpotStatus_AVAILABLE
	req := &reservationv1.ListSpotsRequest{
		FloorLevel:  &floor,
		VehicleType: &vt,
		Status:      &st,
	}
	out, err := f.srv.ListSpots(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, out.Floors)
	assert.Equal(t, int32(0), out.Total)
}

func TestListSpots_InvalidVehicleTypeWhenSet(t *testing.T) {
	f := newFixture(t)
	// Construct a VehicleType pointer with an out-of-range value to trigger the
	// vehicleTypeFromProto error branch. Use a known invalid (negative) enum
	// value via casting.
	bogus := commonv1.VehicleType(99)
	req := &reservationv1.ListSpotsRequest{VehicleType: &bogus}
	_, err := f.srv.ListSpots(context.Background(), req)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListSpots_RepoError(t *testing.T) {
	f := newFixture(t)
	f.spotRepo.listErr = errors.New("query failed")
	_, err := f.srv.ListSpots(context.Background(), &reservationv1.ListSpotsRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------- Register (smoke) ----------------

func TestRegister_DoesNotPanic(t *testing.T) {
	f := newFixture(t)
	// We don't spin up a real grpc.Server; just call the method via reflection
	// is overkill. Instead, assert it doesn't panic with a nil dereference by
	// constructing a real *grpc.Server. Importing google.golang.org/grpc here
	// is cheap (already a transitive dep). Keep test lightweight.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Register panicked: %v", r)
		}
	}()
	gs := grpc.NewServer()
	f.srv.Register(gs)
	gs.Stop()
}
