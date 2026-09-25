package catalog

import (
	"context"
	"encoding/base64"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/google/uuid"
	"strings"
	"time"
)

func (s *AdminCatalogService) GetDeliverySources(ctx context.Context, req *adminv1.DeliverySourcesRequest) (*adminv1.DeliverySourcesReply, error) {
	c := data.Client(ctx, s.repo.data)
	p, err := data.ProductForDelivery(ctx, c, tenancy.FromContext(ctx).SubsiteID, req.ProductId)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.DeliverySourcesReply{Revision: p.LockVersion}
	skus, err := c.ProductSku.Query().Where(productsku.ProductID(p.ID)).Order(ent.Asc(productsku.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	var slots []*ent.ProductSku
	if len(skus) == 0 {
		slots = append(slots, nil)
	} else {
		slots = skus
	}
	for _, sk := range slots {
		id := uint64(0)
		name := "默认规格"
		if sk != nil {
			id = sk.ID
			name = sk.Name
		}
		info := &adminv1.DeliverySourceInfo{SkuId: id, SkuName: name, Mode: data.StockSource(p, sk), Status: "unconfigured"}
		src, e := data.CurrentDeliverySource(ctx, c, p, id)
		if e != nil && !ent.IsNotFound(e) {
			return nil, e
		}
		if src != nil {
			info.Id = src.ID
			info.Status = src.Status
			info.HasContent = len(src.Content) > 0
			info.ExpiresAt = src.ExpiresAt
			info.MaxDeliveries = src.MaxDeliveries
			info.Error = src.LastError
			info.OriginProcurementId = src.OriginProcurementID
			info.CostCents = src.CostCents
			info.UpstreamOrderId = src.UpstreamOrderID
			info.Deliveries = src.DeliveredCount
			if (src.Status == "ready" || src.Status == "paused") && src.ExpiresAt > 0 && src.ExpiresAt <= time.Now().Unix() {
				info.Status = "expired"
			}
		}
		reply.Sources = append(reply.Sources, info)
	}
	// Only original successful upstream receipts of this exact product are eligible.
	items, err := c.OrderItem.Query().Where(orderitem.ProductID(p.ID), orderitem.SubsiteID(p.SubsiteID), orderitem.FulfillmentTypeEQ(orderitem.FulfillmentTypeUpstream), orderitem.HasOrderWith(order.StatusNotIn(order.StatusRefunded, order.StatusCanceled, order.StatusExpired, order.StatusRefundPending))).Order(ent.Desc(orderitem.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if len(it.FormAnswers) > 0 || it.FulfillmentStatus == "refunded" {
			continue
		}
		po, e := c.ProcurementOrder.Query().Where(procurementorder.OrderItemID(it.ID), procurementorder.StatusEQ(procurementorder.StatusFulfilled)).Only(ctx)
		if ent.IsNotFound(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		rows, e := c.ProcurementItem.Query().Where(procurementitem.ProcurementID(po.ID)).All(ctx)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			continue
		}
		for i := range rows[0].ReceivedContent {
			reply.Receipts = append(reply.Receipts, &adminv1.DeliveryReceiptOption{ProcurementId: po.ID, ContentIndex: int32(i), SkuId: it.SkuID, PurchasedAt: po.CreatedAt.Unix(), Label: fmt.Sprintf("采购 #%d · 第 %d 份 · %s", po.ID, i+1, po.CreatedAt.Format("2006-01-02 15:04"))})
		}
	}
	return reply, nil
}
func (s *AdminCatalogService) SetDeliverySource(ctx context.Context, req *adminv1.SetDeliverySourceRequest) (*adminv1.DeliverySourcesReply, error) {
	if req.ExpiresAt < 0 || req.MaxDeliveries < 0 || req.MaxDeliveries > 100000000 || len(req.Content) > 60000 {
		return nil, fmt.Errorf("有效期、发放次数或内容长度无效")
	}
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		p, e := data.GuardProductWrite(ctx, s.repo.data, req.ProductId)
		if e != nil {
			return e
		}
		if p.LockVersion != req.ExpectedRevision {
			return fmt.Errorf("商品设置已变化，请刷新后重试")
		}
		sk, e := data.DeliverySKU(ctx, c, p, req.SkuId)
		if e != nil {
			return e
		}
		if req.SkuId == 0 {
			n, e := c.ProductSku.Query().Where(productsku.ProductID(p.ID)).Count(ctx)
			if e != nil {
				return e
			}
			if n > 0 {
				return fmt.Errorf("请为每个规格分别设置发货来源")
			}
		}
		src, e := data.CurrentDeliverySource(ctx, c, p, req.SkuId)
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		if req.Action != "" && req.Action != "configure" {
			if src == nil {
				return fmt.Errorf("没有已配置的复用内容")
			}
			switch req.Action {
			case "pause":
				if src.Status == "submitting" || src.Status == "polling" || src.Status == "uncertain" {
					return fmt.Errorf("首次采购结果尚未核实，请先核实原采购单；可先下架商品停止新订单")
				}
				if e := c.ProductDeliverySource.UpdateOneID(src.ID).SetStatus("paused").Exec(ctx); e != nil {
					return e
				}
			case "resume":
				if src.Status != "paused" || len(src.Content) == 0 {
					return fmt.Errorf("仅能恢复已有有效内容；未完成采购请先核实")
				}
				if src.ExpiresAt > 0 && src.ExpiresAt <= time.Now().Unix() {
					return fmt.Errorf("内容已过期，请更换内容")
				}
				if e := c.ProductDeliverySource.UpdateOneID(src.ID).SetStatus("ready").Exec(ctx); e != nil {
					return e
				}
			case "reconcile":
				if src.Status != "uncertain" || strings.TrimSpace(req.UpstreamOrderId) == "" || len(req.UpstreamOrderId) > 128 {
					return fmt.Errorf("请填写原上游订单号，核查不会重新采购")
				}
				if src.UpstreamOrderID != "" && src.UpstreamOrderID != strings.TrimSpace(req.UpstreamOrderId) {
					return fmt.Errorf("已记录原采购订单号，不能替换为其他订单")
				}
				conn, e := c.SupplyConnection.Get(ctx, src.ConnectionID)
				if e != nil {
					return e
				}
				if data.ConnectionRevision(conn) != src.ConnectionRevision {
					return fmt.Errorf("货源账号配置与首次采购时不同，请恢复原货源账号后核查原单")
				}
				if e := c.ProductDeliverySource.UpdateOneID(src.ID).SetUpstreamOrderID(strings.TrimSpace(req.UpstreamOrderId)).SetStatus("polling").SetNextCheckAt(0).SetLastError("").Exec(ctx); e != nil {
					return e
				}
			default:
				return fmt.Errorf("无效的操作")
			}
			return c.Product.UpdateOneID(p.ID).AddLockVersion(1).Exec(ctx)
		}
		if req.Mode != "upstream" && req.Mode != "local" && req.Mode != "reuse" {
			return fmt.Errorf("无效的发货来源")
		}
		if req.Mode == "upstream" && p.UpstreamSourceID == 0 {
			return fmt.Errorf("该商品没有上游绑定")
		}
		if req.Mode == "local" && p.StockType != "card" {
			return fmt.Errorf("一次性卡密需要卡密库存类型")
		}
		if src != nil && (src.Status == "submitting" || src.Status == "polling" || src.Status == "uncertain") {
			return fmt.Errorf("请先核实正在处理的首次采购，不能更换来源后重复采购")
		}
		if src != nil && src.Status == "empty" {
			n, e := data.SourceAllocations(ctx, c, src.ID, s.repo.data.Dialect.Capabilities().SupportsSkipLocked)
			if e != nil {
				return e
			}
			if n > 0 {
				return fmt.Errorf("已有订单等待首次采购，请先处理这些订单")
			}
		}
		if req.Mode == "reuse" && src != nil && len(src.Content) > 0 && !req.FirstPurchase && req.Content == "" && req.ProcurementId == 0 {
			if req.ExpiresAt > 0 && req.ExpiresAt <= time.Now().Unix() {
				return fmt.Errorf("有效期必须晚于当前时间")
			}
			n, e := data.SourceAllocations(ctx, c, src.ID, s.repo.data.Dialect.Capabilities().SupportsSkipLocked)
			if e != nil {
				return e
			}
			if req.MaxDeliveries > 0 && req.MaxDeliveries < int64(n) {
				return fmt.Errorf("发放次数不能少于已发放及预占数量")
			}
			if e := c.ProductDeliverySource.UpdateOneID(src.ID).SetExpiresAt(req.ExpiresAt).SetMaxDeliveries(req.MaxDeliveries).Exec(ctx); e != nil {
				return e
			}
			return c.Product.UpdateOneID(p.ID).AddLockVersion(1).Exec(ctx)
		}
		if req.Mode == "reuse" {
			if req.ExpiresAt > 0 && req.ExpiresAt <= time.Now().Unix() {
				return fmt.Errorf("有效期必须晚于当前时间")
			}
			options := 0
			if req.Content != "" {
				options++
			}
			if req.ProcurementId > 0 {
				options++
			}
			if req.FirstPurchase {
				options++
			}
			if options != 1 {
				return fmt.Errorf("请选择一种内容来源：填写、历史采购或首次自动采购")
			}
			controls, e := c.ProductControl.Query().Where(productcontrol.ProductID(p.ID)).Count(ctx)
			if e != nil {
				return e
			}
			if controls > 0 {
				return fmt.Errorf("包含客户填写字段的商品不能自动复用，请先确认并移除专属字段")
			}
			create := c.ProductDeliverySource.Create().SetProductID(p.ID).SetSkuID(req.SkuId).SetSubsiteID(p.SubsiteID).SetExpiresAt(req.ExpiresAt).SetMaxDeliveries(req.MaxDeliveries)
			if req.FirstPurchase {
				if p.UpstreamSourceID == 0 || p.UpstreamProductCode == "" {
					return fmt.Errorf("缺少上游商品绑定")
				}
				conn, e := c.SupplyConnection.Get(ctx, p.UpstreamSourceID)
				if e != nil {
					return e
				}
				if conn.Status != "active" {
					return fmt.Errorf("请先启用货源")
				}
				upstreamSKU := ""
				if sk != nil {
					upstreamSKU = sk.UpstreamSkuID
					if upstreamSKU == "" {
						return fmt.Errorf("缺少上游规格绑定")
					}
				}
				create.SetPurchaseKey("reuse:" + uuid.NewString()).SetExchangeRate(conn.ExchangeRate).SetConnectionID(conn.ID).SetConnectionRevision(data.ConnectionRevision(conn)).SetUpstreamProduct(p.UpstreamProductCode).SetUpstreamSku(upstreamSKU).SetStatus("empty")
			} else {
				if s.cipher == nil {
					return fmt.Errorf("内容加密组件未配置")
				}
				plain := req.Content
				if req.ProcurementId > 0 {
					po, e := c.ProcurementOrder.Get(ctx, req.ProcurementId)
					if e != nil {
						return e
					}
					it, e := c.OrderItem.Get(ctx, po.OrderItemID)
					if e != nil {
						return e
					}
					o, e := c.Order.Get(ctx, it.OrderID)
					if e != nil {
						return e
					}
					if po.Status != "fulfilled" || it.SubsiteID != p.SubsiteID || it.ProductID != p.ID || it.SkuID != req.SkuId || it.FulfillmentType != "upstream" || len(it.FormAnswers) > 0 || o.Status == "refund_pending" || o.Status == "refunded" || o.Status == "canceled" || o.Status == "expired" || it.FulfillmentStatus == "refunded" {
						return fmt.Errorf("只能复用本商品同一规格未退款的成功采购内容")
					}
					receipt, e := c.ProcurementItem.Query().Where(procurementitem.ProcurementID(po.ID)).Only(ctx)
					if e != nil {
						return e
					}
					i := int(req.ContentIndex)
					if i < 0 || i >= len(receipt.ReceivedContent) {
						return fmt.Errorf("请选择有效的采购内容")
					}
					ciphertext, e := base64.StdEncoding.DecodeString(receipt.ReceivedContent[i])
					if e != nil {
						return fmt.Errorf("原采购内容不可读取")
					}
					plaintext, e := s.cipher.Open(ciphertext, p.ID, p.SubsiteID)
					if e != nil {
						return fmt.Errorf("原采购内容不可解密")
					}
					plain = string(plaintext)
					create.SetOriginProcurementID(po.ID)
				}
				if strings.TrimSpace(plain) == "" {
					return fmt.Errorf("发货内容不能为空")
				}
				sealed, e := s.cipher.Seal(plain, p.ID, p.SubsiteID)
				if e != nil {
					return e
				}
				create.SetContent(sealed).SetStatus("ready")
			}
			if src != nil {
				if e := c.ProductDeliverySource.UpdateOneID(src.ID).ClearCurrentKey().Exec(ctx); e != nil {
					return e
				}
			}
			if _, e := create.SetCurrentKey(data.DeliverySourceKey(p.SubsiteID, p.ID, req.SkuId)).Save(ctx); e != nil {
				return e
			}
		} else if src != nil {
			if e := c.ProductDeliverySource.UpdateOneID(src.ID).ClearCurrentKey().Exec(ctx); e != nil {
				return e
			}
		}
		mode := req.Mode
		if mode == "upstream" {
			mode = "auto"
		}
		if sk != nil {
			if e := c.ProductSku.UpdateOneID(sk.ID).SetFulfillmentMode(mode).Exec(ctx); e != nil {
				return e
			}
		} else {
			if e := c.Product.UpdateOneID(p.ID).SetFulfillmentMode(mode).Exec(ctx); e != nil {
				return e
			}
		}
		update := c.Product.UpdateOneID(p.ID).AddLockVersion(1)
		if mode == "local" || mode == "reuse" {
			update.SetAutoListing(false).SetListingChangedAt(time.Now().UnixMilli()).SetListingZeroSince(0).SetListingRestocked(false).SetListingMessage("已切换本地发货，自动上下架已暂停")
		}
		return update.Exec(ctx)
	})
	if err != nil {
		return nil, err
	}
	return s.GetDeliverySources(ctx, &adminv1.DeliverySourcesRequest{ProductId: req.ProductId})
}
