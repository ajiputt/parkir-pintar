package usecase

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// ExpiryWorker — background worker yang scan reservation CONFIRMED dengan
// expires_at < now() dan transisikan ke EXPIRED + release spot.
//
// Pakai SELECT FOR UPDATE SKIP LOCKED supaya replicas bisa run concurrent
// tanpa interferensi.
type ExpiryWorker struct {
	Reservations ReservationRepo
	Spots        SpotRepo
	Events       EventPublisher
	Clock        Clock
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
		if err := w.Reservations.UpdateState(ctx, r); err != nil {
			w.Logger.Error("update state", zap.String("res_id", r.ID.String()), zap.Error(err))
			continue
		}
		if err := w.Spots.MarkAvailable(ctx, r.SpotID); err != nil {
			w.Logger.Error("release spot", zap.Error(err))
		}
		if err := w.Events.PublishReservationExpired(ctx, r); err != nil {
			w.Logger.Warn("publish expired", zap.Error(err))
		}
	}
}
