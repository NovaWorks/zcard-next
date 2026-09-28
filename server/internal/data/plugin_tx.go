package data

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

type transactionMode struct {
	owner     *Data
	isolation sql.IsolationLevel
}
type transactionModeKey struct{}

// PurchaseTx gives all membership reads one snapshot. Callers acquire product
// locks before consistent reads; SQLite callers acquire a write lock first.
func PurchaseTx(ctx context.Context, d *Data, fn func(context.Context) error) error {
	return isolatedTx(ctx, d, sql.LevelRepeatableRead, fn)
}

// RuleWriteTx reads references after waiting for their level/product locks.
func RuleWriteTx(ctx context.Context, d *Data, fn func(context.Context) error) error {
	return isolatedTx(ctx, d, sql.LevelReadCommitted, fn)
}
func isolatedTx(ctx context.Context, d *Data, level sql.IsolationLevel, fn func(context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*ent.Tx); ok {
		mode, ok := ctx.Value(transactionModeKey{}).(transactionMode)
		if !ok || mode.owner != d || mode.isolation != level {
			return fmt.Errorf("incompatible nested plugin transaction")
		}
		return fn(ctx)
	}
	opts := &sql.TxOptions{Isolation: level}
	if d.Dialect == db.SQLite {
		opts.Isolation = sql.LevelDefault
	}
	tx, err := d.Client.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ctx = context.WithValue(ctx, txKey{}, tx)
	ctx = context.WithValue(ctx, transactionModeKey{}, transactionMode{d, level})
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
