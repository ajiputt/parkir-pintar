package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestSpotStatus_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, SpotStatus("AVAILABLE"), SpotAvailable)
	assert.Equal(t, SpotStatus("HELD"), SpotHeld)
	assert.Equal(t, SpotStatus("OCCUPIED"), SpotOccupied)
	assert.Equal(t, SpotStatus("OUT_OF_SERVICE"), SpotOutOfService)
}

func TestSpot_IsAvailableFor_HappyPath(t *testing.T) {
	t.Parallel()
	s := Spot{
		ID:          uuid.New(),
		FloorID:     uuid.New(),
		FloorLevel:  2,
		Code:        "F2-C-007",
		VehicleType: VehicleCar,
		Status:      SpotAvailable,
	}
	assert.True(t, s.IsAvailableFor(VehicleCar))
}

func TestSpot_IsAvailableFor_WrongVehicleType(t *testing.T) {
	t.Parallel()
	s := Spot{
		VehicleType: VehicleCar,
		Status:      SpotAvailable,
	}
	assert.False(t, s.IsAvailableFor(VehicleMotor))
}

func TestSpot_IsAvailableFor_NotAvailableStatus(t *testing.T) {
	t.Parallel()
	for _, st := range []SpotStatus{SpotHeld, SpotOccupied, SpotOutOfService, ""} {
		s := Spot{VehicleType: VehicleCar, Status: st}
		assert.False(t, s.IsAvailableFor(VehicleCar), "status=%q should be unavailable", st)
	}
}

func TestSpot_StructFields(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	fid := uuid.New()
	s := Spot{
		ID:          id,
		FloorID:     fid,
		FloorLevel:  3,
		Code:        "F3-A-001",
		VehicleType: VehicleMotor,
		Status:      SpotHeld,
		Version:     7,
	}
	assert.Equal(t, id, s.ID)
	assert.Equal(t, fid, s.FloorID)
	assert.Equal(t, 3, s.FloorLevel)
	assert.Equal(t, "F3-A-001", s.Code)
	assert.Equal(t, VehicleMotor, s.VehicleType)
	assert.Equal(t, SpotHeld, s.Status)
	assert.Equal(t, 7, s.Version)
}
