package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier — common interface satisfied by both *pgxpool.Pool dan pgx.Tx.
//
// Allows repository methods to accept either type transparently:
//   - Pool context: each call auto-commits independently
//   - Tx context: caller manages commit/rollback via RunInTx
//
// Usage pattern di repo:
//
//	func (r *Repo) Create(ctx context.Context, x *X) error {
//	    return r.create(ctx, r.pool, x)  // pool context
//	}
//
//	func (r *Repo) CreateTx(ctx context.Context, tx pgx.Tx, x *X) error {
//	    return r.create(ctx, tx, x)  // tx context
//	}
//
//	func (r *Repo) create(ctx context.Context, q Querier, x *X) error {
//	    _, err := q.Exec(ctx, "INSERT ...")  // works for both
//	    return err
//	}
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
