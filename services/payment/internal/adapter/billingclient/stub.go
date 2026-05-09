package billingclient

import (
	"context"

	"github.com/google/uuid"
)

// Stub — untuk demo/test tanpa billing service running.
// Set FixedAmount untuk control return value.
type Stub struct {
	FixedDriverID string
	FixedAmount   int64
	FixedError    error
}

// NewStub — convenience.
func NewStub(driverID string, amount int64) *Stub {
	return &Stub{FixedDriverID: driverID, FixedAmount: amount}
}

func (s *Stub) Get(_ context.Context, _ uuid.UUID) (string, int64, error) {
	if s.FixedError != nil {
		return "", 0, s.FixedError
	}
	return s.FixedDriverID, s.FixedAmount, nil
}
