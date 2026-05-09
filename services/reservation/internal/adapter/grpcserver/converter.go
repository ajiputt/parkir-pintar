// Package grpcserver — converter helpers domain ↔ proto.
package grpcserver

import (
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

// vehicleTypeFromProto — map proto enum → domain.
//
// Return error untuk UNSPECIFIED supaya bug typo client (mis. "Car" lowercase)
// tidak silent-pass jadi kosong → dilanjut ke usecase yang failed downstream
// dengan error misleading.
func vehicleTypeFromProto(v commonv1.VehicleType) (domain.VehicleType, error) {
	switch v {
	case commonv1.VehicleType_CAR:
		return domain.VehicleCar, nil
	case commonv1.VehicleType_MOTOR:
		return domain.VehicleMotor, nil
	default:
		return "", domain.ErrInvalidVehicleType
	}
}

// vehicleTypeToProto — map domain → proto enum.
func vehicleTypeToProto(v domain.VehicleType) commonv1.VehicleType {
	switch v {
	case domain.VehicleCar:
		return commonv1.VehicleType_CAR
	case domain.VehicleMotor:
		return commonv1.VehicleType_MOTOR
	default:
		return commonv1.VehicleType_VEHICLE_TYPE_UNSPECIFIED
	}
}

// assignmentModeFromProto.
//
// UNSPECIFIED → default ke SYSTEM. Berbeda dengan VehicleType yang reject
// UNSPECIFIED, mode boleh empty di JSON karena SYSTEM adalah expected default
// behavior (driver mau spot apa aja). USER mode butuh spot_id eksplisit.
func assignmentModeFromProto(m commonv1.AssignmentMode) domain.AssignmentMode {
	switch m {
	case commonv1.AssignmentMode_USER:
		return domain.AssignmentUser
	default:
		// SYSTEM atau UNSPECIFIED → SYSTEM
		return domain.AssignmentSystem
	}
}

// assignmentModeToProto.
func assignmentModeToProto(m domain.AssignmentMode) commonv1.AssignmentMode {
	switch m {
	case domain.AssignmentUser:
		return commonv1.AssignmentMode_USER
	case domain.AssignmentSystem:
		return commonv1.AssignmentMode_SYSTEM
	default:
		return commonv1.AssignmentMode_ASSIGNMENT_MODE_UNSPECIFIED
	}
}

// paymentModeFromProto — required field, return error kalau UNSPECIFIED.
func paymentModeFromProto(m commonv1.BillingMode) (domain.PaymentMode, error) {
	switch m {
	case commonv1.BillingMode_AUTO:
		return domain.PaymentAuto, nil
	case commonv1.BillingMode_MANUAL:
		return domain.PaymentManual, nil
	default:
		return "", domain.ErrInvalidPaymentMode
	}
}

func paymentModeToProto(m domain.PaymentMode) commonv1.BillingMode {
	switch m {
	case domain.PaymentAuto:
		return commonv1.BillingMode_AUTO
	case domain.PaymentManual:
		return commonv1.BillingMode_MANUAL
	default:
		return commonv1.BillingMode_BILLING_MODE_UNSPECIFIED
	}
}

// stateToProto — domain.State → proto common.v1.ReservationState.
func stateToProto(s domain.State) commonv1.ReservationState {
	switch s {
	case domain.StateConfirmed:
		return commonv1.ReservationState_CONFIRMED
	case domain.StateCheckedIn:
		return commonv1.ReservationState_CHECKED_IN
	case domain.StateCheckedOut:
		return commonv1.ReservationState_CHECKED_OUT
	case domain.StateCancelled:
		return commonv1.ReservationState_CANCELLED
	case domain.StateExpired:
		return commonv1.ReservationState_EXPIRED
	default:
		return commonv1.ReservationState_RESERVATION_STATE_UNSPECIFIED
	}
}

// spotStatusToProto — domain.SpotStatus → proto common.v1.SpotStatus.
func spotStatusToProto(s domain.SpotStatus) commonv1.SpotStatus {
	switch s {
	case domain.SpotAvailable:
		return commonv1.SpotStatus_AVAILABLE
	case domain.SpotHeld:
		return commonv1.SpotStatus_HELD
	case domain.SpotOccupied:
		return commonv1.SpotStatus_OCCUPIED
	case domain.SpotOutOfService:
		return commonv1.SpotStatus_OUT_OF_SERVICE
	default:
		return commonv1.SpotStatus_SPOT_STATUS_UNSPECIFIED
	}
}

// spotStatusFromProto — proto enum → domain.SpotStatus.
// UNSPECIFIED → empty (caller cek dengan != nil filter).
func spotStatusFromProto(s commonv1.SpotStatus) domain.SpotStatus {
	switch s {
	case commonv1.SpotStatus_AVAILABLE:
		return domain.SpotAvailable
	case commonv1.SpotStatus_HELD:
		return domain.SpotHeld
	case commonv1.SpotStatus_OCCUPIED:
		return domain.SpotOccupied
	case commonv1.SpotStatus_OUT_OF_SERVICE:
		return domain.SpotOutOfService
	default:
		return ""
	}
}

// spotToProto — convert domain.Spot ke proto Spot.
func spotToProto(s *domain.Spot) *commonv1.Spot {
	if s == nil {
		return nil
	}
	return &commonv1.Spot{
		Id:          s.ID.String(),
		Code:        s.Code,
		FloorLevel:  int32(s.FloorLevel),
		VehicleType: vehicleTypeToProto(s.VehicleType),
		Status:      spotStatusToProto(s.Status),
	}
}

// tsToTime — protobuf Timestamp → Go time.Time. Nil-safe.
func tsToTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

// timeToTs — Go time.Time → proto Timestamp. Zero time returns nil.
func timeToTs(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// timePtrToTs — *time.Time → proto Timestamp. Nil → nil.
func timePtrToTs(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

// parseUUID — convenient wrapper.
func parseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}
