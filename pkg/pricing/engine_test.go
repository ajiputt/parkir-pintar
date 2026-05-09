package pricing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(DefaultConfig())
	require.NoError(t, err)
	return e
}

func TestBookingLine(t *testing.T) {
	e := newEngine(t)
	l := e.BookingLine()
	assert.Equal(t, LineBookingFee, l.Kind)
	assert.True(t, l.Amount.Equals(money.IDR(5_000)))
}

func TestNoShowLine(t *testing.T) {
	e := newEngine(t)
	l := e.NoShowLine()
	assert.Equal(t, LineNoShowPenalty, l.Kind)
	assert.True(t, l.Amount.Equals(money.IDR(5_000)))
}

// CalculateSession — table-driven: cover semua skenario use case.
func TestCalculateSession(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	at := func(y int, mo time.Month, d, h, m int) time.Time {
		return time.Date(y, mo, d, h, m, 0, 0, jakarta)
	}

	cases := []struct {
		name                 string
		in, out              time.Time
		wantHourly           int64 // amount IDR
		wantOvernightCharged bool
	}{
		{
			name:       "30 menit di siang → 1 started hour (5K)",
			in:         at(2026, 5, 1, 10, 0),
			out:        at(2026, 5, 1, 10, 30),
			wantHourly: 5_000,
		},
		{
			name:       "1 jam tepat di siang → 1 started hour (5K)",
			in:         at(2026, 5, 1, 10, 0),
			out:        at(2026, 5, 1, 11, 0),
			wantHourly: 5_000,
		},
		{
			name:       "1 jam 1 menit di siang → 2 started hour (10K)",
			in:         at(2026, 5, 1, 10, 0),
			out:        at(2026, 5, 1, 11, 1),
			wantHourly: 10_000,
		},
		{
			name:       "8 jam di siang → 8 jam (40K)",
			in:         at(2026, 5, 1, 8, 0),
			out:        at(2026, 5, 1, 16, 0),
			wantHourly: 40_000,
		},
		{
			name:                 "23:00 - 03:00 → overnight 4 jam masuk window (flat 20K), no hourly",
			in:                   at(2026, 5, 1, 23, 0),
			out:                  at(2026, 5, 2, 3, 0),
			wantHourly:           0,
			wantOvernightCharged: true,
		},
		{
			name:                 "23:00 - 09:00 → overnight 7 jam (22-06) flat + 3 jam normal (06-09)",
			in:                   at(2026, 5, 1, 23, 0),
			out:                  at(2026, 5, 2, 9, 0),
			wantHourly:           15_000,
			wantOvernightCharged: true,
		},
		{
			name:                 "20:00 - 22:30 → 2 jam normal + 1 jam overnight (22:00-23:00). Started hours total = 3, overnight=1, hourly=2 → 10K + flat",
			in:                   at(2026, 5, 1, 20, 0),
			out:                  at(2026, 5, 1, 22, 30),
			wantHourly:           10_000,
			wantOvernightCharged: true,
		},
		{
			name:       "Pas batas overnight 06:00 - 09:00 → 3 jam normal, no overnight",
			in:         at(2026, 5, 2, 6, 0),
			out:        at(2026, 5, 2, 9, 0),
			wantHourly: 15_000,
		},
	}

	e := newEngine(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines, err := e.CalculateSession(c.in, c.out)
			require.NoError(t, err)

			var hourly int64
			overnightSeen := false
			for _, l := range lines {
				switch l.Kind {
				case LineHourly:
					hourly = l.Amount.Amount()
				case LineOvernight:
					overnightSeen = true
					assert.Equal(t, int64(20_000), l.Amount.Amount())
				}
			}
			assert.Equal(t, c.wantHourly, hourly, "hourly amount")
			assert.Equal(t, c.wantOvernightCharged, overnightSeen, "overnight charge")
		})
	}
}

func TestCalculateSession_InvalidWindow(t *testing.T) {
	e := newEngine(t)
	// Snapshot time once. `time.Now()` consecutive di high-resolution clock
	// (Linux GHA runner) bisa return slightly-later second value, bikin
	// checkOut.After(checkIn) jadi true secara accidental.
	n := time.Now()
	_, err := e.CalculateSession(n, n)
	require.Error(t, err, "checkout == checkin should error")
	_, err = e.CalculateSession(n, n.Add(-1*time.Hour))
	require.Error(t, err, "checkout < checkin should error")
}

// Skenario integrasi: full invoice = booking + session.
func TestFullInvoice_HappyPath(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	in := time.Date(2026, 5, 1, 10, 0, 0, 0, jakarta)
	out := time.Date(2026, 5, 1, 12, 30, 0, 0, jakarta) // 3 started hours

	e := newEngine(t)
	lines := []Line{e.BookingLine()}
	sess, err := e.CalculateSession(in, out)
	require.NoError(t, err)
	lines = append(lines, sess...)

	total, err := Total(lines)
	require.NoError(t, err)
	// 5K booking + 15K hourly = 20K
	assert.Equal(t, int64(20_000), total.Amount())
}

func TestFullInvoice_NoShow(t *testing.T) {
	e := newEngine(t)
	lines := []Line{e.BookingLine(), e.NoShowLine()}
	total, err := Total(lines)
	require.NoError(t, err)
	// 5K booking + 5K penalty = 10K
	assert.Equal(t, int64(10_000), total.Amount())
}

func TestFullInvoice_OvernightWithDayHours(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	in := time.Date(2026, 5, 1, 21, 0, 0, 0, jakarta)
	out := time.Date(2026, 5, 2, 10, 0, 0, 0, jakarta)
	// jam-by-jam: 21 (normal), 22-05 (overnight 8 jam), 06-09 (normal 4 jam)
	// total started hours = 13
	// overnight started = 8 → hourly normal = 5

	e := newEngine(t)
	lines := []Line{e.BookingLine()}
	sess, err := e.CalculateSession(in, out)
	require.NoError(t, err)
	lines = append(lines, sess...)

	total, err := Total(lines)
	require.NoError(t, err)
	// 5K booking + 20K overnight + 5×5K hourly = 50K
	assert.Equal(t, int64(50_000), total.Amount())
}
