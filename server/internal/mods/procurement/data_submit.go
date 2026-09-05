package procurement

// /：采购提交与到手即加密。
//
// 流程（订阅 order.paid → 逐 upstream 项）：
// 1. 幂等建单（order_item_id 唯一）pending
// 2. fail-open 库存校验（stock_mode=real 且缓存不足时实时查；失败放行）
// 3. Gateway.Submit（幂等键 = 采购单 dedupe_key，随请求发送）
// - delivered → 卡密到手即加密（CardCipher.Seal）→ 落 procurement_items
// → MarkFulfilled → 交付出口（fulfillment.AttachUpstreamDelivery）→ 发布 fulfilled
// - pending → MarkSubmitted（记 upstream_order_id + 退避调度）
// - 永久错误 → MarkRejected → 失败策略分流（auto_refund / manual）
// - 可重试 → BumpRetry（退避后由轮询/巡检再试）
//
// 铁律 11（采购侧）：上游卡密内存态 → 立即 Seal → 密文落库；全程零明文落盘零日志。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	fulfillmentport "github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	paymentport "github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

// orderPaidPayload order.paid 事件载荷（与 order 模块发布结构对齐）。
type orderPaidPayload struct {
	OrderNo   string `json:"order_no"`
	OrderID   uint64 `json:"order_id"`
	SubsiteID uint64 `json:"subsite_id"`
	Items     []struct {
		OrderItemID     uint64 `json:"order_item_id"`
		ProductID       uint64 `json:"product_id"`
		SkuID           uint64 `json:"sku_id"`
		Quantity        int32  `json:"quantity"`
		FulfillmentType string `json:"fulfillment_type"`
	} `json:"items"`
}

// ProcureService 采购服务（ 提交 / 三通道 / 失败策略）。
type ProcureService struct {
	repo   *ProcureRepo
	gw     supplyport.UpstreamGateway
	reader catalogport.ProductReader
	cipher *inventory.CardCipher
	attach fulfillmentport.AttachUpstreamDelivery
	refund paymentport.OrderRefunder
	outbox events.Writer
	enq    queue.Enqueuer
	log    *slog.Logger
}

// NewProcureService 构造。
func NewProcureService(
	repo *ProcureRepo,
	gw supplyport.UpstreamGateway,
	reader catalogport.ProductReader,
	cipher *inventory.CardCipher,
	attach fulfillmentport.AttachUpstreamDelivery,
	refund paymentport.OrderRefunder,
	outbox events.Writer,
	enq queue.Enqueuer,
	log *slog.Logger,
) *ProcureService {
	return &ProcureService{repo: repo, gw: gw, reader: reader, cipher: cipher, attach: attach, refund: refund, outbox: outbox, enq: enq, log: log}
}

// OnOrderPaid 订阅 order.paid（Dispatcher 注册；幂等由 processed_events 兜底）。
func (s *ProcureService) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var payload orderPaidPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("procurement: 解析 order.paid 载荷失败: %w", err)
	}
	for _, it := range payload.Items {
		if it.FulfillmentType != "upstream" {
			continue // 本地卡密项由 fulfillment 模块履约
		}
		if err := s.processItem(ctx, payload, it.OrderItemID, it.ProductID, it.SkuID, it.Quantity); err != nil {
			// 单条失败不阻断其余（错误留痕，重试由轮询/人工兜底）
			s.log.Error("procurement.process_item_failed",
				"order_no", payload.OrderNo, "order_item_id", it.OrderItemID, "err", err)
		}
	}
	return nil
}

// processItem 单上游项采购编排（幂等：已存在采购单直接跳过）。
func (s *ProcureService) processItem(ctx context.Context, payload orderPaidPayload, orderItemID, productID, skuID uint64, quantity int32) error {
	// 幂等：该订单项已建采购单（重复投递 / 手动重试）
	if _, err := s.repo.GetByOrderItem(ctx, orderItemID); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}

	// 商品上游映射（upstream_source_id + product_code 判定）
	p, err := s.reader.Get(ctx, payload.SubsiteID, productID)
	if err != nil {
		return fmt.Errorf("procurement: 商品不可读: %w", err)
	}
	if p.UpstreamSourceID == 0 || p.UpstreamProductCode == "" {
		s.log.Warn("procurement.skip_non_upstream", "order_item_id", orderItemID, "product_id", productID)
		return nil // 防御：非上游项（本地项误入）
	}

	// 建单（pending；dedupe_key = order_item:N；失败策略取渠道级配置，默认自动退款）
	failStrategy := "auto_refund"
	if s.gw != nil {
		if fs := s.gw.FailStrategyOf(ctx, p.UpstreamSourceID); fs != "" {
			failStrategy = fs
		}
	}
	po, err := s.repo.CreatePending(ctx, orderItemID, p.UpstreamSourceID, p.UpstreamProductCode, quantity, failStrategy, payload.OrderNo)
	if err != nil {
		if errors.Is(err, ErrDuplicatePurchase) {
			return nil // 并发已建
		}
		return err
	}

	traceID := fmt.Sprintf("po-%d-%s", po.ID, payload.OrderNo)
	_ = s.repo.EnsureTraceID(ctx, po.ID, traceID)

	// 规格选择：客户所选本地 SKU 的上游标识（acg=race|k=v 编码 / dujiao=sku_id）
	upstreamSKU := ""
	if skuID > 0 {
		upstreamSKU = s.reader.SkuUpstreamCode(ctx, payload.SubsiteID, skuID)
	}

	// 提交
	res, err := s.gw.Submit(ctx, supplyport.PurchaseRequest{
		ConnectionID:      p.UpstreamSourceID,
		ProductCode:       p.UpstreamProductCode,
		UpstreamSKU:       upstreamSKU,
		Quantity:          int(quantity),
		DownstreamOrderNo: fmt.Sprintf("order_item:%d", orderItemID),
		TraceID:           traceID,
	})
	if err != nil {
		return s.handleSubmitError(ctx, po.ID, err)
	}

	switch res.Status {
	case "delivered":
		return s.finalizeDelivered(ctx, po.ID, payload.OrderID, orderItemID, productID, payload.SubsiteID, res.Cards, res.Amount)
	case "pending", "submitted", "paid", "accepted", "processing":
		// dujiao 等上游：钱包受理即 paid（异步交付）——轮询/回调推进
		// 受理：记 upstream_order_id + 退避（默认间隔表首档 30s）
		next := time.Now().UTC().Add(30 * time.Second)
		if err := s.repo.MarkSubmitted(ctx, po.ID, res.UpstreamOrderID, next); err != nil {
			return err
		}
		s.log.Info("procurement.submitted", "id", po.ID, "upstream_order_id", res.UpstreamOrderID)
		return s.schedulePoll(ctx, po.ID, 30*time.Second)
	default:
		return fmt.Errorf("procurement: 未知上游状态 %q", res.Status)
	}
}

// finalizeDelivered 同步拿货成功：到手即加密 → 落库 → 交付出口 → 事件。
func (s *ProcureService) finalizeDelivered(ctx context.Context, poID, orderID, orderItemID, productID, subsiteID uint64, cards []string, amount int64) error {
	// 到手即加密：内存明文 → Seal（AAD 绑定本地商品/租户）→ 密文
	sealed := make([][]byte, 0, len(cards))
	deliveryItems := make([]fulfillmentport.UpstreamDeliveryItem, 0, len(cards))
	for _, plain := range cards {
		ct, err := s.cipher.Seal(plain, productID, subsiteID)
		if err != nil {
			return fmt.Errorf("procurement: 卡密加密失败: %w", err)
		}
		sealed = append(sealed, ct)
		deliveryItems = append(deliveryItems, fulfillmentport.UpstreamDeliveryItem{
			SealedContent: ct,
			ContentHash:   s.cipher.ContentHash(plain),
		})
	}
	if err := s.repo.AttachReceivedContent(ctx, poID, sealed); err != nil {
		return err
	}
	if err := s.repo.MarkFulfilled(ctx, poID); err != nil {
		return err
	}
	// 交付出口（写 cards + order_deliveries；幂等）
	if err := s.attach.AttachUpstreamDelivery(ctx, orderID, orderItemID, productID, deliveryItems); err != nil {
		s.log.Error("procurement.attach_delivery_failed", "po_id", poID, "err", err)
	}
	s.publish(ctx, events.ProcurementFulfilled, poID, map[string]any{
		"procurement_id": poID, "order_item_id": orderItemID, "cards": len(cards), "amount": amount,
	})
	return nil
}

// handleSubmitError 提交失败分流：永久错误 → rejected → 失败策略；其余 → 退避重试。
func (s *ProcureService) handleSubmitError(ctx context.Context, poID uint64, err error) error {
	// 防重键冲突（acg request_no 重复即报错）：上游可能已受理首请求（响应丢失
	// 场景），重试永远撞墙、自动退款可能造成上游已成交却退客户款 → 立即转人工核对
	if errors.Is(err, supplyport.ErrUpstreamDuplicate) {
		reason := "上游防重键冲突（同键请求已被受理过，可能已成交）——需人工核对上游订单后处置"
		if merr := s.repo.MarkManual(ctx, poID, reason); merr != nil {
			return merr
		}
		s.log.Warn("procurement.duplicate_submit_manual", "id", poID)
		s.publish(ctx, events.ProcurementFailed, poID, map[string]any{
			"procurement_id": poID, "reason": reason, "strategy": "manual_duplicate_submit",
		})
		return nil
	}
	permanent := errors.Is(err, supplyport.ErrUpstreamDeleted) ||
		errors.Is(err, supplyport.ErrUpstreamUnavailable) ||
		errors.Is(err, supplyport.ErrUpstreamBalance) ||
		// 上游明确无库存：永久拒绝（否则空 upstream_order_id 进轮询死循环到 24h
		// 才转人工；即刻走失败策略让付款顾客马上退款，符合 fail-open 补偿语义）
		errors.Is(err, supplyport.ErrUpstreamNoStock)
	if permanent {
		reason := err.Error()
		if merr := s.repo.MarkRejected(ctx, poID, reason); merr != nil {
			return merr
		}
		s.log.Warn("procurement.rejected", "id", poID, "reason", reason)
		return s.applyFailStrategy(ctx, poID, reason)
	}
	// 可重试（网络/上游抖动）：退避后移交轮询
	s.log.Warn("procurement.submit_retryable", "id", poID, "err", err)
	if err := s.repo.BumpRetry(ctx, poID, time.Now().UTC().Add(30*time.Second), err.Error()); err != nil {
		return err
	}
	return s.schedulePoll(ctx, poID, 30*time.Second)
}

// applyFailStrategy 失败策略：auto_refund → 退款编排；manual → 人工终态 + 事件。
func (s *ProcureService) applyFailStrategy(ctx context.Context, poID uint64, reason string) error {
	po, err := s.repo.Get(ctx, poID)
	if err != nil {
		return err
	}
	if string(po.FailStrategy) == "manual" {
		if err := s.repo.MarkManual(ctx, poID, reason); err != nil {
			return err
		}
		s.publish(ctx, events.ProcurementFailed, poID, map[string]any{
			"procurement_id": poID, "reason": reason, "strategy": "manual",
		})
		return nil
	}
	// auto_refund：创建本地退款单（channel=upstream）→ 驱动订单退款流转
	if err := s.repo.MarkRefunding(ctx, poID); err != nil {
		return err
	}
	if s.refund != nil {
		// 金额 0 = 按订单实付全额退款（payment 内部核算）
		orderID, oerr := s.repo.OrderIDOf(ctx, poID)
		if oerr != nil {
			s.log.Error("procurement.order_resolve_failed", "po_id", poID, "err", oerr)
		} else if err := s.refund.RefundOrder(ctx, orderID, 0, "上游采购失败自动退款: "+reason); err != nil {
			s.log.Error("procurement.auto_refund_failed", "po_id", poID, "order_id", orderID, "err", err)
			_ = s.repo.MarkManual(ctx, poID, "自动退款失败转人工: "+err.Error())
			s.publish(ctx, events.ProcurementFailed, poID, map[string]any{
				"procurement_id": poID, "reason": reason, "strategy": "auto_refund_failed_manual",
			})
			return nil
		}
	}
	s.publish(ctx, events.ProcurementFailed, poID, map[string]any{
		"procurement_id": poID, "reason": reason, "strategy": "auto_refund",
	})
	return nil
}

// schedulePoll 退避调度：入队轮询任务（critical 队列；降级模式进程内执行）。
func (s *ProcureService) schedulePoll(ctx context.Context, poID uint64, delay time.Duration) error {
	payload, _ := json.Marshal(map[string]uint64{"procurement_id": poID})
	if s.enq == nil || !s.enq.Enabled() {
		// 降级：直接异步轮询
		go func() {
			time.Sleep(delay)
			runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			if err := s.PollOne(runCtx, poID); err != nil {
				s.log.Warn("procurement.poll_failed", "po_id", poID, "err", err)
			}
		}()
		return nil
	}
	return s.enq.Enqueue(ctx, queue.Task{
		Type:      PollTaskType,
		Payload:   payload,
		Queue:     queue.QueueCritical,
		DedupeKey: PollTaskType + ":" + fmt.Sprint(poID),
	})
}

// publish 发布采购事件（procurement.fulfilled / procurement.failed）。
func (s *ProcureService) publish(ctx context.Context, typ string, poID uint64, payload map[string]any) {
	if s.outbox == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	agg := fmt.Sprintf("proc:%d", poID)
	_ = s.outbox.Write(ctx, "procurement", typ, agg, agg, raw)
}
