package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRes(t *testing.T) *Reservation {
	t.Helper()
	now := time.Now()
	r, err := New("driver1", "B 1234 ABC", VehicleCar, AssignmentSystem, PaymentAuto,
		uuid.New(), now, now.Add(time.Hour), "key")
	require.NoError(t, err)
	return r
}

func TestNew_ValidatesInputs(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		driverID  string
		plate     string
		vt        VehicleType
		expectErr bool
	}{
		{"empty driver", "", "B 1", VehicleCar, true},
		{"empty plate", "d1", "", VehicleCar, true},
		{"invalid vehicle type", "d1", "B 1", "PLANE", true},
		{"valid", "d1", "B 1", VehicleCar, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.driverID, c.plate, c.vt, AssignmentSystem, PaymentAuto,
				uuid.New(), now, now.Add(time.Hour), "k")
			if c.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStateTransitions_HappyPath(t *testing.T) {
	r := newRes(t)
	now := time.Now()

	require.NoError(t, r.CheckIn(now))
	assert.Equal(t, StateCheckedIn, r.State)
	assert.NotNil(t, r.CheckInAt)

	require.NoError(t, r.CheckOut(now.Add(2*time.Hour)))
	assert.Equal(t, StateCheckedOut, r.State)
	assert.NotNil(t, r.CheckOutAt)
}

func TestCheckIn_AfterExpiry_Rejects(t *testing.T) {
	r := newRes(t)
	err := r.CheckIn(r.ExpiresAt.Add(time.Minute))
	assert.ErrorIs(t, err, ErrHoldExpired)
	assert.Equal(t, StateConfirmed, r.State, "state must not change on failed transition")
}

func TestCancel_OnlyFromConfirmed(t *testing.T) {
	r := newRes(t)
	require.NoError(t, r.Cancel(time.Now()))
	assert.Equal(t, StateCancelled, r.State)

	// double cancel rejected
	err := r.Cancel(time.Now())
	assert.ErrorIs(t, err, ErrInvalidStateTransition)
}

func TestCheckOut_RequiresCheckedIn(t *testing.T) {
	r := newRes(t)
	err := r.CheckOut(time.Now())
	assert.ErrorIs(t, err, ErrInvalidStateTransition)
}

func TestExpire_OnlyFromConfirmed(t *testing.T) {
	r := newRes(t)
	require.NoError(t, r.CheckIn(time.Now()))
	err := r.Expire(time.Now())
	assert.ErrorIs(t, err, ErrInvalidStateTransition)
}

func TestIsHeld(t *testing.T) {
	r := newRes(t)
	assert.True(t, r.IsHeld())
	require.NoError(t, r.CheckIn(time.Now()))
	assert.True(t, r.IsHeld())
	require.NoError(t, r.CheckOut(time.Now()))
	assert.False(t, r.IsHeld())
}
