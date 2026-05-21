// Transaction helper untuk wrap multi-step DB operations dalam single atomic tx.
//
// Equivalent ke Spring's @Transactional annotation. Lihat ADR-0024 untuk
// rationale + pattern guidance.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunInTx eksekusi fn dalam single Postgres transaction.
// Commit otomatis kalau fn return nil. Rollback otomatis kalau fn return error
// atau panic.
//
// Pattern (mirror Spring @Transactional):
//
//	err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
//	    if err := repo1.DoSomething(ctx, tx, ...); err != nil { return err }
//	    if err := repo2.DoSomething(ctx, tx, ...); err != nil { return err }
//	    return nil  // commit
//	})
//
// Properties:
//   - Atomic: semua operations dalam fn commit/rollback bersama
//   - Panic-safe: defer Rollback walaupun panic propagated up
//   - Composable: caller bisa wrap fn yang lebih kompleks
//
// Tidak support nested transaction (savepoint). Kalau dipanggil dari dalam tx,
// caller responsibility untuk pass tx existing instead.
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	// Defer rollback. Tidak masalah kalau Commit sudah sukses — pgx.Tx.Rollback
	// setelah Commit return ErrTxClosed yang aman di-ignore. Pattern ini juga
	// catch panic.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p) // re-throw setelah rollback
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// RunInTxWithOptions seperti RunInTx tapi accept tx options
// (isolation level, read-only, deferrable).
//
// Use case: read-heavy queries dengan repeatable read isolation,
// atau write yang butuh serializable untuk strict consistency.
//
// Example:
//
//	err := db.RunInTxWithOptions(ctx, pool, pgx.TxOptions{
//	    IsoLevel:   pgx.Serializable,
//	    AccessMode: pgx.ReadOnly,
//	}, func(tx pgx.Tx) error { ... })
func RunInTxWithOptions(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(tx pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
