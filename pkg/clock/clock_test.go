package clock_test

import (
	"testing"
	"time"

	"github.com/ajiperdana/parkir-pintar/pkg/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ReturnsRealClock(t *testing.T) {
	c := clock.New()
	require.NotNil(t, c)
	// Now() must return a time close to time.Now().
	got := c.Now()
	assert.WithinDuration(t, time.Now(), got, time.Second)
}

func TestReal_Now(t *testing.T) {
	r := clock.Real{}
	before := time.Now()
	got := r.Now()
	after := time.Now()
	// got should be in [before, after].
	assert.False(t, got.Before(before))
	assert.False(t, got.After(after))
}

func TestFake_Now_ReturnsInitial(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	f := clock.NewFake(fixed)
	require.NotNil(t, f)
	assert.Equal(t, fixed, f.Now())
	// Multiple calls return the same time (deterministic).
	assert.Equal(t, fixed, f.Now())
}

func TestFake_Set(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	f := clock.NewFake(fixed)

	newTime := time.Date(2030, 6, 7, 8, 9, 10, 0, time.UTC)
	f.Set(newTime)
	assert.Equal(t, newTime, f.Now())
}

func TestFake_Advance(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	f := clock.NewFake(fixed)

	f.Advance(2 * time.Hour)
	assert.Equal(t, fixed.Add(2*time.Hour), f.Now())

	f.Advance(30 * time.Minute)
	assert.Equal(t, fixed.Add(2*time.Hour+30*time.Minute), f.Now())
}

func TestFake_Advance_Negative(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	f := clock.NewFake(fixed)

	f.Advance(-1 * time.Hour)
	assert.Equal(t, fixed.Add(-1*time.Hour), f.Now())
}

// TestFake_ImplementsClockInterface verifies *Fake satisfies the Clock interface.
func TestFake_ImplementsClockInterface(t *testing.T) {
	var c clock.Clock = clock.NewFake(time.Now())
	_ = c.Now()
}

func TestReal_ImplementsClockInterface(t *testing.T) {
	var c clock.Clock = clock.Real{}
	_ = c.Now()
}
