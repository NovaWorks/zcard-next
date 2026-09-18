package supply

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

// recordStock changes only stock columns. Order observations by attempt start,
// comparing times in Go so legacy SQLite timezone strings remain comparable.
// A conditional update prevents a late failure from erasing a newer success.
func (r *SupplyRepoImpl) recordStock(ctx context.Context, connectionID uint64, code, sku string, n int32, started time.Time) error {
	client := data.Client(ctx, r.data)
	key := supplymapping.And(supplymapping.ConnectionID(connectionID), supplymapping.UpstreamProduct(code), supplymapping.UpstreamSkuEQ(sku))
	if n < -1 {
		n = -2
	}
	started = started.UTC().Truncate(time.Millisecond)
	for attempt := 0; attempt < 8; attempt++ {
		var old *ent.SupplyMapping
		var rawVersion sql.NullString
		if r.data.Dialect == db.SQLite {
			// SQLite may retain a timezone abbreviation the driver drops on scan.
			// Read the exact stored value alongside stock in one consistent query.
			var rows []struct {
				UpStock          int32          `json:"up_stock"`
				StockReference   int32          `json:"stock_reference"`
				StockCheckedAt   sql.NullTime   `json:"stock_checked_at"`
				StockReferenceAt sql.NullTime   `json:"stock_reference_at"`
				StockVersion     sql.NullString `json:"stock_version"`
			}
			err := client.SupplyMapping.Query().Where(key).Select(supplymapping.FieldUpStock, supplymapping.FieldStockReference, supplymapping.FieldStockCheckedAt, supplymapping.FieldStockReferenceAt).Aggregate(func(s *entsql.Selector) string {
				return "CAST(" + s.C(supplymapping.FieldStockCheckedAt) + " AS TEXT) AS stock_version"
			}).Scan(ctx, &rows)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return ErrNotFound
			}
			row := rows[0]
			rawVersion = row.StockVersion
			old = &ent.SupplyMapping{UpStock: row.UpStock, StockReference: row.StockReference, StockCheckedAt: row.StockCheckedAt.Time, StockReferenceAt: row.StockReferenceAt.Time}
		} else {
			var err error
			old, err = client.SupplyMapping.Query().Where(key).Only(ctx)
			if err != nil {
				return err
			}
		}
		checked := old.StockCheckedAt.Truncate(time.Millisecond)
		if !checked.IsZero() && (started.Before(checked) || (started.Equal(checked) && n < -1 && old.UpStock >= -1)) {
			return nil
		}
		// MySQL reports zero affected rows for an identical observation.
		if started.Equal(checked) && n == old.UpStock && (n < -1 || (old.StockReference == n && !old.StockReferenceAt.IsZero())) {
			return nil
		}
		version := supplymapping.StockCheckedAtIsNil()
		if !old.StockCheckedAt.IsZero() {
			version = supplymapping.StockCheckedAtEQ(old.StockCheckedAt)
			if r.data.Dialect == db.SQLite && rawVersion.Valid {
				version = func(s *entsql.Selector) {
					s.Where(entsql.EQ(s.C(supplymapping.FieldStockCheckedAt), rawVersion.String))
				}
			}
		}
		update := client.SupplyMapping.Update().Where(key, version, supplymapping.UpStockEQ(old.UpStock)).SetUpStock(n).SetStockCheckedAt(started)
		if n >= -1 {
			update.SetStockReference(n).SetStockReferenceAt(started)
		} else if old.StockReferenceAt.IsZero() && old.UpStock >= -1 && !old.StockCheckedAt.IsZero() {
			update.SetStockReference(old.UpStock).SetStockReferenceAt(old.StockCheckedAt)
		}
		changed, err := update.Save(ctx)
		if err != nil {
			return err
		}
		if changed > 0 {
			return nil
		}
	}
	return fmt.Errorf("库存缓存并发更新冲突")
}
