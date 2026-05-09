// Package clock menyediakan abstraksi waktu untuk testability.
// Domain & usecase wajib pakai Clock interface, bukan time.Now() langsung.
package clock

import "time"

// Clock interface untuk dependency injection waktu.
type Clock interface {
	Now() time.Time
}

// Real mengembalikan time.Now() asli — pakai di production main.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// New membuat Real Clock.
func New() Clock { return Real{} }

// Fake — controllable clock untuk test.
type Fake struct {
	t time.Time
}

// NewFake membuat Fake Clock dimulai dari t.
func NewFake(t time.Time) *Fake {
	return &Fake{t: t}
}

func (f *Fake) Now() time.Time { return f.t }

// Set mengatur waktu absolut.
func (f *Fake) Set(t time.Time) { f.t = t }

// Advance majukan waktu.
func (f *Fake) Advance(d time.Duration) { f.t = f.t.Add(d) }
