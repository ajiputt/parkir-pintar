// Package postgres implements billing-domain repositories backed by PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajiperdana/parkir-pintar/pkg/eventbus"
	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/pkg/pricing"
	"github.com/ajiperdana/parkir-pintar/services/billing/internal/domain"
)

type InvoiceRepo struct {
	pool *pgxpool.Pool
}

func NewInvoiceRepo(pool *pgxpool.Pool) *InvoiceRepo {
	return &InvoiceRepo{pool: pool}
}

// Save — upsert invoice + replace items (transactional).
func (r *InvoiceRepo) Save(ctx context.Context, inv *domain.Invoice) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO invoice (id, reservation_id, driver_id, status, payment_mode, total_amount, currency, created_at, issued_at, paid_at, overdue_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO UPDATE SET
		    status=EXCLUDED.status,
		    payment_mode=EXCLUDED.payment_mode,
		    total_amount=EXCLUDED.total_amount,
		    issued_at=EXCLUDED.issued_at,
		    paid_at=EXCLUDED.paid_at,
		    overdue_at=EXCLUDED.overdue_at,
		    updated_at=EXCLUDED.updated_at
	`, inv.ID, inv.ReservationID, inv.DriverID, string(inv.Status), string(inv.PaymentMode),
		inv.Total.Amount(), inv.Total.Currency(), inv.CreatedAt, inv.IssuedAt, inv.PaidAt, inv.OverdueAt, inv.UpdatedAt)
	if err != nil {
		return err
	}

	// Replace items: simple strategy untuk demo. Production: append-only + soft delete.
	if _, err := tx.Exec(ctx, `DELETE FROM invoice_item WHERE invoice_id=$1`, inv.ID); err != nil {
		return err
	}
	for _, it := range inv.Items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoice_item (id, invoice_id, type, description, amount, period_start, period_end)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, it.ID, inv.ID, string(it.Type), it.Description, it.Amount.Amount(), it.PeriodStart, it.PeriodEnd); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *InvoiceRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Invoice, error) {
	return r.fetchOne(ctx, `WHERE id=$1`, id)
}

func (r *InvoiceRepo) GetByReservationID(ctx context.Context, resID uuid.UUID) (*domain.Invoice, error) {
	return r.fetchOne(ctx, `WHERE reservation_id=$1`, resID)
}

// FindOverdueCandidates — invoice ISSUED + MANUAL + issued_at < cutoff.
// Dipakai overdue worker untuk batch detection.
func (r *InvoiceRepo) FindOverdueCandidates(ctx context.Context, issuedBefore time.Time, limit int) ([]*domain.Invoice, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, reservation_id, driver_id, status, COALESCE(payment_mode,'AUTO'),
		       total_amount, currency,
		       created_at, issued_at, paid_at, overdue_at, updated_at
		FROM invoice
		WHERE status = 'ISSUED'
		  AND COALESCE(payment_mode,'AUTO') = 'MANUAL'
		  AND issued_at < $1
		ORDER BY issued_at ASC
		LIMIT $2
	`, issuedBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Invoice
	for rows.Next() {
		var (
			inv         domain.Invoice
			st, cur, pm string
			amount      int64
		)
		if err := rows.Scan(&inv.ID, &inv.ReservationID, &inv.DriverID, &st, &pm, &amount, &cur,
			&inv.CreatedAt, &inv.IssuedAt, &inv.PaidAt, &inv.OverdueAt, &inv.UpdatedAt); err != nil {
			return nil, err
		}
		inv.Status = domain.InvoiceStatus(st)
		inv.PaymentMode = domain.PaymentMode(pm)
		inv.Total = money.New(amount, cur)
		out = append(out, &inv)
	}
	return out, rows.Err()
}

// CountOverdueByDriver — count invoice OVERDUE per driver. Dipakai cross-service
// dari reservation untuk driver blocking pre-check.
func (r *InvoiceRepo) CountOverdueByDriver(ctx context.Context, driverID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM invoice WHERE driver_id = $1 AND status = 'OVERDUE'
	`, driverID).Scan(&count)
	return count, err
}

func (r *InvoiceRepo) fetchOne(ctx context.Context, where string, arg any) (*domain.Invoice, error) {
	q := `SELECT id, reservation_id, driver_id, status, COALESCE(payment_mode, 'AUTO'),
	             total_amount, currency,
	             created_at, issued_at, paid_at, overdue_at, updated_at
	      FROM invoice ` + where
	row := r.pool.QueryRow(ctx, q, arg)
	var (
		inv         domain.Invoice
		st, cur, pm string
		amount      int64
	)
	err := row.Scan(&inv.ID, &inv.ReservationID, &inv.DriverID, &st, &pm, &amount, &cur,
		&inv.CreatedAt, &inv.IssuedAt, &inv.PaidAt, &inv.OverdueAt, &inv.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvoiceNotFound
		}
		return nil, err
	}
	inv.Status = domain.InvoiceStatus(st)
	inv.PaymentMode = domain.PaymentMode(pm)
	inv.Total = money.New(amount, cur)

	rows, err := r.pool.Query(ctx, `SELECT id, type, description, amount, period_start, period_end
	                                FROM invoice_item WHERE invoice_id=$1 ORDER BY created_at`, inv.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			it     domain.Item
			t      string
			amount int64
		)
		if err := rows.Scan(&it.ID, &t, &it.Description, &amount, &it.PeriodStart, &it.PeriodEnd); err != nil {
			return nil, err
		}
		it.Type = pricing.LineKind(t)
		it.Amount = money.IDR(amount)
		inv.Items = append(inv.Items, it)
	}
	return &inv, nil
}

// EventLog implementasi.
type EventLog struct {
	pool *pgxpool.Pool
}

func NewEventLog(pool *pgxpool.Pool) *EventLog {
	return &EventLog{pool: pool}
}

func (e *EventLog) Seen(ctx context.Context, sourceEventID string) (bool, error) {
	var count int
	err := e.pool.QueryRow(ctx, `SELECT COUNT(*) FROM events_log WHERE source_event_id=$1`, sourceEventID).Scan(&count)
	return count > 0, err
}

func (e *EventLog) Append(ctx context.Context, env eventbus.Envelope) error {
	body, _ := json.Marshal(env.Payload)
	_, err := e.pool.Exec(ctx, `
		INSERT INTO events_log (source_event_id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (source_event_id) DO NOTHING
	`, env.ID, env.AggregateType, env.AggregateID, env.Type, body, env.OccurredAt)
	return err
}
