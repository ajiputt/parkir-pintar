package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajiperdana/parkir-pintar/pkg/money"
	"github.com/ajiperdana/parkir-pintar/services/payment/internal/domain"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) Save(ctx context.Context, p *domain.Payment) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO payment (id, invoice_id, method, gateway, gateway_ref, qr_string, qr_url,
		    amount, currency, status, idempotency_key, created_at, settled_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO UPDATE SET
		    gateway_ref = EXCLUDED.gateway_ref,
		    qr_string   = EXCLUDED.qr_string,
		    qr_url      = EXCLUDED.qr_url,
		    status      = EXCLUDED.status,
		    settled_at  = EXCLUDED.settled_at
	`, p.ID, p.InvoiceID, string(p.Method), p.Gateway, p.GatewayRef, p.QRString, p.QRURL,
		p.Amount.Amount(), p.Amount.Currency(), string(p.Status), p.IdempotencyKey,
		p.CreatedAt, p.SettledAt, p.ExpiresAt)
	return err
}

func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Payment, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, invoice_id, method, gateway, COALESCE(gateway_ref,''), COALESCE(qr_string,''), COALESCE(qr_url,''),
		       amount, currency, status, COALESCE(idempotency_key,''),
		       created_at, settled_at, expires_at
		FROM payment WHERE id=$1
	`, id)
	return scan(row)
}

func (r *Repo) GetByGatewayRef(ctx context.Context, ref string) (*domain.Payment, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, invoice_id, method, gateway, COALESCE(gateway_ref,''), COALESCE(qr_string,''), COALESCE(qr_url,''),
		       amount, currency, status, COALESCE(idempotency_key,''),
		       created_at, settled_at, expires_at
		FROM payment WHERE gateway_ref=$1
	`, ref)
	return scan(row)
}

// GetByInvoiceID — return latest payment untuk invoice (atau ErrPaymentNotFound).
// Dipakai oleh CreatePayment untuk idempotent QR generation.
func (r *Repo) GetByInvoiceID(ctx context.Context, invoiceID uuid.UUID) (*domain.Payment, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, invoice_id, method, gateway, COALESCE(gateway_ref,''), COALESCE(qr_string,''), COALESCE(qr_url,''),
		       amount, currency, status, COALESCE(idempotency_key,''),
		       created_at, settled_at, expires_at
		FROM payment
		WHERE invoice_id=$1
		ORDER BY created_at DESC
		LIMIT 1
	`, invoiceID)
	return scan(row)
}

func (r *Repo) LogWebhook(ctx context.Context, paymentID *uuid.UUID, source, signature string, raw []byte, verified bool) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO webhook_log (payment_id, source, signature, raw_payload, verified)
		VALUES ($1,$2,$3,$4,$5)
	`, paymentID, source, signature, raw, verified)
	return err
}

type scanner interface{ Scan(...any) error }

func scan(s scanner) (*domain.Payment, error) {
	var (
		p              domain.Payment
		method, gw, st string
		amount         int64
		currency       string
	)
	err := s.Scan(&p.ID, &p.InvoiceID, &method, &gw, &p.GatewayRef, &p.QRString, &p.QRURL,
		&amount, &currency, &st, &p.IdempotencyKey,
		&p.CreatedAt, &p.SettledAt, &p.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPaymentNotFound
		}
		return nil, err
	}
	p.Method = domain.Method(method)
	p.Gateway = gw
	p.Status = domain.Status(st)
	p.Amount = money.New(amount, currency)
	return &p, nil
}
