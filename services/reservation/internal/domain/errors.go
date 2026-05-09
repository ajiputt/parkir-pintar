package domain

import "github.com/ajiperdana/parkir-pintar/pkg/errs"

// Domain-specific errors. Code prefix: RES (reservation).
var (
	ErrInvalidDriver = errs.New(errs.KindInvalidArgument, "RES-001", "driver_id is required")
	ErrInvalidPlate  = errs.New(errs.KindInvalidArgument, "RES-002", "plate_no is required")
	// RES-003 (ErrInvalidWindow) di-DEPRECATED — end_at sudah dihapus per ADR-0011.
	ErrInvalidVehicleType     = errs.New(errs.KindInvalidArgument, "RES-004", "invalid vehicle_type")
	ErrInvalidPaymentMode     = errs.New(errs.KindInvalidArgument, "RES-005", "payment_mode required: AUTO or MANUAL")
	ErrInvalidStateTransition = errs.New(errs.KindFailedPrecondition, "RES-010", "invalid reservation state transition")
	ErrHoldExpired            = errs.New(errs.KindFailedPrecondition, "RES-011", "reservation hold has expired")

	ErrSpotNotFound       = errs.New(errs.KindNotFound, "RES-020", "spot not found")
	ErrSpotUnavailable    = errs.New(errs.KindConflict, "RES-021", "spot is not available")
	ErrSpotMismatchedType = errs.New(errs.KindInvalidArgument, "RES-022", "spot vehicle type does not match request")
	ErrNoSpotAvailable    = errs.New(errs.KindFailedPrecondition, "RES-023", "no available spot for the requested vehicle type")

	ErrReservationNotFound        = errs.New(errs.KindNotFound, "RES-030", "reservation not found")
	ErrDriverHasActiveReservation = errs.New(errs.KindConflict, "RES-031", "driver already has an active reservation — checkout or cancel the existing one first")
	ErrDriverHasOverdueInvoice    = errs.New(errs.KindFailedPrecondition, "RES-032", "driver has overdue invoice(s) — settle outstanding bills before booking")

	ErrLockContention = errs.New(errs.KindConflict, "RES-040", "another request holding the spot — please retry")
)
