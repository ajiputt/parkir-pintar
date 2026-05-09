package domain

import "time"

// Contact — embedded user contact data (lihat ADR-0013).
//
// DriverID adalah PK natural key (string opaque dari other services).
// Kalau scope grows ke real user/auth domain, extract ke user service —
// notification akan call gRPC GetUser instead of local lookup.
type Contact struct {
	DriverID  string
	Email     string
	Name      string
	Phone     string // optional, future channel
	OptIn     bool   // global opt-in, default true
	CreatedAt time.Time
	UpdatedAt time.Time
}
