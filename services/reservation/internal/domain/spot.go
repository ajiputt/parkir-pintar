package domain

import "github.com/google/uuid"

// SpotStatus.
type SpotStatus string

const (
	SpotAvailable    SpotStatus = "AVAILABLE"
	SpotHeld         SpotStatus = "HELD"
	SpotOccupied     SpotStatus = "OCCUPIED"
	SpotOutOfService SpotStatus = "OUT_OF_SERVICE"
)

// Spot — entitas inventory.
type Spot struct {
	ID          uuid.UUID
	FloorID     uuid.UUID
	FloorLevel  int
	Code        string // "F2-C-007"
	VehicleType VehicleType
	Status      SpotStatus
	Version     int
}

// IsAvailableFor — bisa di-assign untuk vehicle type tertentu.
func (s Spot) IsAvailableFor(vt VehicleType) bool {
	return s.Status == SpotAvailable && s.VehicleType == vt
}
