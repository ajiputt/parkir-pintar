// Package usecase — payment application service.
package usecase

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/pkg/idempotency"
	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/adapter/midtrans"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
)

// Repo — payment persistence port.
type Repo interface {
	Save(ctx context.Context, p *domain.Payment) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error)
	GetByGatewayRef(ctx context.Context, ref string) (*domain.Payment, error)
	GetByInvoiceID(ctx context.Context, invoiceID uuid.UUID) (*domain.Payment, error)
	LogWebhook(ctx context.Context, paymentID *uuid.UUID, source, signature string, raw []byte, verified bool) error
}

// InvoiceLookup — port untuk panggil billing service.
type InvoiceLookup interface {
	Get(ctx context.Context, invoiceID uuid.UUID) (driverID string, amountIDR int64, err error)
}

// EventPublisher — publish PaymentSucceeded/Failed.
type EventPublisher interface {
	PublishPaymentSucceeded(ctx context.Context, p *domain.Payment) error
	PublishPaymentFailed(ctx context.Context, p *domain.Payment) error
}

// GatewayClient — Midtrans abstraksi (real or mock).
type GatewayClient interface {
	Charge(ctx context.Context, req midtrans.ChargeRequest) (*midtrans.ChargeResponse, error)
}

// Clock — abstraksi waktu (untuk testability).
type Clock interface {
	Now() time.Time
}

// Service — payment application service.
type Service struct {
	Payments    Repo
	Invoices    InvoiceLookup
	Pub         EventPublisher
	Gateway     GatewayClient
	Idempotency idempotency.Store
	Clock       Clock
	ServerKey   string        // utk verify signature webhook
	IdemTTL     time.Duration // default 24h

	// AutoPayMode — kalau true, HandleInvoiceIssued akan auto-charge mock saat
	// terima event InvoiceIssued dari billing service. Gateway "MOCK", langsung
	// SUCCESS, publish PaymentSucceeded → billing mark invoice PAID.
	//
	// Production toggle: false untuk manual flow (driver scan QRIS), true untuk
	// wallet/auto-charge model. Demo: true.
	AutoPayMode bool
}

// CreatePaymentInput — DTO usecase.
type CreatePaymentInput struct {
	InvoiceID      uuid.UUID
	IdempotencyKey string // dari header x-idempotency-key
}

// CreatePayment — generate QRIS untuk invoice.
//
// Idempotency:
//   - Jika header IdempotencyKey ada, cek apakah payment untuk invoice ini
//     sudah ada (UNIQUE per invoice_id). Kalau ada dan masih PENDING, return
//     yang sudah ada (idempotent QR generation).
//   - Plus standard idempotency-key store wrapper untuk full request dedup.
func (s *Service) CreatePayment(ctx context.Context, in CreatePaymentInput) (*domain.Payment, error) {
	// Cepat-cek: invoice ini sudah ada payment PENDING? Kalau ada, return.
	if existing, err := s.Payments.GetByInvoiceID(ctx, in.InvoiceID); err == nil {
		if existing.Status == domain.StatusPending && !s.isExpired(existing) {
			return existing, nil
		}
		// PENDING tapi expired, atau status lain → buat baru di bawah.
	}

	driverID, amount, err := s.Invoices.Get(ctx, in.InvoiceID)
	if err != nil {
		return nil, err
	}

	p := domain.New(in.InvoiceID, domain.MethodQRIS, money.IDR(amount))
	p.IdempotencyKey = in.IdempotencyKey

	resp, err := s.Gateway.Charge(ctx, midtrans.ChargeRequest{
		OrderID:    p.ID.String(),
		GrossIDR:   amount,
		CustomerID: driverID,
	})
	if err != nil {
		return nil, err
	}
	p.GatewayRef = resp.TransactionID
	p.QRString = resp.QRString
	if !resp.ExpiryTime.IsZero() {
		exp := resp.ExpiryTime
		p.ExpiresAt = &exp
	}

	if err := s.Payments.Save(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// MidtransNotification — payload struct dari webhook Midtrans.
//
// Reference: https://docs.midtrans.com/reference/notification-webhooks
type MidtransNotification struct {
	TransactionStatus string `json:"transaction_status"`
	OrderID           string `json:"order_id"`
	TransactionID     string `json:"transaction_id"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	FraudStatus       string `json:"fraud_status"`
	PaymentType       string `json:"payment_type"`
}

// HandleMidtransNotification — webhook handler.
//
// Status mapping (per Midtrans docs):
//
//	capture      → SUCCESS (kalau fraud_status=accept)
//	settlement   → SUCCESS (final state)
//	pending      → PENDING (no action)
//	deny         → FAILED
//	cancel       → FAILED
//	expire       → EXPIRED
//	failure      → FAILED
//
// Fraud status:
//
//	challenge    → manual review (no action)
//	accept       → proceed
//	deny         → mark failed
//
// Idempotency: webhook bisa di-redeliver Midtrans. Karena MarkSuccess/MarkFailed
// di domain return nil kalau sudah di-state target (idempotent), call ulang aman.
func (s *Service) HandleMidtransNotification(ctx context.Context, raw []byte) error {
	var pl MidtransNotification
	if err := json.Unmarshal(raw, &pl); err != nil {
		return err
	}

	verified := midtrans.VerifySignatureKey(pl.OrderID, pl.StatusCode, pl.GrossAmount, s.ServerKey, pl.SignatureKey)

	// Audit semua webhook (verified atau tidak) — bahkan invalid signature di-log untuk security investigation.
	paymentID, perr := uuid.Parse(pl.OrderID)
	var pPtr *uuid.UUID
	if perr == nil {
		pPtr = &paymentID
	}
	_ = s.Payments.LogWebhook(ctx, pPtr, "midtrans", pl.SignatureKey, raw, verified)

	if !verified {
		return domain.ErrInvalidSignature
	}
	if perr != nil {
		return perr
	}

	p, err := s.Payments.GetByID(ctx, paymentID)
	if err != nil {
		return err
	}

	now := s.now()

	switch pl.TransactionStatus {
	case "capture", "settlement":
		// Fraud status = challenge → tunda. accept/empty → proceed.
		if pl.FraudStatus == "challenge" {
			return nil
		}
		if pl.FraudStatus == "deny" {
			p.MarkFailed(now)
			if err := s.Payments.Save(ctx, p); err != nil {
				return err
			}
			return s.Pub.PublishPaymentFailed(ctx, p)
		}
		if err := p.MarkSuccess(now, pl.TransactionID); err != nil {
			return err
		}
		if err := s.Payments.Save(ctx, p); err != nil {
			return err
		}
		return s.Pub.PublishPaymentSucceeded(ctx, p)

	case "deny", "cancel", "failure":
		p.MarkFailed(now)
		if err := s.Payments.Save(ctx, p); err != nil {
			return err
		}
		return s.Pub.PublishPaymentFailed(ctx, p)

	case "expire":
		p.MarkExpired(now)
		if err := s.Payments.Save(ctx, p); err != nil {
			return err
		}
		return s.Pub.PublishPaymentFailed(ctx, p)

	case "pending":
		// Tidak ada perubahan state. Return ok.
		return nil

	default:
		// Unknown status — log dan abaikan. Jangan fail webhook karena
		// Midtrans bisa kirim status baru di masa depan.
		return nil
	}
}

// GetPayment — query.
func (s *Service) GetPayment(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	return s.Payments.GetByID(ctx, id)
}

// ----- internal helpers -----

func (s *Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) isExpired(p *domain.Payment) bool {
	if p.ExpiresAt == nil {
		return false
	}
	return s.now().After(*p.ExpiresAt)
}

// ----- Event payload helpers (dipakai oleh nats publisher) -----

type PaymentEventPayload struct {
	PaymentID string `json:"payment_id"`
	InvoiceID string `json:"invoice_id"`
	Status    string `json:"status"`
	AmountIDR int64  `json:"amount_idr"`
}

// ----- Auto-pay subscriber (mock mode) -----

// InvoiceIssuedPayload — mirror dari billing publisher (services/billing/internal/adapter/nats).
type InvoiceIssuedPayload struct {
	InvoiceID     string `json:"invoice_id"`
	ReservationID string `json:"reservation_id"`
	DriverID      string `json:"driver_id"`
	Status        string `json:"status"`
	TotalIDR      int64  `json:"total_idr"`
	PaymentMode   string `json:"payment_mode"` // AUTO atau MANUAL — ADR-0014
}

// HandleInvoiceIssued — auto-pay subscriber.
//
// Subscribed ke topic "billing.invoice.issued.v1". Saat billing ISSUE invoice
// (CheckOut, Cancelled, Expired), payment service otomatis create Payment
// record + mark SUCCESS (mock gateway), publish PaymentSucceeded.
//
// Idempotency:
//   - Kalau Payment untuk invoice ini sudah SUCCESS, skip (NATS redelivery safe).
//   - Kalau Payment ada tapi status != SUCCESS (mis. PENDING dari manual flow),
//     update jadi SUCCESS (mock auto-settle).
//
// Disabled (no-op) kalau s.AutoPayMode = false → manual flow tetap jalan via
// CreatePayment endpoint.
func (s *Service) HandleInvoiceIssued(ctx context.Context, env eventbus.Envelope) error {
	if !s.AutoPayMode {
		return nil // service-level auto-pay disabled → skip
	}

	pl, err := eventbus.Decode[InvoiceIssuedPayload](env)
	if err != nil {
		return err
	}
	// Per-reservation toggle: hanya AUTO mode yang di-charge otomatis. MANUAL
	// driver harus call CreatePayment + scan QRIS sendiri (lihat ADR-0014).
	if pl.PaymentMode != "AUTO" {
		return nil
	}
	invoiceID, err := uuid.Parse(pl.InvoiceID)
	if err != nil {
		return err
	}
	if pl.TotalIDR <= 0 {
		return nil // sanity guard
	}

	now := s.now()

	// Idempotency check: kalau payment untuk invoice ini sudah ada + SUCCESS, skip.
	if existing, err := s.Payments.GetByInvoiceID(ctx, invoiceID); err == nil && existing != nil {
		if existing.Status == domain.StatusSuccess {
			return nil
		}
		// PENDING (dari manual flow) → upgrade jadi SUCCESS (auto-settle).
		if err := existing.MarkSuccess(now, "auto-"+existing.ID.String()[:8]); err != nil {
			return err
		}
		if err := s.Payments.Save(ctx, existing); err != nil {
			return err
		}
		return s.Pub.PublishPaymentSucceeded(ctx, existing)
	}

	// Belum ada → create + mark SUCCESS sekaligus (mock auto-charge).
	p := domain.New(invoiceID, domain.MethodQRIS, money.IDR(pl.TotalIDR))
	p.Gateway = "MOCK"
	if err := p.MarkSuccess(now, "auto-"+p.ID.String()[:8]); err != nil {
		return err
	}
	if err := s.Payments.Save(ctx, p); err != nil {
		return err
	}
	return s.Pub.PublishPaymentSucceeded(ctx, p)
}

// HelperEncode — convert domain.Payment ke PaymentEventPayload untuk NATS publish.
// Dipakai di services/payment/internal/adapter/nats/publisher.go.
func HelperEncode(p *domain.Payment) PaymentEventPayload {
	return PaymentEventPayload{
		PaymentID: p.ID.String(),
		InvoiceID: p.InvoiceID.String(),
		Status:    string(p.Status),
		AmountIDR: p.Amount.Amount(),
	}
}
