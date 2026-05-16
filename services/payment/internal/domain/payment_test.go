package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
)

func TestNew_InitializesPendingPaymentWithDefaults(t *testing.T) {
	t.Parallel()
	invID := uuid.New()
	amt := money.IDR(30_000)

	p := domain.New(invID, domain.MethodQRIS, amt)

	require.NotNil(t, p)
	assert.NotEqual(t, uuid.Nil, p.ID)
	assert.Equal(t, invID, p.InvoiceID)
	assert.Equal(t, domain.MethodQRIS, p.Method)
	assert.Equal(t, "MIDTRANS", p.Gateway)
	assert.Equal(t, domain.StatusPending, p.Status)
	assert.True(t, amt.Equals(p.Amount))
	assert.WithinDuration(t, time.Now().UTC(), p.CreatedAt, 5*time.Second)

	// QR berlaku 15 menit dari CreatedAt.
	require.NotNil(t, p.ExpiresAt)
	assert.WithinDuration(t, p.CreatedAt.Add(15*time.Minute), *p.ExpiresAt, time.Second)

	// SettledAt belum di-set.
	assert.Nil(t, p.SettledAt)
}

func TestMarkSuccess_FromPending_SetsStatusAndSettledAt(t *testing.T) {
	t.Parallel()
	p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	err := p.MarkSuccess(now, "txn-abc-1")

	require.NoError(t, err)
	assert.Equal(t, domain.StatusSuccess, p.Status)
	require.NotNil(t, p.SettledAt)
	assert.Equal(t, now, *p.SettledAt)
	assert.Equal(t, "txn-abc-1", p.GatewayRef)
}

func TestMarkSuccess_EmptyGatewayRef_PreservesExisting(t *testing.T) {
	t.Parallel()
	p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
	p.GatewayRef = "existing-ref"
	now := time.Now().UTC()

	require.NoError(t, p.MarkSuccess(now, ""))
	assert.Equal(t, "existing-ref", p.GatewayRef, "empty gatewayRef must not overwrite")
}

func TestMarkSuccess_AlreadySuccess_Idempotent(t *testing.T) {
	t.Parallel()
	p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
	first := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, p.MarkSuccess(first, "txn-1"))

	// Second call should be no-op (idempotent) — original SettledAt preserved.
	second := first.Add(time.Hour)
	require.NoError(t, p.MarkSuccess(second, "txn-2"))
	assert.Equal(t, domain.StatusSuccess, p.Status)
	require.NotNil(t, p.SettledAt)
	assert.Equal(t, first, *p.SettledAt, "idempotent call should not update SettledAt")
	assert.Equal(t, "txn-1", p.GatewayRef, "idempotent call should not overwrite GatewayRef")
}

func TestMarkSuccess_FromTerminalState_Rejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		seed domain.Status
	}{
		{"from FAILED", domain.StatusFailed},
		{"from EXPIRED", domain.StatusExpired},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
			p.Status = c.seed
			err := p.MarkSuccess(time.Now().UTC(), "ref")
			assert.ErrorIs(t, err, domain.ErrInvalidTransition)
			assert.Equal(t, c.seed, p.Status, "status must not change on rejected transition")
		})
	}
}

func TestMarkFailed_FromPending_Transitions(t *testing.T) {
	t.Parallel()
	p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	p.MarkFailed(now)

	assert.Equal(t, domain.StatusFailed, p.Status)
	require.NotNil(t, p.SettledAt)
	assert.Equal(t, now, *p.SettledAt)
}

func TestMarkFailed_NonPending_NoOp(t *testing.T) {
	t.Parallel()
	// Already-SUCCESS payment should not be flipped to FAILED.
	p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
	originalSettled := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, p.MarkSuccess(originalSettled, "txn-1"))

	p.MarkFailed(originalSettled.Add(time.Hour))

	assert.Equal(t, domain.StatusSuccess, p.Status, "MarkFailed must be no-op on non-pending state")
	require.NotNil(t, p.SettledAt)
	assert.Equal(t, originalSettled, *p.SettledAt)
}

func TestMarkExpired_FromPending_Transitions(t *testing.T) {
	t.Parallel()
	p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
	now := time.Date(2026, 5, 1, 10, 15, 0, 0, time.UTC)

	p.MarkExpired(now)

	assert.Equal(t, domain.StatusExpired, p.Status)
	require.NotNil(t, p.SettledAt)
	assert.Equal(t, now, *p.SettledAt)
}

func TestMarkExpired_NonPending_NoOp(t *testing.T) {
	t.Parallel()
	p := domain.New(uuid.New(), domain.MethodQRIS, money.IDR(10_000))
	p.MarkFailed(time.Now().UTC())

	before := *p.SettledAt
	p.MarkExpired(before.Add(time.Hour))

	assert.Equal(t, domain.StatusFailed, p.Status, "expired must not overwrite failed")
	assert.Equal(t, before, *p.SettledAt)
}

func TestErrors_AreNotNilAndDescriptive(t *testing.T) {
	t.Parallel()
	// Pastikan typed error sentinels expose-able & comparable lewat errors.Is.
	assert.NotNil(t, domain.ErrPaymentNotFound)
	assert.NotNil(t, domain.ErrInvalidTransition)
	assert.NotNil(t, domain.ErrInvalidSignature)
	assert.Contains(t, domain.ErrPaymentNotFound.Error(), "payment not found")
	assert.Contains(t, domain.ErrInvalidTransition.Error(), "transition")
	assert.Contains(t, domain.ErrInvalidSignature.Error(), "signature")
}
