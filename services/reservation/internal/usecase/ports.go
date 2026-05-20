// Package usecase — application services + port interfaces.
// Adapter kongkrit (postgres, redis, nats) di internal/adapter/*.
package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// ----- Ports (interfaces yang dibutuhkan usecase) -----

//go:generate mockgen -package=mock_usecase -source=ports.go -destination=../../_mock/usecase/ports_mock.go

// ReservationRepo — persistence reservation.
type ReservationRepo interface {
	// Create simpan reservation baru. Adapter map unique violation (23505) dari
	// partial index `one_active_reservation_per_spot` atau `one_active_reservation_per_driver`
	// ke domain.ErrSpotUnavailable atau ErrDriverHasActiveReservation. Lihat ADR-0011.
	Create(ctx context.Context, r *domain.Reservation) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Reservation, error)
	UpdateState(ctx context.Context, r *domain.Reservation) error
	// FindActiveByDriverID — return reservation aktif (CONFIRMED/CHECKED_IN) dari driver.
	// Return ErrReservationNotFound kalau tidak ada (caller bisa errors.Is check).
	FindActiveByDriverID(ctx context.Context, driverID string) (*domain.Reservation, error)
	// FindExpiredConfirmed — untuk worker. Pakai SKIP LOCKED untuk paralelisme.
	FindExpiredConfirmed(ctx context.Context, now time.Time, limit int) ([]*domain.Reservation, error)
}

// SpotRepo — inventory.
//
// Catatan tentang multi-area: ParkirPintar saat ini single-area. Adapter
// implementasi (postgres) decides default area — biasanya ambil semua spot
// (no area filter) atau filter ke parking_area default. Usecase tidak peduli
// area_id; itu deployment concern. Kalau ekspansi multi-tenant, port ini
// di-extend dengan tenant context (mis. dari middleware).
type SpotRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Spot, error)
	// PickAvailable — untuk SYSTEM-assigned. Atomic: kunci row pakai SELECT FOR UPDATE.
	PickAvailable(ctx context.Context, vt domain.VehicleType) (*domain.Spot, error)
	// List — untuk USER-assigned flow. Filter optional (nil = no filter on that field).
	// Return total count untuk pagination metadata.
	List(ctx context.Context, filter SpotFilter) (spots []*domain.Spot, total int, err error)
	// Lock + status change in transaction (helper).
	MarkHeld(ctx context.Context, id uuid.UUID, version int) error
	MarkAvailable(ctx context.Context, id uuid.UUID) error
	MarkOccupied(ctx context.Context, id uuid.UUID) error
	GetAvailability(ctx context.Context) (*Availability, error)
}

// SpotFilter — query criteria untuk SpotRepo.List.
//
// Semua nil = no filter pada field itu. Kombinasi filter di-AND.
type SpotFilter struct {
	VehicleType *domain.VehicleType
	Status      *domain.SpotStatus
	FloorLevel  *int
	Limit       int // default 50, max 200
	Offset      int // for pagination
}

// EventPublisher — emit domain event.
type EventPublisher interface {
	PublishReservationConfirmed(ctx context.Context, r *domain.Reservation) error
	PublishReservationCheckedIn(ctx context.Context, r *domain.Reservation) error
	PublishReservationCheckedOut(ctx context.Context, r *domain.Reservation) error
	PublishReservationCancelled(ctx context.Context, r *domain.Reservation) error
	PublishReservationExpired(ctx context.Context, r *domain.Reservation) error
}

// Locker — distributed lock (Redis Redlock atau in-mem).
type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (release func(context.Context) error, ok bool, err error)
}

// IdempotencyService — wrapper di atas pkg/idempotency.
// Usecase tidak peduli bagaimana implementasinya.
type IdempotencyService interface {
	Wrap(ctx context.Context, key, requestKey string, payload []byte, fn func(ctx context.Context) (any, error)) (any, error)
}

// Clock — time abstraction.
type Clock interface {
	Now() time.Time
}

// ----- DTO usecase (bukan transport DTO) -----

// CreateReservationInput.
//
// Field semantics (lihat ADR-0011 + ADR-0014):
//   - StartAt:     expected arrival, default = now untuk booking instant.
//   - PaymentMode: AUTO (wallet auto-debit) atau MANUAL (QRIS scan). REQUIRED.
//
// AreaID dihapus: single-area assumption. EndAt dihapus: ADR-0011.
type CreateReservationInput struct {
	DriverID       string
	PlateNo        string
	VehicleType    domain.VehicleType
	Mode           domain.AssignmentMode
	PaymentMode    domain.PaymentMode
	SpotID         *uuid.UUID
	StartAt        time.Time
	IdempotencyKey string
}

// OverdueChecker — port lookup overdue invoice count (ke billing service via gRPC).
//
// Cross-service call dipakai pre-check di CreateReservation untuk block driver
// yang punya tagihan tertunggak. Lihat ADR-0014.
type OverdueChecker interface {
	CountOverdueByDriverID(ctx context.Context, driverID string) (int, error)
}

// CreateReservationOutput.
type CreateReservationOutput struct {
	Reservation *domain.Reservation
	Spot        *domain.Spot
}

// Availability — read model untuk GetAvailability.
type Availability struct {
	ParkingAreaID   uuid.UUID
	ParkingAreaName string
	AsOf            time.Time
	Floors          []FloorAvailability
}

type FloorAvailability struct {
	Level          int
	CarAvailable   int
	CarCapacity    int
	MotorAvailable int
	MotorCapacity  int
}
