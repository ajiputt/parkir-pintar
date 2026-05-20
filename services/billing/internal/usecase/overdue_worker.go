package usecase

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
)

// OverdueWorker — background scan invoice ISSUED + MANUAL mode + age > grace
// period. Mark OVERDUE + publish event ke notification + driver-blocking.
//
// Lihat ADR-0014.
type OverdueWorker struct {
	Invoices    OverdueInvoiceRepo
	Pub         EventPublisher
	Interval    time.Duration // scan frequency (mis. 5 menit)
	GracePeriod time.Duration // mis. 15 menit setelah issued
	BatchSize   int
	Logger      *zap.Logger
}

//go:generate mockgen -package=mock_usecase -source=overdue_worker.go -destination=../../_mock/usecase/overdue_worker_mock.go

// OverdueInvoiceRepo — port subset, hanya yang dibutuhkan worker.
type OverdueInvoiceRepo interface {
	FindOverdueCandidates(ctx context.Context, issuedBefore time.Time, limit int) ([]*domain.Invoice, error)
	Save(ctx context.Context, inv *domain.Invoice) error
}

// Run — loop sampai ctx canceled. Best-effort: error per-invoice di-log,
// loop tetap continue.
func (w *OverdueWorker) Run(ctx context.Context) {
	if w.Interval == 0 {
		w.Interval = 5 * time.Minute
	}
	if w.GracePeriod == 0 {
		w.GracePeriod = 15 * time.Minute
	}
	if w.BatchSize == 0 {
		w.BatchSize = 100
	}
	if w.Logger == nil {
		w.Logger = zap.NewNop()
	}

	w.Logger.Info("overdue worker started",
		zap.Duration("interval", w.Interval),
		zap.Duration("grace_period", w.GracePeriod),
	)

	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.Logger.Info("overdue worker stopped")
			return
		case <-ticker.C:
			w.scanOnce(ctx)
		}
	}
}

func (w *OverdueWorker) scanOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-w.GracePeriod)
	invoices, err := w.Invoices.FindOverdueCandidates(ctx, cutoff, w.BatchSize)
	if err != nil {
		w.Logger.Error("overdue scan", zap.Error(err))
		return
	}
	if len(invoices) == 0 {
		return
	}
	w.Logger.Info("overdue candidates found", zap.Int("count", len(invoices)))

	now := time.Now().UTC()
	for _, inv := range invoices {
		if err := inv.MarkOverdue(now); err != nil {
			w.Logger.Warn("MarkOverdue rejected",
				zap.String("invoice_id", inv.ID.String()),
				zap.String("status", string(inv.Status)),
				zap.Error(err))
			continue
		}
		if err := w.Invoices.Save(ctx, inv); err != nil {
			w.Logger.Error("save invoice",
				zap.String("invoice_id", inv.ID.String()),
				zap.Error(err))
			continue
		}
		if err := w.Pub.PublishInvoiceOverdue(ctx, inv); err != nil {
			w.Logger.Warn("publish InvoiceOverdue (best-effort)",
				zap.String("invoice_id", inv.ID.String()),
				zap.Error(err))
		}
		w.Logger.Info("invoice marked OVERDUE",
			zap.String("invoice_id", inv.ID.String()),
			zap.String("driver_id", inv.DriverID))
	}
}
