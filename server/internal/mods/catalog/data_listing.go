package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// Filter cached inventory before pagination. Reading this list never calls the
// upstream. Scan bounded batches so large descriptions don't all stay in memory.
func (r *ProductRepoImpl) listInventory(ctx context.Context, q *ent.ProductQuery, f port.AdminFilter) ([]*ent.Product, int64, error) {
	switch f.Inventory {
	case "", "available", "low", "empty", "unknown":
	default:
		return nil, 0, fmt.Errorf("库存筛选无效")
	}
	var out []*ent.Product
	var total int64
	start := int64(0)
	if f.Page > 0 && f.PageSize > 0 {
		start = int64(f.Page-1) * int64(f.PageSize)
	}
	for offset := 0; ; offset += 500 {
		rows, err := q.Clone().Offset(offset).Limit(500).All(ctx)
		if err != nil {
			return nil, 0, err
		}
		stocks, err := data.ProductStocks(ctx, r.data, rows)
		if err != nil {
			return nil, 0, err
		}
		for _, p := range rows {
			n, ok := stocks[p.ID]
			if !ok {
				n = -2
			}
			match := true
			switch f.Inventory {
			case "available":
				match = n > 0 || n == -1
			case "low":
				match = n > 0 && n < int64(f.LowStockThreshold)
			case "empty":
				match = n == 0
			case "unknown":
				match = n < -1
			}
			if f.RestockedOnly {
				match = match && (n > 0 || n == -1)
			}
			if !match {
				continue
			}
			if total >= start && (f.PageSize <= 0 || len(out) < int(f.PageSize)) {
				out = append(out, p)
			}
			total++
		}
		if len(rows) < 500 {
			break
		}
	}
	return out, total, nil
}

func listingRevision(p *ent.Product) string {
	// GuardProductWrite performs a no-op write on SQLite which advances updated_at.
	// Compare business state instead; taking a lock must not invalidate a preview.
	copy := *p
	copy.UpdatedAt = time.Time{}
	raw, _ := json.Marshal(copy)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (s *AdminCatalogService) listingUnsupported(ctx context.Context, p *ent.Product) string {
	if p.SubsiteID != 0 {
		return "仅主站上游商品支持自动上下架"
	}
	if p.UpstreamSourceID == 0 || p.UpstreamProductCode == "" {
		return "仅支持上游供货商品"
	}
	if p.FulfillmentMode != "auto" || string(p.StockType) != "card" {
		return "本地或人工发货不参与上游自动上下架"
	}
	c := data.Client(ctx, s.repo.data)
	local, err := data.HasLocalDelivery(ctx, c, p)
	if err != nil || local {
		return "含本地卡密或重复发货规格，暂不支持"
	}
	active, err := c.SupplyConnection.Query().Where(supplyconnection.ID(p.UpstreamSourceID), supplyconnection.StatusEQ(supplyconnection.StatusActive)).Exist(ctx)
	if err != nil || !active {
		return "货源已停用或不可用"
	}
	return ""
}

// Preview freezes IDs and revisions on the client, like keyword classification.
// Apply only accepts bounded batches of those IDs; new filter matches cannot be
// silently added. A stale revision is skipped, including retries of a completed item.
func (s *AdminCatalogService) ManageProductListing(ctx context.Context, req *adminv1.ManageProductListingRequest) (*adminv1.ManageProductListingReply, error) {
	switch req.Action {
	case "enable", "disable", "on", "off", "hide":
	default:
		return nil, fmt.Errorf("请选择有效操作")
	}
	actor, err := batchActor(ctx)
	if err != nil {
		return nil, err
	}
	out := &adminv1.ManageProductListingReply{}
	if !req.Apply {
		var rows []*ent.Product
		if len(req.Ids) > 0 {
			if len(req.Ids) > 5000 {
				return nil, fmt.Errorf("单次最多预览 5000 件，请缩小范围")
			}
			rows, err = data.Client(ctx, s.repo.data).Product.Query().Where(product.IDIn(req.Ids...), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0)).Order(ent.Asc(product.FieldID)).All(ctx)
			out.Matched = int32(len(rows))
		} else {
			if req.Filter == nil {
				return nil, fmt.Errorf("请指定商品或筛选范围")
			}
			f := s.adminFilter(ctx, req.Filter)
			f.Page = 1
			f.PageSize = 5001
			f.OptionsOnly = false
			var count int64
			rows, count, err = s.repo.ListAdmin(ctx, f)
			out.Matched = int32(count)
			if len(rows) > 5000 {
				out.Truncated = true
				rows = rows[:5000]
			}
		}
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			row := &adminv1.ProductListingTarget{Id: p.ID, Name: p.Name, Revision: listingRevision(p), Status: int32(p.Status), AutoListing: p.AutoListing, Result: "ready"}
			if p.IsLocked {
				row.Result = "locked"
				row.Message = "已锁定，跳过"
				out.SkippedLocked++
			} else if req.Action == "enable" {
				row.Message = s.listingUnsupported(ctx, p)
				if row.Message != "" {
					row.Result = "unsupported"
					out.Unsupported++
				} else if p.Status == 0 {
					row.Message = "开启后重新检查库存；确认有货将上架"
				} else {
					row.Message = "缺货复核后下架，补货后恢复原展示状态"
				}
			}
			if req.Action == "on" || req.Action == "off" || req.Action == "hide" {
				row.Message = "人工设置将暂停自动上下架"
			}
			out.Items = append(out.Items, row)
		}
		return out, nil
	}
	if len(req.Revisions) == 0 || len(req.Revisions) > 100 {
		return nil, fmt.Errorf("请先预览，每批最多处理 100 件")
	}
	ids := make([]uint64, 0, len(req.Revisions))
	for id := range req.Revisions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		row := &adminv1.ProductListingTarget{Id: id, Result: "failed"}
		err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
			p, e := data.GuardProductWrite(ctx, s.repo.data, id)
			if data.IsProductLocked(e) {
				row.Result = "locked"
				return nil
			}
			if e != nil {
				return e
			}
			row.Name = p.Name
			row.Status = int32(p.Status)
			row.AutoListing = p.AutoListing
			if listingRevision(p) != req.Revisions[id] {
				row.Result = "conflict"
				row.Message = "商品已变化，请重新预览"
				return nil
			}
			if req.Action == "enable" {
				if reason := s.listingUnsupported(ctx, p); reason != "" {
					row.Result = "unsupported"
					row.Message = reason
					return nil
				}
			}
			q := data.Client(ctx, s.repo.data).Product.UpdateOneID(id)
			switch req.Action {
			case "enable":
				q.SetAutoListing(true).SetListingChangedAt(time.Now().UnixMilli()).SetListingZeroSince(0).SetListingMessage("自动管理已开启，等待后台重新检查")
				if p.Status == 0 {
					q.SetListingReason("stock_out")
					if p.ListingReason != "stock_out" {
						q.SetListingRestoreStatus(1)
					}
				} else {
					q.SetListingRestoreStatus(p.Status)
				}
			case "disable":
				q.SetAutoListing(false).SetListingChangedAt(time.Now().UnixMilli()).SetListingZeroSince(0).SetListingMessage("自动管理已关闭，保留当前状态")
			default:
				status := int8(0)
				if req.Action == "on" {
					status = 1
				}
				if req.Action == "hide" {
					status = 2
				}
				data.ManualListing(q, status)
			}
			if e = q.Exec(ctx); e != nil {
				return e
			}
			if e = data.Client(ctx, s.repo.data).AuditLog.Create().SetOperatorType("admin").SetOperatorID(actor).SetPermissionPoint("catalog:write").SetAction("PUT").SetRoute("/api/v1/admin/products/listing").SetAfter(map[string]any{"subsite_id": p.SubsiteID, "product_id": id, "action": req.Action}).Exec(ctx); e != nil {
				return e
			}
			row.Result = "updated"
			return nil
		})
		if err != nil {
			row.Result = "failed"
			row.Message = "处理失败，请刷新后重试"
		}
		out.Matched++
		switch row.Result {
		case "updated":
			out.Changed++
		case "locked":
			out.SkippedLocked++
		case "conflict":
			out.Conflicts++
		case "unsupported":
			out.Unsupported++
		default:
			out.Failed++
		}
		out.Items = append(out.Items, row)
	}
	return out, nil
}
