// Package domain — entitas & aturan bisnis murni untuk Reservation.
// Tidak ada dependency ke library eksternal kecuali stdlib.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// State — siklus hidup reservation.
type State string

const (
	StateConfirmed  State = "CONFIRMED"
	StateCheckedIn  State = "CHECKED_IN"
	StateCheckedOut State = "CHECKED_OUT"
	StateCancelled  State = "CANCELLED"
	StateExpired    State = "EXPIRED"
)

// VehicleType.
type VehicleType string

const (
	VehicleCar   VehicleType = "CAR"
	VehicleMotor VehicleType = "MOTOR"
)

// AssignmentMode.
type AssignmentMode string

const (
	AssignmentSystem AssignmentMode = "SYSTEM"
	AssignmentUser   AssignmentMode = "USER"
)

// PaymentMode — driver pilih cara bayar saat booking. Lihat ADR-0014.
type PaymentMode string

const (
	PaymentAuto   PaymentMode = "AUTO"   // wallet auto-debit
	PaymentManual PaymentMode = "MANUAL" // QRIS scan, risk overdue
)

// Reservation — aggregate root.
//
// Time semantics (lihat ADR-0011):
//   - StartAt:    expected arrival. Default = now() saat booking instant.
//   - ExpiresAt:  hold deadline = StartAt + holdDuration. Driver harus check-in
//     sebelum ini, kalau tidak auto-cancel + no-show penalty.
//   - CheckInAt:  actual arrival, set saat CheckIn().
//   - CheckOutAt: actual departure, set saat CheckOut(). Billing dihitung dari
//     (CheckOutAt - CheckInAt).
//
// EndAt sengaja TIDAK ada — driver gak tau kapan keluar (parking ≠ hotel).
// Anti-overlap pakai status atomic + partial unique index, bukan tsrange.
type Reservation struct {
	ID             uuid.UUID
	DriverID       string
	SpotID         uuid.UUID
	PlateNo        string
	VehicleType    VehicleType
	State          State
	StartAt        time.Time
	ExpiresAt      time.Time
	CheckInAt      *time.Time
	CheckOutAt     *time.Time
	AssignmentMode AssignmentMode
	PaymentMode    PaymentMode // AUTO | MANUAL — required, lihat ADR-0014
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// New — factory dengan validasi.
//
// holdUntil = StartAt + holdDuration, dihitung di caller (usecase).
// payMode WAJIB AUTO atau MANUAL — lihat ADR-0014.
func New(
	driverID, plateNo string,
	vt VehicleType, mode AssignmentMode, payMode PaymentMode,
	spotID uuid.UUID,
	startAt, holdUntil time.Time,
	idemKey string,
) (*Reservation, error) {
	if driverID == "" {
		return nil, ErrInvalidDriver
	}
	if plateNo == "" {
		return nil, ErrInvalidPlate
	}
	if vt != VehicleCar && vt != VehicleMotor {
		return nil, ErrInvalidVehicleType
	}
	if payMode != PaymentAuto && payMode != PaymentManual {
		return nil, ErrInvalidPaymentMode
	}
	now := time.Now().UTC()
	return &Reservation{
		ID:             uuid.New(),
		DriverID:       driverID,
		SpotID:         spotID,
		PlateNo:        plateNo,
		VehicleType:    vt,
		State:          StateConfirmed,
		StartAt:        startAt,
		ExpiresAt:      holdUntil,
		AssignmentMode: mode,
		PaymentMode:    payMode,
		IdempotencyKey: idemKey,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// Cancel — transisi state. Hanya dari CONFIRMED yang valid.
func (r *Reservation) Cancel(now time.Time) error {
	if r.State != StateConfirmed {
		return ErrInvalidStateTransition
	}
	r.State = StateCancelled
	r.UpdatedAt = now
	return nil
}

// Expire — dipanggil oleh worker kalau lewat hold time tanpa check-in.
func (r *Reservation) Expire(now time.Time) error {
	if r.State != StateConfirmed {
		return ErrInvalidStateTransition
	}
	r.State = StateExpired
	r.UpdatedAt = now
	return nil
}

// CheckIn — driver hadir. Wajib state CONFIRMED dan belum lewat ExpiresAt.
func (r *Reservation) CheckIn(now time.Time) error {
	if r.State != StateConfirmed {
		return ErrInvalidStateTransition
	}
	if now.After(r.ExpiresAt) {
		return ErrHoldExpired
	}
	r.State = StateCheckedIn
	r.CheckInAt = ptrTime(now)
	r.UpdatedAt = now
	return nil
}

// CheckOut — driver pulang. Wajib state CHECKED_IN.
func (r *Reservation) CheckOut(now time.Time) error {
	if r.State != StateCheckedIn {
		return ErrInvalidStateTransition
	}
	r.State = StateCheckedOut
	r.CheckOutAt = ptrTime(now)
	r.UpdatedAt = now
	return nil
}

// IsHeld — apakah masih memegang spot (CONFIRMED atau CHECKED_IN).
func (r *Reservation) IsHeld() bool {
	return r.State == StateConfirmed || r.State == StateCheckedIn
}

func ptrTime(t time.Time) *time.Time { return &t }
