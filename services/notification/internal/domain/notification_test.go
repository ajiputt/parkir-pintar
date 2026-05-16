package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

func TestNew_BuildsPendingNotification(t *testing.T) {
	t.Parallel()
	n := domain.New("drv-1", domain.KindReservationConfirmed, domain.ChannelEmail,
		"evt-uuid-1", "Subject: hello", "Body content")

	if n == nil {
		t.Fatal("New returned nil")
	}
	if n.ID == uuid.Nil {
		t.Error("ID must be generated")
	}
	if n.DriverID != "drv-1" {
		t.Errorf("DriverID = %q", n.DriverID)
	}
	if n.Kind != domain.KindReservationConfirmed {
		t.Errorf("Kind = %q", n.Kind)
	}
	if n.Channel != domain.ChannelEmail {
		t.Errorf("Channel = %q", n.Channel)
	}
	if n.Subject != "Subject: hello" {
		t.Errorf("Subject = %q", n.Subject)
	}
	if n.Body != "Body content" {
		t.Errorf("Body = %q", n.Body)
	}
	if n.Status != domain.StatusPending {
		t.Errorf("Status = %q, want PENDING", n.Status)
	}
	if n.EventID != "evt-uuid-1" {
		t.Errorf("EventID = %q", n.EventID)
	}
	if n.SentAt != nil {
		t.Error("SentAt should be nil on fresh notification")
	}
	if n.LastError != "" {
		t.Errorf("LastError should be empty, got %q", n.LastError)
	}
	if n.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestMarkSent_SetsStatusAndTimestamp(t *testing.T) {
	t.Parallel()
	n := domain.New("drv-1", domain.KindInvoiceIssued, domain.ChannelEmail, "evt-1", "s", "b")
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	n.MarkSent(now)

	if n.Status != domain.StatusSent {
		t.Errorf("Status = %q, want SENT", n.Status)
	}
	if n.SentAt == nil || !n.SentAt.Equal(now) {
		t.Errorf("SentAt should equal %v", now)
	}
}

func TestMarkFailed_SetsStatusAndError(t *testing.T) {
	t.Parallel()
	n := domain.New("drv-1", domain.KindInvoiceIssued, domain.ChannelEmail, "evt-1", "s", "b")

	n.MarkFailed("SES connection refused")

	if n.Status != domain.StatusFailed {
		t.Errorf("Status = %q, want FAILED", n.Status)
	}
	if n.LastError != "SES connection refused" {
		t.Errorf("LastError = %q", n.LastError)
	}
}

func TestMarkFailed_TruncatesLongError(t *testing.T) {
	t.Parallel()
	// Internal limit = 1000 chars. Build a 1500-char error and ensure truncation.
	longErr := strings.Repeat("x", 1500)
	n := domain.New("drv-1", domain.KindPaymentFailed, domain.ChannelEmail, "evt-1", "s", "b")

	n.MarkFailed(longErr)

	if len(n.LastError) != 1000 {
		t.Errorf("LastError length = %d, want 1000 (truncated)", len(n.LastError))
	}
}

func TestMarkFailed_ShortError_NotTruncated(t *testing.T) {
	t.Parallel()
	n := domain.New("drv-1", domain.KindPaymentFailed, domain.ChannelEmail, "evt-1", "s", "b")
	n.MarkFailed("short")
	if n.LastError != "short" {
		t.Errorf("LastError = %q", n.LastError)
	}
}

func TestKindConstants_Stable(t *testing.T) {
	t.Parallel()
	// Wire-level snapshot: konsumer external bergantung pada nilai string ini.
	cases := map[domain.Kind]string{
		domain.KindReservationConfirmed: "RESERVATION_CONFIRMED",
		domain.KindReservationExpired:   "RESERVATION_EXPIRED",
		domain.KindInvoiceIssued:        "INVOICE_ISSUED",
		domain.KindInvoiceOverdue:       "INVOICE_OVERDUE",
		domain.KindPaymentSucceeded:     "PAYMENT_SUCCEEDED",
		domain.KindPaymentFailed:        "PAYMENT_FAILED",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("Kind constant %v = %q, want %q", k, string(k), want)
		}
	}
}

func TestChannelConstants_Stable(t *testing.T) {
	t.Parallel()
	if string(domain.ChannelEmail) != "EMAIL" {
		t.Errorf("ChannelEmail = %q", domain.ChannelEmail)
	}
	if string(domain.ChannelSMS) != "SMS" {
		t.Errorf("ChannelSMS = %q", domain.ChannelSMS)
	}
	if string(domain.ChannelPush) != "PUSH" {
		t.Errorf("ChannelPush = %q", domain.ChannelPush)
	}
}

func TestStatusConstants_Stable(t *testing.T) {
	t.Parallel()
	if string(domain.StatusPending) != "PENDING" {
		t.Errorf("StatusPending = %q", domain.StatusPending)
	}
	if string(domain.StatusSent) != "SENT" {
		t.Errorf("StatusSent = %q", domain.StatusSent)
	}
	if string(domain.StatusFailed) != "FAILED" {
		t.Errorf("StatusFailed = %q", domain.StatusFailed)
	}
}

func TestSentinelErrors_DefinedAndDescriptive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{domain.ErrContactNotFound, "contact not found"},
		{domain.ErrContactOptedOut, "opted out"},
		{domain.ErrTemplateNotFound, "template not found"},
		{domain.ErrTemplateRenderFailed, "render"},
		{domain.ErrSendFailed, "delivery failed"},
		{domain.ErrAlreadyDispatched, "dispatched"},
	}
	for _, c := range cases {
		if c.err == nil {
			t.Errorf("error must be defined")
			continue
		}
		if !strings.Contains(c.err.Error(), c.want) {
			t.Errorf("error %q must contain %q", c.err.Error(), c.want)
		}
	}
}
