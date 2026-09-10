package coupon

import (
	"context"
	"entgo.io/ent/dialect/sql"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/flashsale"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon/port"
	"time"
)

func (r *CouponRepoImpl) Reserve(ctx context.Context, id uint64, qty int32) error {
	if qty <= 0 {
		return fmt.Errorf("coupon.INVALID_QUANTITY")
	}
	client := data.Client(ctx, r.data)
	n, err := client.FlashSale.Update().Where(flashsale.ID(id), flashsale.StartAtLTE(time.Now().UTC()), flashsale.EndAtGTE(time.Now().UTC()), func(s *sql.Selector) {
		s.Where(sql.P(func(b *sql.Builder) {
			b.Ident(flashsale.FieldSoldQty).WriteString(" + ").Ident(flashsale.FieldReservedQty).WriteString(" + ").Arg(qty).WriteString(" <= ").Ident(flashsale.FieldLimitQty)
		}))
	}).AddReservedQty(qty).Save(ctx)
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	fs, err := client.FlashSale.Get(ctx, id)
	if err != nil {
		return err
	}
	if fs.LimitQty-fs.SoldQty >= qty && fs.ReservedQty > 0 {
		return port.ErrFlashReserved
	}
	return port.ErrFlashSoldOut
}

func (r *CouponRepoImpl) Confirm(ctx context.Context, id uint64, qty int32) error {
	if qty <= 0 {
		return fmt.Errorf("coupon.INVALID_QUANTITY")
	}
	n, err := data.Client(ctx, r.data).FlashSale.Update().Where(flashsale.ID(id), flashsale.ReservedQtyGTE(qty)).AddReservedQty(-qty).AddSoldQty(qty).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("coupon.FLASH_RESERVATION_MISSING")
	}
	return nil
}

func (r *CouponRepoImpl) Release(ctx context.Context, id uint64, qty int32) error {
	if qty <= 0 {
		return fmt.Errorf("coupon.INVALID_QUANTITY")
	}
	n, err := data.Client(ctx, r.data).FlashSale.Update().Where(flashsale.ID(id), flashsale.ReservedQtyGTE(qty)).AddReservedQty(-qty).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("coupon.FLASH_RESERVATION_MISSING")
	}
	return nil
}
