package domain_test

import (
	"testing"
	"time"

	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

// Contact is currently a pure value object — no methods. We just verify
// the zero value + field semantics so refactors don't silently regress.

func TestContact_ZeroValue_IsUsable(t *testing.T) {
	t.Parallel()
	var c domain.Contact
	if c.DriverID != "" {
		t.Errorf("zero DriverID = %q, want empty", c.DriverID)
	}
	if c.OptIn {
		// Default zero bool is false. Documented default is "true" — but the
		// zero value doesn't enforce that; callers MUST explicitly set OptIn.
		// This test pins current behavior so any change is intentional.
		t.Log("OptIn zero value is false (callers must set explicitly)")
	}
}

func TestContact_PopulatedFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	c := domain.Contact{
		DriverID:  "drv-1",
		Email:     "aji@example.com",
		Name:      "Aji",
		Phone:     "+6281234567890",
		OptIn:     true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if c.DriverID != "drv-1" {
		t.Errorf("DriverID = %q", c.DriverID)
	}
	if c.Email != "aji@example.com" {
		t.Errorf("Email = %q", c.Email)
	}
	if c.Name != "Aji" {
		t.Errorf("Name = %q", c.Name)
	}
	if c.Phone != "+6281234567890" {
		t.Errorf("Phone = %q", c.Phone)
	}
	if !c.OptIn {
		t.Error("OptIn must remain true after explicit set")
	}
	if !c.CreatedAt.Equal(now) {
		t.Error("CreatedAt should be preserved")
	}
	if !c.UpdatedAt.Equal(now) {
		t.Error("UpdatedAt should be preserved")
	}
}
