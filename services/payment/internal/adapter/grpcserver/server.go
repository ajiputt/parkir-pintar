// Package grpcserver — primary adapter, gRPC handler untuk PaymentService.
//
// Implementasi paymentv1.PaymentServiceServer (proto-generated). HandleWebhook
// dieksposisi via gRPC tapi tidak dipakai oleh public traffic — Midtrans push
// raw HTTP ke gateway → gateway forward raw body ke payment HTTP /v1/payments/
// midtrans/notification (yang masih HTTP karena raw body + signature header).
package grpcserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ajiperdana/parkir-pintar/pkg/errs"
	"github.com/ajiperdana/parkir-pintar/pkg/grpcutil"
	"github.com/ajiperdana/parkir-pintar/pkg/idempotency"
	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
	paymentv1 "github.com/ajiperdana/parkir-pintar/proto/gen/payment/v1"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/usecase"
)

// Server — gRPC server.
type Server struct {
	paymentv1.UnimplementedPaymentServiceServer

	Service     *usecase.Service
	Idempotency idempotency.Store
	IdemTTL     time.Duration
	Logger      *zap.Logger
}

// Register — daftarkan ke gRPC server.
func (s *Server) Register(grpcServer *grpc.Server) {
	paymentv1.RegisterPaymentServiceServer(grpcServer, s)
}

// CreatePayment — generate QRIS untuk invoice.
//
// Idempotency: header x-idempotency-key (gRPC metadata).
func (s *Server) CreatePayment(ctx context.Context, req *paymentv1.CreatePaymentRequest) (*paymentv1.Payment, error) {
	idemKey := grpcutil.IdempotencyKeyFromContext(ctx)

	invoiceID, err := uuid.Parse(req.GetInvoiceId())
	if err != nil {
		return nil, errs.ToStatus(errs.New(errs.KindInvalidArgument, "PAY-001", "invalid invoice_id"))
	}

	body, _ := json.Marshal(req)
	res, err := idempotency.Wrap(ctx, s.Idempotency, idemKey, "payment:create", body, s.IdemTTL,
		func(ctx context.Context) (int, []byte, error) {
			p, err := s.Service.CreatePayment(ctx, usecase.CreatePaymentInput{
				InvoiceID:      invoiceID,
				IdempotencyKey: idemKey,
			})
			if err != nil {
				return 0, nil, err
			}
			out, _ := json.Marshal(paymentToCache(p))
			return 201, out, nil
		},
	)
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	var pc paymentCache
	if err := json.Unmarshal(res.Body, &pc); err != nil {
		return nil, errs.ToStatus(err)
	}
	return cacheToProto(pc), nil
}

// GetPayment — query.
func (s *Server) GetPayment(ctx context.Context, req *paymentv1.GetPaymentRequest) (*paymentv1.Payment, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, errs.ToStatus(domain.ErrPaymentNotFound)
	}
	p, err := s.Service.GetPayment(ctx, id)
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return paymentToProto(p), nil
}

// HandleWebhook — gRPC alternative untuk webhook handler. Untuk demo, tidak
// dipakai karena Midtrans push raw HTTP. Kept here untuk future use (mis. test
// harness atau internal replay).
func (s *Server) HandleWebhook(ctx context.Context, req *paymentv1.WebhookPayload) (*paymentv1.WebhookResponse, error) {
	if err := s.Service.HandleMidtransNotification(ctx, req.GetRawPayload()); err != nil {
		return &paymentv1.WebhookResponse{Accepted: false}, errs.ToStatus(err)
	}
	return &paymentv1.WebhookResponse{Accepted: true}, nil
}

// ----- converter -----

// paymentCache — minimal data buat cache di idempotency store.
// Lebih efisien dari serialize full proto (yang punya hidden fields).
type paymentCache struct {
	ID         string     `json:"id"`
	InvoiceID  string     `json:"invoice_id"`
	Method     string     `json:"method"`
	Gateway    string     `json:"gateway"`
	GatewayRef string     `json:"gateway_ref,omitempty"`
	QRString   string     `json:"qr_string,omitempty"`
	AmountIDR  int64      `json:"amount_idr"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	SettledAt  *time.Time `json:"settled_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

func paymentToCache(p *domain.Payment) paymentCache {
	return paymentCache{
		ID:         p.ID.String(),
		InvoiceID:  p.InvoiceID.String(),
		Method:     string(p.Method),
		Gateway:    p.Gateway,
		GatewayRef: p.GatewayRef,
		QRString:   p.QRString,
		AmountIDR:  p.Amount.Amount(),
		Status:     string(p.Status),
		CreatedAt:  p.CreatedAt,
		SettledAt:  p.SettledAt,
		ExpiresAt:  p.ExpiresAt,
	}
}

func cacheToProto(c paymentCache) *paymentv1.Payment {
	return &paymentv1.Payment{
		Id:         c.ID,
		InvoiceId:  c.InvoiceID,
		Method:     methodToProto(c.Method),
		Gateway:    c.Gateway,
		GatewayRef: c.GatewayRef,
		QrString:   c.QRString,
		Amount:     &commonv1.Money{Amount: c.AmountIDR, Currency: "IDR"},
		Status:     statusToProto(c.Status),
		CreatedAt:  timeToTs(c.CreatedAt),
		SettledAt:  timePtrToTs(c.SettledAt),
		ExpiresAt:  timePtrToTs(c.ExpiresAt),
	}
}

func paymentToProto(p *domain.Payment) *paymentv1.Payment {
	if p == nil {
		return nil
	}
	return &paymentv1.Payment{
		Id:         p.ID.String(),
		InvoiceId:  p.InvoiceID.String(),
		Method:     methodToProto(string(p.Method)),
		Gateway:    p.Gateway,
		GatewayRef: p.GatewayRef,
		QrString:   p.QRString,
		Amount:     &commonv1.Money{Amount: p.Amount.Amount(), Currency: p.Amount.Currency()},
		Status:     statusToProto(string(p.Status)),
		CreatedAt:  timeToTs(p.CreatedAt),
		SettledAt:  timePtrToTs(p.SettledAt),
		ExpiresAt:  timePtrToTs(p.ExpiresAt),
	}
}

func methodToProto(s string) paymentv1.PaymentMethod {
	if s == "QRIS" {
		return paymentv1.PaymentMethod_QRIS
	}
	return paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED
}

func statusToProto(s string) paymentv1.PaymentStatus {
	switch s {
	case "PENDING":
		return paymentv1.PaymentStatus_PENDING
	case "SUCCESS":
		return paymentv1.PaymentStatus_SUCCESS
	case "FAILED":
		return paymentv1.PaymentStatus_FAILED
	case "EXPIRED":
		return paymentv1.PaymentStatus_EXPIRED
	default:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED
	}
}

func timeToTs(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timePtrToTs(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}
