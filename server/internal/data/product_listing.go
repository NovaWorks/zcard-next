package data

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

// ManualListing must be used under GuardProductWrite. Explicit manual actions
// pause automation, including a repeated "off" on an already-off product.
func ManualListing(q *ent.ProductUpdateOne, status int8) *ent.ProductUpdateOne {
	return q.SetStatus(status).SetAutoListing(false).SetListingReason("manual").
		SetListingChangedAt(time.Now().UnixMilli()).SetListingZeroSince(0).
		SetListingRestocked(false).SetListingMessage("人工设置，自动上下架已暂停")
}

// ObserveListing runs in the caller's transaction, after taking the product
// write guard. checked is the start of the upstream observation, not its finish.
// Unknown observations interrupt zero-stock confirmation and never change status.
func ObserveListing(ctx context.Context, d *Data, p *ent.Product, stock int32, active bool, checked time.Time) error {
	if checked.IsZero() || checked.UnixMilli() <= p.ListingObservedAt || checked.UnixMilli() <= p.ListingChangedAt || time.Since(checked) > 5*time.Minute {
		return nil
	}
	if p.IsLocked {
		return ErrProductLocked
	}
	if local, err := HasLocalDelivery(ctx, Client(ctx, d), p); err != nil {
		return err
	} else if local {
		return nil
	}
	q := Client(ctx, d).Product.UpdateOneID(p.ID).SetListingObservedAt(checked.UnixMilli())
	if stock < -1 && active {
		return q.SetListingZeroSince(0).SetListingMessage("库存待确认，保留当前上下架状态").Exec(ctx)
	}
	q.SetListingLastStock(stock)
	available := active && (stock > 0 || stock == -1)
	if available && p.ListingLastStock == 0 && p.Status == 0 {
		q.SetListingRestocked(true)
	}
	nextStatus, reason := p.Status, p.ListingReason
	switch {
	case !active:
		if p.AutoListing {
			q.SetListingReason("upstream_unavailable")
		}
		q.SetListingZeroSince(0).SetListingMessage("上游停售或不可售，请核实后人工恢复")
		if p.AutoListing && p.Status > 0 {
			nextStatus, reason = 0, "upstream_unavailable"
			q.SetListingRestoreStatus(p.Status)
		}
	case stock == 0:
		if p.ListingZeroSince == 0 {
			q.SetListingZeroSince(checked.UnixMilli())
		}
		q.SetListingRestocked(false).SetListingMessage("已确认缺货，等待下一次独立检查复核")
		if p.AutoListing && p.Status > 0 && p.ListingZeroSince > 0 && checked.UnixMilli()-p.ListingZeroSince >= 30000 {
			nextStatus, reason = 0, "stock_out"
			q.SetListingRestoreStatus(p.Status).SetListingMessage("连续确认缺货，已自动下架；补货后恢复原展示状态")
		}
	case available:
		q.SetListingZeroSince(0).SetListingMessage("上游有货")
		if p.AutoListing && p.Status == 0 && p.ListingReason == "stock_out" {
			if p.Price <= 0 {
				q.SetListingMessage("上游有货，但本地售价无效，暂不自动恢复")
			} else {
				nextStatus = p.ListingRestoreStatus
				if nextStatus != 2 {
					nextStatus = 1
				}
				reason = "stock_recovered"
				q.SetListingRestocked(false).SetListingMessage("补货已确认，自动恢复原展示状态")
			}
		}
	}
	if nextStatus != p.Status {
		q.SetStatus(nextStatus).SetListingReason(reason)
		if err := Client(ctx, d).AuditLog.Create().SetOperatorType("system").SetOperatorID(0).SetPermissionPoint("catalog:write").SetAction("PUT").SetRoute("supply/status").SetBefore(map[string]any{"product_id": p.ID, "status": p.Status}).SetAfter(map[string]any{"subsite_id": p.SubsiteID, "product_id": p.ID, "status": nextStatus, "reason": reason, "stock": stock}).Exec(ctx); err != nil {
			return err
		}
	}
	return q.Exec(ctx)
}
