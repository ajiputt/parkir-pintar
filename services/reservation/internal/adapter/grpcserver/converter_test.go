package grpcserver

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/domain"
)

func TestVehicleTypeFromProto(t *testing.T) {
	cases := []struct {
		name    string
		in      commonv1.VehicleType
		want    domain.VehicleType
		wantErr bool
	}{
		{"car", commonv1.VehicleType_CAR, domain.VehicleCar, false},
		{"motor", commonv1.VehicleType_MOTOR, domain.VehicleMotor, false},
		{"unspecified", commonv1.VehicleType_VEHICLE_TYPE_UNSPECIFIED, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := vehicleTypeFromProto(c.in)
			if c.wantErr {
				assert.ErrorIs(t, err, domain.ErrInvalidVehicleType)
			} else {
				require.NoError(t, err)
				assert.Equal(t, c.want, got)
			}
		})
	}
}

func TestVehicleTypeToProto(t *testing.T) {
	cases := []struct {
		name string
		in   domain.VehicleType
		want commonv1.VehicleType
	}{
		{"car", domain.VehicleCar, commonv1.VehicleType_CAR},
		{"motor", domain.VehicleMotor, commonv1.VehicleType_MOTOR},
		{"unknown", domain.VehicleType("PLANE"), commonv1.VehicleType_VEHICLE_TYPE_UNSPECIFIED},
		{"empty", domain.VehicleType(""), commonv1.VehicleType_VEHICLE_TYPE_UNSPECIFIED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, vehicleTypeToProto(c.in))
		})
	}
}

func TestAssignmentModeFromProto(t *testing.T) {
	cases := []struct {
		name string
		in   commonv1.AssignmentMode
		want domain.AssignmentMode
	}{
		{"user", commonv1.AssignmentMode_USER, domain.AssignmentUser},
		{"system", commonv1.AssignmentMode_SYSTEM, domain.AssignmentSystem},
		{"unspecified_defaults_to_system", commonv1.AssignmentMode_ASSIGNMENT_MODE_UNSPECIFIED, domain.AssignmentSystem},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, assignmentModeFromProto(c.in))
		})
	}
}

func TestAssignmentModeToProto(t *testing.T) {
	cases := []struct {
		name string
		in   domain.AssignmentMode
		want commonv1.AssignmentMode
	}{
		{"user", domain.AssignmentUser, commonv1.AssignmentMode_USER},
		{"system", domain.AssignmentSystem, commonv1.AssignmentMode_SYSTEM},
		{"unknown", domain.AssignmentMode("FOO"), commonv1.AssignmentMode_ASSIGNMENT_MODE_UNSPECIFIED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, assignmentModeToProto(c.in))
		})
	}
}

func TestPaymentModeFromProto(t *testing.T) {
	cases := []struct {
		name    string
		in      commonv1.BillingMode
		want    domain.PaymentMode
		wantErr bool
	}{
		{"auto", commonv1.BillingMode_AUTO, domain.PaymentAuto, false},
		{"manual", commonv1.BillingMode_MANUAL, domain.PaymentManual, false},
		{"unspecified", commonv1.BillingMode_BILLING_MODE_UNSPECIFIED, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := paymentModeFromProto(c.in)
			if c.wantErr {
				assert.ErrorIs(t, err, domain.ErrInvalidPaymentMode)
			} else {
				require.NoError(t, err)
				assert.Equal(t, c.want, got)
			}
		})
	}
}

func TestPaymentModeToProto(t *testing.T) {
	cases := []struct {
		name string
		in   domain.PaymentMode
		want commonv1.BillingMode
	}{
		{"auto", domain.PaymentAuto, commonv1.BillingMode_AUTO},
		{"manual", domain.PaymentManual, commonv1.BillingMode_MANUAL},
		{"unknown", domain.PaymentMode("CASH"), commonv1.BillingMode_BILLING_MODE_UNSPECIFIED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, paymentModeToProto(c.in))
		})
	}
}

func TestStateToProto(t *testing.T) {
	cases := []struct {
		name string
		in   domain.State
		want commonv1.ReservationState
	}{
		{"confirmed", domain.StateConfirmed, commonv1.ReservationState_CONFIRMED},
		{"checked_in", domain.StateCheckedIn, commonv1.ReservationState_CHECKED_IN},
		{"checked_out", domain.StateCheckedOut, commonv1.ReservationState_CHECKED_OUT},
		{"cancelled", domain.StateCancelled, commonv1.ReservationState_CANCELLED},
		{"expired", domain.StateExpired, commonv1.ReservationState_EXPIRED},
		{"unknown", domain.State("FOO"), commonv1.ReservationState_RESERVATION_STATE_UNSPECIFIED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, stateToProto(c.in))
		})
	}
}

func TestSpotStatusToProto(t *testing.T) {
	cases := []struct {
		name string
		in   domain.SpotStatus
		want commonv1.SpotStatus
	}{
		{"available", domain.SpotAvailable, commonv1.SpotStatus_AVAILABLE},
		{"held", domain.SpotHeld, commonv1.SpotStatus_HELD},
		{"occupied", domain.SpotOccupied, commonv1.SpotStatus_OCCUPIED},
		{"out_of_service", domain.SpotOutOfService, commonv1.SpotStatus_OUT_OF_SERVICE},
		{"unknown", domain.SpotStatus("X"), commonv1.SpotStatus_SPOT_STATUS_UNSPECIFIED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, spotStatusToProto(c.in))
		})
	}
}

func TestSpotStatusFromProto(t *testing.T) {
	cases := []struct {
		name string
		in   commonv1.SpotStatus
		want domain.SpotStatus
	}{
		{"available", commonv1.SpotStatus_AVAILABLE, domain.SpotAvailable},
		{"held", commonv1.SpotStatus_HELD, domain.SpotHeld},
		{"occupied", commonv1.SpotStatus_OCCUPIED, domain.SpotOccupied},
		{"out_of_service", commonv1.SpotStatus_OUT_OF_SERVICE, domain.SpotOutOfService},
		{"unspecified", commonv1.SpotStatus_SPOT_STATUS_UNSPECIFIED, domain.SpotStatus("")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, spotStatusFromProto(c.in))
		})
	}
}

func TestSpotToProto_Nil(t *testing.T) {
	assert.Nil(t, spotToProto(nil))
}

func TestSpotToProto_Mapped(t *testing.T) {
	id := uuid.New()
	in := &domain.Spot{
		ID:          id,
		Code:        "F2-C-007",
		FloorLevel:  2,
		VehicleType: domain.VehicleCar,
		Status:      domain.SpotAvailable,
	}
	got := spotToProto(in)
	require.NotNil(t, got)
	assert.Equal(t, id.String(), got.Id)
	assert.Equal(t, "F2-C-007", got.Code)
	assert.Equal(t, int32(2), got.FloorLevel)
	assert.Equal(t, commonv1.VehicleType_CAR, got.VehicleType)
	assert.Equal(t, commonv1.SpotStatus_AVAILABLE, got.Status)
}

func TestTsToTime(t *testing.T) {
	assert.True(t, tsToTime(nil).IsZero(), "nil ts -> zero time")

	now := time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC)
	got := tsToTime(timestamppb.New(now))
	assert.True(t, got.Equal(now))
}

func TestTimeToTs(t *testing.T) {
	assert.Nil(t, timeToTs(time.Time{}), "zero time -> nil")

	now := time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC)
	got := timeToTs(now)
	require.NotNil(t, got)
	assert.True(t, got.AsTime().Equal(now))
}

func TestTimePtrToTs(t *testing.T) {
	assert.Nil(t, timePtrToTs(nil), "nil pointer -> nil")

	zero := time.Time{}
	assert.Nil(t, timePtrToTs(&zero), "zero pointed time -> nil")

	now := time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC)
	got := timePtrToTs(&now)
	require.NotNil(t, got)
	assert.True(t, got.AsTime().Equal(now))
}

func TestParseUUID(t *testing.T) {
	id := uuid.New()
	got, err := parseUUID(id.String())
	require.NoError(t, err)
	assert.Equal(t, id, got)

	_, err = parseUUID("not-a-uuid")
	assert.Error(t, err)
}
