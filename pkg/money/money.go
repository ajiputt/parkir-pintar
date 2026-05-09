// Package money menyediakan value object Rupiah yang aman dari floating point.
// Semua perhitungan billing/pricing wajib lewat tipe ini.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
)

// CurrencyIDR — kode mata uang Rupiah Indonesia.
const CurrencyIDR = "IDR"

// Money adalah jumlah uang dalam unit terkecil (1 IDR — Indonesia tidak punya subdivisi).
// Selalu integer untuk menghindari error pembulatan floating point.
type Money struct {
	amount   int64
	currency string
}

var (
	// ErrCurrencyMismatch dikembalikan ketika operasi aritmatika dilakukan
	// pada dua nilai dengan currency berbeda.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	// ErrNegative dikembalikan ketika hasil operasi menjadi negatif.
	// Pricing engine kita tidak mengizinkan nilai negatif.
	ErrNegative = errors.New("money: negative amount not allowed")
)

// IDR membuat Money dengan currency IDR.
func IDR(amount int64) Money {
	return Money{amount: amount, currency: CurrencyIDR}
}

// New membuat Money dengan currency arbitrary (untuk kebutuhan future).
func New(amount int64, currency string) Money {
	return Money{amount: amount, currency: currency}
}

// Zero IDR.
func Zero() Money {
	return IDR(0)
}

// Amount mengembalikan jumlah dalam unit terkecil.
func (m Money) Amount() int64 { return m.amount }

// Currency mengembalikan kode mata uang.
func (m Money) Currency() string { return m.currency }

// IsZero true kalau amount = 0.
func (m Money) IsZero() bool { return m.amount == 0 }

// Add menambahkan dua Money. Currency harus sama.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

// MustAdd panics jika error — pakai hanya saat currency dijamin sama.
func (m Money) MustAdd(other Money) Money {
	r, err := m.Add(other)
	if err != nil {
		panic(err)
	}
	return r
}

// Sub mengurangkan; tidak boleh hasil negatif.
func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, other.currency)
	}
	if m.amount < other.amount {
		return Money{}, ErrNegative
	}
	return Money{amount: m.amount - other.amount, currency: m.currency}, nil
}

// Mul kalikan dengan scalar integer.
func (m Money) Mul(factor int64) Money {
	return Money{amount: m.amount * factor, currency: m.currency}
}

// Equals membandingkan dua Money.
func (m Money) Equals(other Money) bool {
	return m.currency == other.currency && m.amount == other.amount
}

// String — pretty print "Rp5.000".
func (m Money) String() string {
	if m.currency == CurrencyIDR {
		return fmt.Sprintf("Rp%s", formatThousands(m.amount))
	}
	return fmt.Sprintf("%d %s", m.amount, m.currency)
}

func formatThousands(n int64) string {
	if n < 0 {
		return "-" + formatThousands(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return formatThousands(n/1000) + "." + fmt.Sprintf("%03d", n%1000)
}

// MarshalJSON — serialize as { "amount": int64, "currency": "IDR" }.
// Tanpa ini, money.Money akan serialize jadi `{}` karena field-nya unexported.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}{Amount: m.amount, Currency: m.currency})
}

// UnmarshalJSON — pasangan dari MarshalJSON. Toleran: kalau format lama
// (raw int) datang dari source eksternal, fallback ke IDR.
func (m *Money) UnmarshalJSON(data []byte) error {
	// Coba format object dulu.
	var aux struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(data, &aux); err == nil && aux.Currency != "" {
		m.amount = aux.Amount
		m.currency = aux.Currency
		return nil
	}
	// Fallback: bare integer → asumsikan IDR.
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	m.amount = n
	m.currency = CurrencyIDR
	return nil
}

// Sum menjumlah slice Money. Currency harus konsisten.
func Sum(items []Money) (Money, error) {
	if len(items) == 0 {
		return Zero(), nil
	}
	total := items[0]
	for _, m := range items[1:] {
		var err error
		total, err = total.Add(m)
		if err != nil {
			return Money{}, err
		}
	}
	return total, nil
}
