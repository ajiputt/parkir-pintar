// tx_runner.go — implementasi usecase.TxRunner port.
//
// Wrap pkg/db.RunInTx supaya usecase tidak langsung depend ke pkg/db.
// Lihat ADR-0024.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajiperdana/parkir-pintar/pkg/db"
)

// TxRunner — Postgres implementation of usecase.TxRunner.
type TxRunner struct {
	pool *pgxpool.Pool
}

// NewTxRunner — constructor.
func NewTxRunner(pool *pgxpool.Pool) *TxRunner {
	return &TxRunner{pool: pool}
}

// RunInTx — eksekusi fn dalam single Postgres transaction.
//
// Commit otomatis kalau fn return nil. Rollback otomatis kalau fn return
// error atau panic. Lihat pkg/db.RunInTx untuk detail.
func (r *TxRunner) RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.RunInTx(ctx, r.pool, fn)
}
