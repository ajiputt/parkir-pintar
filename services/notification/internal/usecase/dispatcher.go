package usecase

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	loggerpkg "github.com/ajiperdana/parkir-pintar/pkg/logger"
	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

// Note: pkg/logger di-alias `loggerpkg` karena local var `logger` di Dispatch
// shadow package-level identifier.

// Dispatcher — orchestrator: lookup contact → render template → send → log.
//
// No-retry policy (sesuai ADR-0013): pada SendFailed, mark FAILED + publish ke
// DLQ + return nil ke NATS (consumed). Tidak ack-NAK loop.
type Dispatcher struct {
	Contacts      ContactRepo
	Notifications NotificationRepo
	Sender        EmailSender
	Renderer      TemplateRenderer
	DLQ           DLQPublisher
	Logger        *zap.Logger
}

// Dispatch — full pipeline untuk satu event.
//
// kind: KindReservationConfirmed, dst.
// vars: data untuk template rendering (driver_name, spot_code, dst).
// originalSubject: NATS subject sumber (untuk DLQ tagging).
// rawPayload: original JSON event payload (untuk DLQ replay).
func (d *Dispatcher) Dispatch(
	ctx context.Context,
	env eventbus.Envelope,
	kind domain.Kind,
	driverID string,
	vars map[string]any,
	originalSubject string,
	rawPayload []byte,
) error {
	logger := d.Logger.With(
		zap.String("event_id", env.ID),
		zap.String("kind", string(kind)),
		zap.String("driver_id", driverID),
	)

	// 1. Idempotency check — sudah pernah di-dispatch event_id ini?
	exists, err := d.Notifications.ExistsForEventID(ctx, env.ID)
	if err != nil {
		logger.Error("check exists", zap.Error(err))
		return nil // skip, jangan retry NATS
	}
	if exists {
		logger.Debug("notification already dispatched (dedup)")
		return nil
	}

	// 2. Lookup contact.
	contact, err := d.Contacts.GetByDriverID(ctx, driverID)
	if err != nil {
		if errors.Is(err, domain.ErrContactNotFound) {
			logger.Warn("contact not found — skipping notification (registered driver only)")
			return nil // not found = skip silently, bukan failure
		}
		logger.Error("contact lookup", zap.Error(err))
		_ = d.DLQ.PublishDLQ(ctx, originalSubject, env.ID, rawPayload, "contact lookup error: "+err.Error())
		return nil
	}
	if !contact.OptIn {
		logger.Info("driver opted out — skipping")
		return nil
	}

	// Inject contact data ke vars (template bisa pakai {{.DriverName}}).
	vars["DriverName"] = contact.Name
	vars["DriverEmail"] = contact.Email

	// 3. Render template.
	subject, body, err := d.Renderer.Render(kind, vars)
	if err != nil {
		logger.Error("template render", zap.Error(err))
		_ = d.DLQ.PublishDLQ(ctx, originalSubject, env.ID, rawPayload, "template render: "+err.Error())
		return nil
	}

	// 4. Persist PENDING record (UNIQUE event_id → dedup at DB level).
	notif := domain.New(driverID, kind, domain.ChannelEmail, env.ID, subject, body)
	if err := d.Notifications.SaveNew(ctx, notif); err != nil {
		if errors.Is(err, domain.ErrAlreadyDispatched) {
			logger.Debug("race lost — another consumer dispatched first")
			return nil
		}
		logger.Error("persist notification", zap.Error(err))
		_ = d.DLQ.PublishDLQ(ctx, originalSubject, env.ID, rawPayload, "persist: "+err.Error())
		return nil
	}

	// 5. Send via SES.
	sendErr := d.Sender.Send(ctx, contact.Email, contact.Name, subject, body)
	now := time.Now().UTC()

	if sendErr != nil {
		logger.Warn("send failed — marking FAILED + DLQ", zap.Error(sendErr))
		notif.MarkFailed(sendErr.Error())
		_ = d.Notifications.UpdateStatus(ctx, notif.ID, domain.StatusFailed, nil, sendErr.Error())
		_ = d.DLQ.PublishDLQ(ctx, originalSubject, env.ID, rawPayload, "send: "+sendErr.Error())
		return nil
	}

	notif.MarkSent(now)
	var sentAt interface{} = now
	_ = d.Notifications.UpdateStatus(ctx, notif.ID, domain.StatusSent, &sentAt, "")
	// PII masking: log email tidak utuh untuk audit (lihat ADR-0016).
	logger.Info("notification sent",
		zap.String("to_masked", loggerpkg.MaskEmail(contact.Email)),
		zap.String("subject", subject))
	return nil
}
