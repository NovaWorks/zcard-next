package admincmd

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/orderaccess"
)

func reencryptOrderAccess(ctx context.Context, client *ent.Client, oldCipher, newCipher *inventory.CardCipher, batchSize int) (rotated, skipped, failed int, err error) {
	var cursor uint64
	for {
		rows, e := client.Order.Query().Where(order.IDGT(cursor), order.OrderAccessTokenHashNEQ("")).Order(ent.Asc(order.FieldID)).Limit(batchSize).All(ctx)
		if e != nil {
			return rotated, skipped, failed, e
		}
		if len(rows) == 0 {
			return rotated, skipped, failed, nil
		}
		for _, row := range rows {
			// Recovery intentionally discards checkout replay secrets; their token hash remains usable.
			if len(row.OrderAccessTokenSecret) == 0 {
				continue
			}
			token, e := oldCipher.OpenOrderAccess(row.OrderAccessTokenSecret, row.OrderNo, row.SubsiteID)
			if e != nil {
				if current, e := newCipher.OpenOrderAccess(row.OrderAccessTokenSecret, row.OrderNo, row.SubsiteID); e == nil && orderaccess.VerifyToken(row.OrderAccessTokenHash, row.SubsiteID, row.OrderNo, current) {
					skipped++
				} else {
					failed++
				}
				continue
			}
			if !orderaccess.VerifyToken(row.OrderAccessTokenHash, row.SubsiteID, row.OrderNo, token) {
				failed++
				continue
			}
			sealed, e := newCipher.SealOrderAccess(token, row.OrderNo, row.SubsiteID)
			if e != nil {
				return rotated, skipped, failed, e
			}
			// A concurrent recovery must never have its discarded old secret restored.
			n, e := client.Order.Update().Where(order.ID(row.ID), order.OrderAccessTokenHash(row.OrderAccessTokenHash), order.OrderAccessTokenSecretEQ(row.OrderAccessTokenSecret)).SetOrderAccessTokenSecret(sealed).Save(ctx)
			if e != nil {
				return rotated, skipped, failed, e
			}
			if n == 1 {
				rotated++
			} else {
				skipped++
			}
		}
		cursor = rows[len(rows)-1].ID
	}
}
