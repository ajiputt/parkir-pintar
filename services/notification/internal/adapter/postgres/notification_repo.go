package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

const pgErrCodeUniqueViolation = "23505"

type NotificationRepo struct {
	pool *pgxpool.Pool
}

func NewNotificationRepo(pool *pgxpool.Pool) *NotificationRepo {
	return &NotificationRepo{pool: pool}
}

// SaveNew — INSERT only. UNIQUE(event_id) → return ErrAlreadyDispatched (dedup).
func (r *NotificationRepo) SaveNew(ctx context.Context, n *domain.Notification) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notification_log
		    (id, driver_id, kind, channel, subject, body, status, event_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		n.ID, n.DriverID, string(n.Kind), string(n.Channel),
		n.Subject, n.Body, string(n.Status), n.EventID, n.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeUniqueViolation {
			return domain.ErrAlreadyDispatched
		}
		return err
	}
	return nil
}

func (r *NotificationRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.Status, sentAt *interface{}, lastErr string) error {
	var sentAtTime *time.Time
	if sentAt != nil {
		if t, ok := (*sentAt).(time.Time); ok {
			sentAtTime = &t
		}
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE notification_log
		SET status = $2, sent_at = $3, last_error = $4
		WHERE id = $1
	`, id, string(status), sentAtTime, lastErr)
	return err
}

func (r *NotificationRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, driver_id, kind, channel, COALESCE(subject,''), COALESCE(body,''),
		       status, sent_at, COALESCE(last_error,''), event_id, created_at
		FROM notification_log WHERE id = $1
	`, id)

	var n domain.Notification
	var kind, channel, status string
	var sentAt *time.Time
	err := row.Scan(&n.ID, &n.DriverID, &kind, &channel, &n.Subject, &n.Body,
		&status, &sentAt, &n.LastError, &n.EventID, &n.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAlreadyDispatched // reuse "not found"-ish error
		}
		return nil, err
	}
	n.Kind = domain.Kind(kind)
	n.Channel = domain.Channel(channel)
	n.Status = domain.Status(status)
	n.SentAt = sentAt
	return &n, nil
}

func (r *NotificationRepo) ExistsForEventID(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM notification_log WHERE event_id = $1)`,
		eventID).Scan(&exists)
	return exists, err
}
