package data

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginrequirement"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// LockPurchaseProducts serializes first-rule insertion with purchase admission.
// A maintenance-locked product remains purchasable. Do not use GuardProductWrite.
func LockPurchaseProducts(ctx context.Context, d *Data, subsite uint64, ids []uint64) error {
	sorted := append([]uint64(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	c := Client(ctx, d)
	for i, id := range sorted {
		if i > 0 && sorted[i-1] == id {
			continue
		}
		if d.Dialect == db.SQLite {
			if _, err := c.Product.Update().Where(product.ID(id), product.SubsiteID(subsite)).AddPluginRuleRevision(0).Save(ctx); err != nil {
				return err
			}
		}
		q := c.Product.Query().Where(product.ID(id), product.SubsiteID(subsite))
		if d.Dialect != db.SQLite {
			q.ForUpdate()
		}
		if _, err := q.Only(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Missing injection is safe even for direct usecase callers: only a reliable
// read proving no requirement allows the legacy path to continue.
func HasPurchaseRequirements(ctx context.Context, d *Data, subsite uint64, ids []uint64) (bool, error) {
	return Client(ctx, d).PluginRequirement.Query().Where(pluginrequirement.SubsiteID(subsite), pluginrequirement.ProductIDIn(ids...), pluginrequirement.Required(true)).Exist(ctx)
}
func PurchaseRetry(ctx context.Context, d *Data, fn func(context.Context) error) error {
	if _, nested := ctx.Value(txKey{}).(*ent.Tx); nested {
		return PurchaseTx(ctx, d, fn)
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = PurchaseTx(ctx, d, fn); err == nil || !purchaseRetryable(err) {
			return err
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return kerrors.Conflict("order.CONCURRENT_UPDATE", "交易状态已变化，请重试")
}
func purchaseRetryable(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "40001" || pg.Code == "40P01"
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1213 || my.Number == 1205
	}
	var sq interface{ Code() int }
	if errors.As(err, &sq) {
		return sq.Code()&255 == 5 || sq.Code()&255 == 6
	}
	return false
}
