package usecase

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// ExpiryWorker — background worker yang scan reservation CONFIRMED dengan
// expires_at < now() dan transisikan ke EXPIRED + release spot.
//
// Pakai SELECT FOR UPDATE SKIP LOCKED supaya replicas bisa run concurrent
// tanpa interferensi (di repo.FindExpiredConfirmed).
//
// Per-row processing dibungkus db.RunInTx (ADR-0024) — UpdateState +
// MarkAvailable atomic per reservation. Kalau MarkAvailable fail, reservation
// tidak ke-flag EXPIRED, worker akan retry di tick berikutnya (idempotent).
type ExpiryWorker struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
	TxRunner     TxRunner
	Interval     time.Duration
	BatchSize    int
	Logger       *zap.Logger
}

// Run blocking. Stop via ctx cancellation.
func (w *ExpiryWorker) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()

	w.Logger.Info("expiry worker started",
		zap.Duration("interval", w.Interval),
		zap.Int("batch_size", w.BatchSize))

	for {
		select {
		case <-ctx.Done():
			w.Logger.Info("expiry worker stopping")
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *ExpiryWorker) tick(ctx context.Context) {
	now := w.Clock.Now()
	reservations, err := w.Reservations.FindExpiredConfirmed(ctx, now, w.BatchSize)
	if err != nil {
		w.Logger.Error("find expired", zap.Error(err))
		return
	}
	if len(reservations) == 0 {
		return
	}

	w.Logger.Info("expiring reservations", zap.Int("count", len(reservations)))
	for _, r := range reservations {
		if err := r.Expire(now); err != nil {
			continue
		}

		// Atomic: UpdateState (CONFIRMED → EXPIRED) + MarkAvailable (HELD → AVAILABLE).
		// Kalau salah satu fail, rollback semua. Reservation tetap CONFIRMED,
		// worker retry di tick berikutnya (idempotent — state machine catches).
		if err := w.TxRunner.RunInTx(ctx, func(tx pgx.Tx) error {
			if err := w.Reservations.UpdateStateTx(ctx, tx, r); err != nil {
				return err
			}
			return w.Spots.MarkAvailableTx(ctx, tx, r.SpotID)
		}); err != nil {
			w.Logger.Error("expire reservation tx",
				zap.String("res_id", r.ID.String()),
				zap.Error(err))
			continue
		}

		// Best-effort publish post-commit.
		if err := w.Events.PublishReservationExpired(ctx, r); err != nil {
			w.Logger.Warn("publish expired", zap.Error(err))
		}
	}
}
