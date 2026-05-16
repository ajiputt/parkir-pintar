package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/pkg/pricing"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
)

// ----- helpers -----

func bookingLine() pricing.Line {
	return pricing.Line{
		Kind:        pricing.LineBookingFee,
		Description: "Booking fee",
		Amount:      money.IDR(5_000),
	}
}

func hourlyLine(amount int64) pricing.Line {
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	return pricing.Line{
		Kind:        pricing.LineHourly,
		Description: "Hourly",
		Amount:      money.IDR(amount),
		PeriodStart: now,
		PeriodEnd:   now.Add(time.Hour),
	}
}

// ----- New / NewWithMode -----

func TestNew_DefaultsToAutoPayMode(t *testing.T) {
	t.Parallel()
	resID := uuid.New()

	inv := domain.New(resID, "drv-1")

	if inv == nil {
		t.Fatal("New returned nil")
	}
	if inv.PaymentMode != domain.PaymentAuto {
		t.Errorf("PaymentMode = %q, want AUTO", inv.PaymentMode)
	}
	if inv.Status != domain.InvoiceDraft {
		t.Errorf("Status = %q, want DRAFT", inv.Status)
	}
	if inv.ReservationID != resID {
		t.Error("ReservationID not set correctly")
	}
	if inv.DriverID != "drv-1" {
		t.Errorf("DriverID = %q", inv.DriverID)
	}
	if inv.ID == uuid.Nil {
		t.Error("ID must be generated")
	}
	if !inv.Total.IsZero() {
		t.Error("Total must start at zero")
	}
	if inv.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}
	if !inv.CreatedAt.Equal(inv.UpdatedAt) {
		t.Error("UpdatedAt should equal CreatedAt for fresh invoice")
	}
}

func TestNewWithMode_EmptyModeFallsBackToAuto(t *testing.T) {
	t.Parallel()
	inv := domain.NewWithMode(uuid.New(), "drv-1", "")
	if inv.PaymentMode != domain.PaymentAuto {
		t.Errorf("empty payment mode must default to AUTO, got %q", inv.PaymentMode)
	}
}

func TestNewWithMode_ManualPreserved(t *testing.T) {
	t.Parallel()
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentManual)
	if inv.PaymentMode != domain.PaymentManual {
		t.Errorf("PaymentMode = %q, want MANUAL", inv.PaymentMode)
	}
}

// ----- AddLine -----

func TestAddLine_AppendsItemAndUpdatesTotal(t *testing.T) {
	t.Parallel()
	inv := domain.New(uuid.New(), "drv-1")

	if err := inv.AddLine(bookingLine()); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if err := inv.AddLine(hourlyLine(10_000)); err != nil {
		t.Fatalf("AddLine: %v", err)
	}

	if len(inv.Items) != 2 {
		t.Fatalf("Items len = %d, want 2", len(inv.Items))
	}
	if inv.Total.Amount() != 15_000 {
		t.Errorf("Total = %d, want 15000", inv.Total.Amount())
	}
	// PeriodStart/End on hourly line should be copied to item.
	hourly := inv.Items[1]
	if hourly.PeriodStart == nil || hourly.PeriodEnd == nil {
		t.Error("hourly item must carry period bounds")
	}
}

func TestAddLine_BookingFeeWithoutPeriod_NoPeriodOnItem(t *testing.T) {
	t.Parallel()
	inv := domain.New(uuid.New(), "drv-1")
	if err := inv.AddLine(bookingLine()); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	item := inv.Items[0]
	if item.PeriodStart != nil || item.PeriodEnd != nil {
		t.Error("booking fee item should have nil period")
	}
}

// ----- Issue -----

func TestIssue_SetsStatusAndIssuedAt(t *testing.T) {
	t.Parallel()
	inv := domain.New(uuid.New(), "drv-1")
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	inv.Issue(now)

	if inv.Status != domain.InvoiceIssued {
		t.Errorf("Status = %q, want ISSUED", inv.Status)
	}
	if inv.IssuedAt == nil || !inv.IssuedAt.Equal(now) {
		t.Errorf("IssuedAt should equal %v", now)
	}
	if !inv.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt should equal %v", now)
	}
}

// ----- MarkPaid -----

func TestMarkPaid_FromIssued_Settles(t *testing.T) {
	t.Parallel()
	inv := domain.New(uuid.New(), "drv-1")
	inv.Issue(time.Now().UTC())
	now := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)

	if err := inv.MarkPaid(now); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}
	if inv.Status != domain.InvoicePaid {
		t.Errorf("Status = %q, want PAID", inv.Status)
	}
	if inv.PaidAt == nil || !inv.PaidAt.Equal(now) {
		t.Errorf("PaidAt should equal %v", now)
	}
}

func TestMarkPaid_FromOverdue_Settles(t *testing.T) {
	t.Parallel()
	// Overdue invoice can still be paid (driver pays after overdue → unblock).
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentManual)
	issuedAt := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	inv.Issue(issuedAt)
	if err := inv.MarkOverdue(issuedAt.Add(time.Hour)); err != nil {
		t.Fatalf("MarkOverdue: %v", err)
	}

	paidAt := issuedAt.Add(2 * time.Hour)
	if err := inv.MarkPaid(paidAt); err != nil {
		t.Fatalf("MarkPaid from OVERDUE: %v", err)
	}
	if inv.Status != domain.InvoicePaid {
		t.Errorf("Status = %q, want PAID", inv.Status)
	}
}

func TestMarkPaid_AlreadyPaid_Idempotent(t *testing.T) {
	t.Parallel()
	inv := domain.New(uuid.New(), "drv-1")
	inv.Issue(time.Now().UTC())
	first := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
	if err := inv.MarkPaid(first); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}

	// Second call should be idempotent — no error and no state mutation.
	second := first.Add(time.Hour)
	if err := inv.MarkPaid(second); err != nil {
		t.Fatalf("idempotent MarkPaid should not error: %v", err)
	}
	if !inv.PaidAt.Equal(first) {
		t.Errorf("idempotent call must not move PaidAt: got %v, want %v", inv.PaidAt, first)
	}
}

func TestMarkPaid_FromDraft_Rejected(t *testing.T) {
	t.Parallel()
	inv := domain.New(uuid.New(), "drv-1")
	err := inv.MarkPaid(time.Now().UTC())
	if !errors.Is(err, domain.ErrInvalidStateTransition) {
		t.Errorf("expected ErrInvalidStateTransition, got %v", err)
	}
	if inv.Status != domain.InvoiceDraft {
		t.Error("status must not change on rejected transition")
	}
}

func TestMarkPaid_FromVoid_Rejected(t *testing.T) {
	t.Parallel()
	inv := domain.New(uuid.New(), "drv-1")
	inv.Status = domain.InvoiceVoid
	err := inv.MarkPaid(time.Now().UTC())
	if !errors.Is(err, domain.ErrInvalidStateTransition) {
		t.Errorf("expected ErrInvalidStateTransition, got %v", err)
	}
}

// ----- MarkOverdue -----

func TestMarkOverdue_HappyPath_Manual_Issued(t *testing.T) {
	t.Parallel()
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentManual)
	inv.Issue(time.Now().UTC())
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	if err := inv.MarkOverdue(now); err != nil {
		t.Fatalf("MarkOverdue: %v", err)
	}
	if inv.Status != domain.InvoiceOverdue {
		t.Errorf("Status = %q, want OVERDUE", inv.Status)
	}
	if inv.OverdueAt == nil || !inv.OverdueAt.Equal(now) {
		t.Errorf("OverdueAt should be %v", now)
	}
}

func TestMarkOverdue_Idempotent(t *testing.T) {
	t.Parallel()
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentManual)
	inv.Issue(time.Now().UTC())
	first := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := inv.MarkOverdue(first); err != nil {
		t.Fatalf("MarkOverdue: %v", err)
	}

	second := first.Add(time.Hour)
	if err := inv.MarkOverdue(second); err != nil {
		t.Fatalf("idempotent MarkOverdue should not error: %v", err)
	}
	if !inv.OverdueAt.Equal(first) {
		t.Error("idempotent call must not advance OverdueAt")
	}
}

func TestMarkOverdue_AutoMode_Rejected(t *testing.T) {
	t.Parallel()
	// AUTO mode never goes OVERDUE — auto-pay handles settlement.
	inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentAuto)
	inv.Issue(time.Now().UTC())

	err := inv.MarkOverdue(time.Now().UTC())
	if !errors.Is(err, domain.ErrInvalidStateTransition) {
		t.Errorf("AUTO mode must not transition to OVERDUE; got err = %v", err)
	}
	if inv.Status != domain.InvoiceIssued {
		t.Error("status must remain ISSUED on rejected transition")
	}
}

func TestMarkOverdue_FromNonIssuedStates_Rejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		seed domain.InvoiceStatus
	}{
		{"from DRAFT", domain.InvoiceDraft},
		{"from PAID", domain.InvoicePaid},
		{"from VOID", domain.InvoiceVoid},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			inv := domain.NewWithMode(uuid.New(), "drv-1", domain.PaymentManual)
			inv.Status = c.seed

			err := inv.MarkOverdue(time.Now().UTC())
			if !errors.Is(err, domain.ErrInvalidStateTransition) {
				t.Errorf("expected ErrInvalidStateTransition, got %v", err)
			}
			if inv.Status != c.seed {
				t.Error("status must not change on rejected transition")
			}
		})
	}
}

// ----- Sentinel errors -----

func TestSentinelErrors_AreNonNil(t *testing.T) {
	t.Parallel()
	if domain.ErrInvoiceNotFound == nil {
		t.Error("ErrInvoiceNotFound should be defined")
	}
	if domain.ErrInvalidStateTransition == nil {
		t.Error("ErrInvalidStateTransition should be defined")
	}
	if domain.ErrDuplicateEvent == nil {
		t.Error("ErrDuplicateEvent should be defined")
	}
}
