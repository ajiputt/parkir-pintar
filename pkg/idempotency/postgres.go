package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore — production-grade idempotency store di Postgres.
//
// Schema (lihat deploy/migrations/<service>/00X_idempotency.sql):
//
//	CREATE TABLE idempotency_keys (
//	  key             TEXT PRIMARY KEY,
//	  request_hash    TEXT NOT NULL,
//	  response_status INT,
//	  response_body   JSONB,
//	  status          TEXT NOT NULL,
//	  created_at      TIMESTAMPTZ DEFAULT now(),
//	  expires_at      TIMESTAMPTZ NOT NULL
//	);
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Begin(ctx context.Context, key, hash string, ttl time.Duration) (*Record, bool, error) {
	now := time.Now()
	exp := now.Add(ttl)

	// INSERT ... ON CONFLICT DO NOTHING — atomic reserve.
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO idempotency_keys (key, request_hash, status, created_at, expires_at)
		VALUES ($1, $2, 'PROCESSING', $3, $4)
		ON CONFLICT (key) DO NOTHING
	`, key, hash, now, exp)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		// new row inserted
		return nil, false, nil
	}

	// already existed → fetch
	rec, err := s.Get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	// auto-cleanup expired
	if time.Now().After(rec.ExpiresAt) {
		_, _ = s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE key=$1`, key)
		// retry begin
		return s.Begin(ctx, key, hash, ttl)
	}
	return rec, true, nil
}

func (s *PostgresStore) Complete(ctx context.Context, key string, status int, body []byte) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE idempotency_keys
		SET status='COMPLETED', response_status=$2, response_body=$3
		WHERE key=$1
	`, key, status, body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrKeyNotFound
	}
	return nil
}

func (s *PostgresStore) Fail(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `UPDATE idempotency_keys SET status='FAILED' WHERE key=$1`, key)
	return err
}

func (s *PostgresStore) Get(ctx context.Context, key string) (*Record, error) {
	var rec Record
	err := s.pool.QueryRow(ctx, `
		SELECT key, request_hash, COALESCE(response_status,0), COALESCE(response_body,'null'::jsonb), status, created_at, expires_at
		FROM idempotency_keys
		WHERE key=$1
	`, key).Scan(&rec.Key, &rec.RequestHash, &rec.ResponseStatus, &rec.ResponseBody, &rec.Status, &rec.CreatedAt, &rec.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyNotFound
		}
		return nil, err
	}
	return &rec, nil
}
