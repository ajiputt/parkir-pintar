// Package domain — payment models.
package domain

import (
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/errs"
	"github.com/ajiperdana/parkir-pintar/pkg/money"
)

type Status string

const (
	StatusPending Status = "PENDING"
	StatusSuccess Status = "SUCCESS"
	StatusFailed  Status = "FAILED"
	StatusExpired Status = "EXPIRED"
)

type Method string

const MethodQRIS Method = "QRIS"

type Payment struct {
	ID             uuid.UUID
	InvoiceID      uuid.UUID
	Method         Method
	Gateway        string
	GatewayRef     string
	QRString       string
	QRURL          string // Midtrans actions[generate-qr-code].url - public image URL
	Amount         money.Money
	Status         Status
	IdempotencyKey string
	CreatedAt      time.Time
	SettledAt      *time.Time
	ExpiresAt      *time.Time
}

func New(invoiceID uuid.UUID, method Method, amount money.Money) *Payment {
	now := time.Now().UTC()
	exp := now.Add(15 * time.Minute) // QR berlaku 15 menit
	return &Payment{
		ID:        uuid.New(),
		InvoiceID: invoiceID,
		Method:    method,
		Gateway:   "MIDTRANS",
		Amount:    amount,
		Status:    StatusPending,
		CreatedAt: now,
		ExpiresAt: &exp,
	}
}

func (p *Payment) MarkSuccess(now time.Time, gatewayRef string) error {
	if p.Status == StatusSuccess {
		return nil // idempotent
	}
	if p.Status != StatusPending {
		return ErrInvalidTransition
	}
	p.Status = StatusSuccess
	p.SettledAt = &now
	if gatewayRef != "" {
		p.GatewayRef = gatewayRef
	}
	return nil
}

func (p *Payment) MarkFailed(now time.Time) {
	if p.Status == StatusPending {
		p.Status = StatusFailed
		p.SettledAt = &now
	}
}

// MarkExpired — webhook expire dari Midtrans (QR tidak di-bayar dalam 15 menit).
func (p *Payment) MarkExpired(now time.Time) {
	if p.Status == StatusPending {
		p.Status = StatusExpired
		p.SettledAt = &now
	}
}

// Errors.
var (
	ErrPaymentNotFound   = errs.New(errs.KindNotFound, "PAY-001", "payment not found")
	ErrInvalidTransition = errs.New(errs.KindFailedPrecondition, "PAY-002", "invalid payment state transition")
	ErrInvalidSignature  = errs.New(errs.KindUnauthenticated, "PAY-010", "webhook signature mismatch")
)
