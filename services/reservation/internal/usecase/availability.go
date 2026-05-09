package usecase

import "context"

// GetAvailability — read-side: ambil snapshot per floor.
//
// Single-area assumption — adapter return default area.
type GetAvailability struct {
	Spots SpotRepo
}

func (uc *GetAvailability) Execute(ctx context.Context) (*Availability, error) {
	return uc.Spots.GetAvailability(ctx)
}
