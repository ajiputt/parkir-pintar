package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/services/reservation/internal/usecase"
)

func TestGetAvailability_DelegatesToSpotRepo(t *testing.T) {
	spots := newFakeSpotRepo()
	want := &usecase.Availability{
		ParkingAreaID:   uuid.New(),
		ParkingAreaName: "Main",
		AsOf:            time.Now(),
		Floors: []usecase.FloorAvailability{
			{Level: 1, CarAvailable: 10, CarCapacity: 20},
		},
	}
	spots.availability = want

	uc := &usecase.GetAvailability{Spots: spots}
	got, err := uc.Execute(context.Background())
	require.NoError(t, err)
	assert.Same(t, want, got)
}

func TestGetAvailability_PropagatesError(t *testing.T) {
	spots := newFakeSpotRepo()
	boom := errors.New("db down")
	spots.availabilityErr = boom

	uc := &usecase.GetAvailability{Spots: spots}
	_, err := uc.Execute(context.Background())
	assert.ErrorIs(t, err, boom)
}
