package usecase_test

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

// ----- fakeReservationRepo -----

type fakeReservationRepo struct {
	mu sync.Mutex

	byID           map[uuid.UUID]*domain.Reservation
	activeByDriver map[string]*domain.Reservation
	createCalls    []*domain.Reservation
	updateCalls    []*domain.Reservation
	createErr      error
	getByIDErr     error
	updateErr      error
	findActiveErr  error
	findExpiredRes []*domain.Reservation
	findExpiredErr error
}

func newFakeReservationRepo() *fakeReservationRepo {
	return &fakeReservationRepo{
		byID:           map[uuid.UUID]*domain.Reservation{},
		activeByDriver: map[string]*domain.Reservation{},
	}
}

func (r *fakeReservationRepo) Create(_ context.Context, res *domain.Reservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	r.byID[res.ID] = res
	r.createCalls = append(r.createCalls, res)
	return nil
}

// CreateTx — Tx variant. Fakes ignore tx param (no real DB).
func (r *fakeReservationRepo) CreateTx(ctx context.Context, _ pgx.Tx, res *domain.Reservation) error {
	return r.Create(ctx, res)
}

func (r *fakeReservationRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Reservation, error) {
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

func (r *fakeReservationRepo) UpdateState(_ context.Context, res *domain.Reservation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateErr != nil {
		return r.updateErr
	}
	r.byID[res.ID] = res
	r.updateCalls = append(r.updateCalls, res)
	return nil
}

// UpdateStateTx — Tx variant.
func (r *fakeReservationRepo) UpdateStateTx(ctx context.Context, _ pgx.Tx, res *domain.Reservation) error {
	return r.UpdateState(ctx, res)
}

func (r *fakeReservationRepo) FindActiveByDriverID(_ context.Context, driverID string) (*domain.Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.findActiveErr != nil {
		return nil, r.findActiveErr
	}
	if res, ok := r.activeByDriver[driverID]; ok {
		return res, nil
	}
	return nil, domain.ErrReservationNotFound
}

func (r *fakeReservationRepo) FindExpiredConfirmed(_ context.Context, _ time.Time, _ int) ([]*domain.Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.findExpiredErr != nil {
		return nil, r.findExpiredErr
	}
	out := make([]*domain.Reservation, len(r.findExpiredRes))
	copy(out, r.findExpiredRes)
	// clear after first delivery so background ticker loops don't double-expire.
	r.findExpiredRes = nil
	return out, nil
}

// ----- fakeSpotRepo -----

type fakeSpotRepo struct {
	mu sync.Mutex

	byID             map[uuid.UUID]*domain.Spot
	pickReturn       *domain.Spot
	pickErr          error
	getByIDErr       error
	markHeldErr      error
	markAvailableErr error
	markOccupiedErr  error
	availability     *usecase.Availability
	availabilityErr  error

	markHeldCalls      []uuid.UUID
	markAvailableCalls []uuid.UUID
	markOccupiedCalls  []uuid.UUID
}

func newFakeSpotRepo() *fakeSpotRepo {
	return &fakeSpotRepo{byID: map[uuid.UUID]*domain.Spot{}}
}

func (s *fakeSpotRepo) put(spot *domain.Spot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[spot.ID] = spot
}

func (s *fakeSpotRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Spot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getByIDErr != nil {
		return nil, s.getByIDErr
	}
	spot, ok := s.byID[id]
	if !ok {
		return nil, domain.ErrSpotNotFound
	}
	// return a copy so mutations downstream don't leak back
	cp := *spot
	return &cp, nil
}

func (s *fakeSpotRepo) PickAvailable(_ context.Context, _ domain.VehicleType) (*domain.Spot, error) {
	if s.pickErr != nil {
		return nil, s.pickErr
	}
	if s.pickReturn == nil {
		return nil, domain.ErrSpotNotFound
	}
	cp := *s.pickReturn
	return &cp, nil
}

func (s *fakeSpotRepo) List(_ context.Context, _ usecase.SpotFilter) ([]*domain.Spot, int, error) {
	return nil, 0, nil
}

func (s *fakeSpotRepo) MarkHeld(_ context.Context, id uuid.UUID, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markHeldErr != nil {
		return s.markHeldErr
	}
	s.markHeldCalls = append(s.markHeldCalls, id)
	if spot, ok := s.byID[id]; ok {
		spot.Status = domain.SpotHeld
	}
	return nil
}

// MarkHeldTx — Tx variant. Fakes ignore tx param.
func (s *fakeSpotRepo) MarkHeldTx(ctx context.Context, _ pgx.Tx, id uuid.UUID, version int) error {
	return s.MarkHeld(ctx, id, version)
}

func (s *fakeSpotRepo) MarkAvailable(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markAvailableErr != nil {
		return s.markAvailableErr
	}
	s.markAvailableCalls = append(s.markAvailableCalls, id)
	if spot, ok := s.byID[id]; ok {
		spot.Status = domain.SpotAvailable
	}
	return nil
}

// MarkAvailableTx — Tx variant.
func (s *fakeSpotRepo) MarkAvailableTx(ctx context.Context, _ pgx.Tx, id uuid.UUID) error {
	return s.MarkAvailable(ctx, id)
}

func (s *fakeSpotRepo) MarkOccupied(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markOccupiedErr != nil {
		return s.markOccupiedErr
	}
	s.markOccupiedCalls = append(s.markOccupiedCalls, id)
	if spot, ok := s.byID[id]; ok {
		spot.Status = domain.SpotOccupied
	}
	return nil
}

// MarkOccupiedTx — Tx variant.
func (s *fakeSpotRepo) MarkOccupiedTx(ctx context.Context, _ pgx.Tx, id uuid.UUID) error {
	return s.MarkOccupied(ctx, id)
}

func (s *fakeSpotRepo) GetAvailability(_ context.Context) (*usecase.Availability, error) {
	if s.availabilityErr != nil {
		return nil, s.availabilityErr
	}
	return s.availability, nil
}

// ----- fakeEventPublisher -----

type fakeEventPublisher struct {
	mu sync.Mutex

	confirmed   []*domain.Reservation
	checkedIn   []*domain.Reservation
	checkedOut  []*domain.Reservation
	cancelled   []*domain.Reservation
	expired     []*domain.Reservation
	confirmErr  error
	checkInErr  error
	checkOutErr error
	cancelErr   error
	expireErr   error
}

func (e *fakeEventPublisher) PublishReservationConfirmed(_ context.Context, r *domain.Reservation) error {
	if e.confirmErr != nil {
		return e.confirmErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.confirmed = append(e.confirmed, r)
	return nil
}

func (e *fakeEventPublisher) PublishReservationCheckedIn(_ context.Context, r *domain.Reservation) error {
	if e.checkInErr != nil {
		return e.checkInErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checkedIn = append(e.checkedIn, r)
	return nil
}

func (e *fakeEventPublisher) PublishReservationCheckedOut(_ context.Context, r *domain.Reservation) error {
	if e.checkOutErr != nil {
		return e.checkOutErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checkedOut = append(e.checkedOut, r)
	return nil
}

func (e *fakeEventPublisher) PublishReservationCancelled(_ context.Context, r *domain.Reservation) error {
	if e.cancelErr != nil {
		return e.cancelErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancelled = append(e.cancelled, r)
	return nil
}

func (e *fakeEventPublisher) PublishReservationExpired(_ context.Context, r *domain.Reservation) error {
	if e.expireErr != nil {
		return e.expireErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.expired = append(e.expired, r)
	return nil
}

// ----- fakeLocker -----

type fakeLocker struct {
	mu sync.Mutex

	acquireOK  bool
	acquireErr error
	releaseErr error
	calls      []string
	released   int
}

func (l *fakeLocker) Acquire(_ context.Context, key string, _ time.Duration) (func(context.Context) error, bool, error) {
	l.mu.Lock()
	l.calls = append(l.calls, key)
	l.mu.Unlock()
	if l.acquireErr != nil {
		return nil, false, l.acquireErr
	}
	if !l.acquireOK {
		return nil, false, nil
	}
	release := func(_ context.Context) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.released++
		return l.releaseErr
	}
	return release, true, nil
}

// ----- fakeClock -----

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

// ----- fakeOverdueChecker -----

type fakeOverdueChecker struct {
	count int
	err   error
}

func (c *fakeOverdueChecker) CountOverdueByDriverID(_ context.Context, _ string) (int, error) {
	return c.count, c.err
}

// ----- fakeTxRunner -----
//
// Test double untuk usecase.TxRunner. Tidak ada real DB tx — fakes ignore
// tx param. fn dipanggil dengan tx=nil. Kalau test mau simulate tx failure
// (commit/rollback fail), set fakeTxRunner.err.

type fakeTxRunner struct {
	err   error // simulate Begin/Commit failure
	calls int
}

func (t *fakeTxRunner) RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	t.calls++
	if t.err != nil {
		return t.err
	}
	// fn dipanggil dengan tx=nil — fakes punya Tx-variant yang ignore tx.
	// Kalau fn return error, simulate rollback (no commit). Caller dapat err.
	return fn(nil)
}

// newFakeTxRunner — helper supaya consistent dengan factory pattern lain.
func newFakeTxRunner() *fakeTxRunner { return &fakeTxRunner{} }
