// Package pricing — pricing engine ParkirPintar.
//
// Aturan (sesuai use case):
//
//   - Booking fee: 5_000 IDR per reservation terkonfirmasi.
//   - Hourly: 5_000 IDR per started hour (jam pertama dan seterusnya).
//   - Overnight flat: 20_000 IDR jika sesi melewati tengah malam (00:00 Asia/Jakarta).
//     Catatan: jika overnight, hourly tidak di-charge UNTUK jam-jam yang masuk window
//     overnight. Jam normal di hari berikutnya tetap dihitung hourly.
//   - No-show penalty: 5_000 IDR (booking fee + penalty = 10_000 IDR total).
//   - Overstay: tidak ada penalty, tetap dihitung hourly.
//
// Implementasi: pure functions, deterministic, fully testable.
package pricing

import (
	"errors"
	"time"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
)

// Default rate values (sesuai use case). Bisa di-override via Config untuk testing
// atau perubahan business rule di masa depan.
var (
	DefaultBookingFee     = money.IDR(5_000)
	DefaultHourlyRate     = money.IDR(5_000)
	DefaultOvernightFlat  = money.IDR(20_000)
	DefaultNoShowPenalty  = money.IDR(5_000)
	DefaultTimeZone       = "Asia/Jakarta"
	DefaultOvernightStart = 22 // jam mulai overnight (22:00)
	DefaultOvernightEndHr = 6  // jam selesai overnight (06:00)
)

// Config — knobs untuk pricing engine.
type Config struct {
	BookingFee     money.Money
	HourlyRate     money.Money
	OvernightFlat  money.Money
	NoShowPenalty  money.Money
	TimeZone       string
	OvernightStart int // hour 0-23
	OvernightEnd   int // hour 0-23
}

// DefaultConfig sesuai aturan use case.
func DefaultConfig() Config {
	return Config{
		BookingFee:     DefaultBookingFee,
		HourlyRate:     DefaultHourlyRate,
		OvernightFlat:  DefaultOvernightFlat,
		NoShowPenalty:  DefaultNoShowPenalty,
		TimeZone:       DefaultTimeZone,
		OvernightStart: DefaultOvernightStart,
		OvernightEnd:   DefaultOvernightEndHr,
	}
}

// LineKind — type of line item yang di-generate engine.
type LineKind string

const (
	LineBookingFee    LineKind = "BOOKING_FEE"
	LineHourly        LineKind = "HOURLY"
	LineOvernight     LineKind = "OVERNIGHT"
	LineNoShowPenalty LineKind = "NO_SHOW_PENALTY"
)

// Line — satu komponen invoice.
type Line struct {
	Kind        LineKind
	Description string
	Amount      money.Money
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// Engine — pricing engine; immutable setelah konstruksi.
type Engine struct {
	cfg Config
	loc *time.Location
}

// ErrInvalidConfig dikembalikan kalau config tidak valid.
var ErrInvalidConfig = errors.New("pricing: invalid config")

// New membuat Engine baru. Validate config dan parse timezone.
func New(cfg Config) (*Engine, error) {
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return nil, err
	}
	if cfg.OvernightStart < 0 || cfg.OvernightStart > 23 ||
		cfg.OvernightEnd < 0 || cfg.OvernightEnd > 23 {
		return nil, ErrInvalidConfig
	}
	return &Engine{cfg: cfg, loc: loc}, nil
}

// MustNew — panic on error. Pakai untuk init di main.
func MustNew(cfg Config) *Engine {
	e, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return e
}

// Default — singleton dengan default config.
func Default() *Engine {
	return MustNew(DefaultConfig())
}

// BookingLine — booking fee per reservation. Dipanggil saat ReservationConfirmed.
func (e *Engine) BookingLine() Line {
	return Line{
		Kind:        LineBookingFee,
		Description: "Booking fee",
		Amount:      e.cfg.BookingFee,
	}
}

// NoShowLine — penalty kalau driver tidak check-in dalam hold time.
func (e *Engine) NoShowLine() Line {
	return Line{
		Kind:        LineNoShowPenalty,
		Description: "No-show penalty (reservation expired)",
		Amount:      e.cfg.NoShowPenalty,
	}
}

// CalculateSession — generate line items untuk sesi parkir aktual
// (checkin..checkout). Tidak include booking fee — itu sudah di-issue terpisah.
//
// Aturan:
//   - Hitung "started hours": ceil(duration / 1h).
//   - Jika sesi melewati tengah malam (00:00 di TZ), apply overnight flat
//     untuk segmen overnight, dan hourly untuk segmen non-overnight.
//   - Overnight window: [OvernightStart, 24:00) ∪ [00:00, OvernightEnd).
func (e *Engine) CalculateSession(checkIn, checkOut time.Time) ([]Line, error) {
	if !checkOut.After(checkIn) {
		return nil, errors.New("pricing: checkout must be after checkin")
	}
	checkIn = checkIn.In(e.loc)
	checkOut = checkOut.In(e.loc)

	overnightHours, normalHours := e.splitOvernight(checkIn, checkOut)

	lines := make([]Line, 0, 2)
	if overnightHours > 0 {
		lines = append(lines, Line{
			Kind:        LineOvernight,
			Description: "Overnight flat fee",
			Amount:      e.cfg.OvernightFlat,
			PeriodStart: checkIn,
			PeriodEnd:   checkOut,
		})
	}
	if normalHours > 0 {
		lines = append(lines, Line{
			Kind:        LineHourly,
			Description: "Hourly rate",
			Amount:      e.cfg.HourlyRate.Mul(int64(normalHours)),
			PeriodStart: checkIn,
			PeriodEnd:   checkOut,
		})
	}
	return lines, nil
}

// splitOvernight — return (overnight_hours_used_for_flat, normal_hours_for_hourly).
//
// Logika:
//   - Jika sesi punya overlap dengan window overnight, pakai overnight flat (1×).
//     Jam yang ada di window overnight TIDAK di-charge hourly.
//   - Jam di luar window overnight di-charge hourly (started hours = ceil).
func (e *Engine) splitOvernight(checkIn, checkOut time.Time) (overnightHours, normalHours int) {
	// total started hours dari sesi
	totalDur := checkOut.Sub(checkIn)
	totalStarted := int(totalDur / time.Hour)
	if totalDur%time.Hour > 0 {
		totalStarted++
	}

	// hitung berapa "jam sesi" yang berada dalam window overnight
	overnightStarted := e.overnightStartedHours(checkIn, checkOut)

	if overnightStarted == 0 {
		return 0, totalStarted
	}

	// Sisanya: normal hours = total - overnight (clamp >=0)
	normal := totalStarted - overnightStarted
	if normal < 0 {
		normal = 0
	}
	return overnightStarted, normal
}

// overnightStartedHours — jumlah "jam sesi" yang menyentuh window overnight.
// Kita iterasi per jam (cukup cepat untuk durasi normal < 48 jam).
func (e *Engine) overnightStartedHours(checkIn, checkOut time.Time) int {
	count := 0
	// "started hour" pertama dimulai dari checkIn → checkIn+1h
	h := checkIn
	for h.Before(checkOut) {
		if e.isOvernightHour(h) {
			count++
		}
		h = h.Add(time.Hour)
	}
	return count
}

// isOvernightHour — apakah jam mulai t (di TZ engine) berada dalam overnight window.
// Window: [OvernightStart, 24:00) ∪ [00:00, OvernightEnd).
func (e *Engine) isOvernightHour(t time.Time) bool {
	h := t.In(e.loc).Hour()
	if e.cfg.OvernightStart <= e.cfg.OvernightEnd {
		// non-wrapping window (rare)
		return h >= e.cfg.OvernightStart && h < e.cfg.OvernightEnd
	}
	// wrap-around (default 22..06)
	return h >= e.cfg.OvernightStart || h < e.cfg.OvernightEnd
}

// Total — sum semua line.
func Total(lines []Line) (money.Money, error) {
	amounts := make([]money.Money, 0, len(lines))
	for _, l := range lines {
		amounts = append(amounts, l.Amount)
	}
	return money.Sum(amounts)
}
