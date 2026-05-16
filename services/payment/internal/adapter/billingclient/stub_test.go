package billingclient

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestStub_Zero(t *testing.T) {
	t.Parallel()
	s := &Stub{}
	driverID, amount, err := s.Get(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("zero-value Stub.Get error = %v, want nil", err)
	}
	if driverID != "" {
		t.Errorf("driverID = %q, want \"\"", driverID)
	}
	if amount != 0 {
		t.Errorf("amount = %d, want 0", amount)
	}
}

func TestNewStub_ReturnsFixedValues(t *testing.T) {
	t.Parallel()
	s := NewStub("drv-1", 12345)
	if s == nil {
		t.Fatalf("NewStub returned nil")
	}
	if s.FixedDriverID != "drv-1" {
		t.Errorf("FixedDriverID = %q, want drv-1", s.FixedDriverID)
	}
	if s.FixedAmount != 12345 {
		t.Errorf("FixedAmount = %d, want 12345", s.FixedAmount)
	}

	driverID, amount, err := s.Get(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Get error = %v, want nil", err)
	}
	if driverID != "drv-1" {
		t.Errorf("returned driverID = %q, want drv-1", driverID)
	}
	if amount != 12345 {
		t.Errorf("returned amount = %d, want 12345", amount)
	}
}

func TestStub_Get_FixedErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	s := &Stub{FixedDriverID: "drv", FixedAmount: 100, FixedError: want}
	driverID, amount, err := s.Get(context.Background(), uuid.New())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if driverID != "" {
		t.Errorf("driverID on error = %q, want \"\"", driverID)
	}
	if amount != 0 {
		t.Errorf("amount on error = %d, want 0", amount)
	}
}

func TestStub_Get_IgnoresContextAndInvoiceID(t *testing.T) {
	t.Parallel()
	// Stub should not care about cancellation or specific ID.
	s := NewStub("drv-x", 500)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled — stub still returns the fixed values
	driverID, amount, err := s.Get(ctx, uuid.Nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if driverID != "drv-x" || amount != 500 {
		t.Errorf("got (%q,%d), want (drv-x,500)", driverID, amount)
	}
}
