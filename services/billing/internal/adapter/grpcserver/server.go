// Package grpcserver — primary adapter, gRPC handler untuk BillingService.
//
// Implementasi billingv1.BillingServiceServer (proto-generated).
package grpcserver

import (
	"time"

	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ajiperdana/parkir-pintar/pkg/errs"
	billingv1 "github.com/ajiperdana/parkir-pintar/proto/gen/billing/v1"
	commonv1 "github.com/ajiperdana/parkir-pintar/proto/gen/common/v1"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/usecase"
)

// Server — gRPC server.
type Server struct {
	billingv1.UnimplementedBillingServiceServer

	Service *usecase.Service
	Logger  *zap.Logger
}

// Register — daftarkan ke gRPC server.
func (s *Server) Register(grpcServer *grpc.Server) {
	billingv1.RegisterBillingServiceServer(grpcServer, s)
}

// GetInvoice — query by invoice ID.
func (s *Server) GetInvoice(ctx context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.Invoice, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, errs.ToStatus(domain.ErrInvoiceNotFound)
	}
	inv, err := s.Service.GetInvoice(ctx, id)
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return invoiceToProto(inv), nil
}

// GetInvoiceByReservation — lookup invoice via reservation ID. Dipakai
// payment service untuk resolve amount sebelum CreatePayment.
func (s *Server) GetInvoiceByReservation(ctx context.Context, req *billingv1.GetInvoiceByReservationRequest) (*billingv1.Invoice, error) {
	id, err := uuid.Parse(req.GetReservationId())
	if err != nil {
		return nil, errs.ToStatus(domain.ErrInvoiceNotFound)
	}
	inv, err := s.Service.GetInvoiceByReservation(ctx, id)
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return invoiceToProto(inv), nil
}

// IssueInvoice — manual issue (admin/debug). Normalnya event-driven via NATS.
// Untuk demo: not implemented (event handler sudah cover happy path).
func (s *Server) IssueInvoice(_ context.Context, _ *billingv1.IssueInvoiceRequest) (*billingv1.Invoice, error) {
	return nil, errs.ToStatus(errs.New(errs.KindFailedPrecondition, "BIL-100", "issue invoice manual not implemented (event-driven only)"))
}

// MarkPaid — manual mark paid (admin/debug). Normalnya via PaymentSucceeded event.
func (s *Server) MarkPaid(_ context.Context, _ *billingv1.MarkPaidRequest) (*billingv1.Invoice, error) {
	return nil, errs.ToStatus(errs.New(errs.KindFailedPrecondition, "BIL-101", "mark paid manual not implemented (event-driven only)"))
}

// CountOverdueByDriver — Tier 2 ADR-0014: cross-service check dipanggil
// reservation untuk driver blocking pre-check.
func (s *Server) CountOverdueByDriver(ctx context.Context, req *billingv1.CountOverdueByDriverRequest) (*billingv1.CountOverdueByDriverResponse, error) {
	count, err := s.Service.CountOverdueByDriver(ctx, req.GetDriverId())
	if err != nil {
		return nil, errs.ToStatus(err)
	}
	return &billingv1.CountOverdueByDriverResponse{Count: int32(count)}, nil
}

// ----- converter -----

func invoiceToProto(inv *domain.Invoice) *billingv1.Invoice {
	if inv == nil {
		return nil
	}
	items := make([]*billingv1.InvoiceItem, 0, len(inv.Items))
	for _, it := range inv.Items {
		items = append(items, &billingv1.InvoiceItem{
			Id:          it.ID.String(),
			Type:        lineKindToProto(string(it.Type)),
			Description: it.Description,
			Amount: &commonv1.Money{
				Amount:   it.Amount.Amount(),
				Currency: it.Amount.Currency(),
			},
			PeriodStart: timePtrToTs(it.PeriodStart),
			PeriodEnd:   timePtrToTs(it.PeriodEnd),
		})
	}
	return &billingv1.Invoice{
		Id:            inv.ID.String(),
		ReservationId: inv.ReservationID.String(),
		DriverId:      inv.DriverID,
		Status:        invoiceStatusToProto(inv.Status),
		PaymentMode:   billingPaymentModeToProto(inv.PaymentMode),
		Total: &commonv1.Money{
			Amount:   inv.Total.Amount(),
			Currency: inv.Total.Currency(),
		},
		Items:     items,
		CreatedAt: timeToTs(inv.CreatedAt),
		IssuedAt:  timePtrToTs(inv.IssuedAt),
		PaidAt:    timePtrToTs(inv.PaidAt),
		OverdueAt: timePtrToTs(inv.OverdueAt),
	}
}

func billingPaymentModeToProto(m domain.PaymentMode) commonv1.BillingMode {
	switch m {
	case domain.PaymentAuto:
		return commonv1.BillingMode_AUTO
	case domain.PaymentManual:
		return commonv1.BillingMode_MANUAL
	default:
		return commonv1.BillingMode_BILLING_MODE_UNSPECIFIED
	}
}

func invoiceStatusToProto(s domain.InvoiceStatus) billingv1.InvoiceStatus {
	switch s {
	case domain.InvoiceDraft:
		return billingv1.InvoiceStatus_DRAFT
	case domain.InvoiceIssued:
		return billingv1.InvoiceStatus_ISSUED
	case domain.InvoicePaid:
		return billingv1.InvoiceStatus_PAID
	case domain.InvoiceVoid:
		return billingv1.InvoiceStatus_VOID
	case domain.InvoiceOverdue:
		return billingv1.InvoiceStatus_OVERDUE
	default:
		return billingv1.InvoiceStatus_INVOICE_STATUS_UNSPECIFIED
	}
}

// lineKindToProto — pricing.LineKind string → proto enum.
// pricing.LineKind values (from pkg/pricing): "BOOKING_FEE", "HOURLY", "OVERNIGHT", "NO_SHOW_PENALTY".
func lineKindToProto(s string) billingv1.LineItemType {
	switch s {
	case "BOOKING_FEE":
		return billingv1.LineItemType_BOOKING_FEE
	case "HOURLY":
		return billingv1.LineItemType_HOURLY
	case "OVERNIGHT":
		return billingv1.LineItemType_OVERNIGHT
	case "NO_SHOW_PENALTY":
		return billingv1.LineItemType_NO_SHOW_PENALTY
	default:
		return billingv1.LineItemType_LINE_ITEM_TYPE_UNSPECIFIED
	}
}

// timeToTs — Go time.Time → proto Timestamp. Zero time returns nil.
func timeToTs(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// timePtrToTs — *time.Time → proto Timestamp. Nil → nil.
func timePtrToTs(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}
