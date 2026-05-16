// Package postgres implements notification-domain repositories backed by PostgreSQL.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajiperdana/parkir-pintar/services/notification/internal/domain"
)

type ContactRepo struct {
	pool *pgxpool.Pool
}

func NewContactRepo(pool *pgxpool.Pool) *ContactRepo {
	return &ContactRepo{pool: pool}
}

func (r *ContactRepo) GetByDriverID(ctx context.Context, driverID string) (*domain.Contact, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT driver_id, email, COALESCE(name,''), COALESCE(phone,''), opt_in, created_at, updated_at
		FROM user_contact WHERE driver_id = $1
	`, driverID)
	var c domain.Contact
	err := row.Scan(&c.DriverID, &c.Email, &c.Name, &c.Phone, &c.OptIn, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrContactNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *ContactRepo) Upsert(ctx context.Context, c *domain.Contact) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_contact (driver_id, email, name, phone, opt_in)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (driver_id) DO UPDATE
		SET email = EXCLUDED.email,
		    name = EXCLUDED.name,
		    phone = EXCLUDED.phone,
		    opt_in = EXCLUDED.opt_in,
		    updated_at = now()
	`, c.DriverID, c.Email, c.Name, c.Phone, c.OptIn)
	return err
}
