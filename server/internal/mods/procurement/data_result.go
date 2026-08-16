package procurement

// P2-02 T3 三通道结果获取（友商标准照搬 + 间隔表）：
//   1. 轮询：指数退避间隔表 [30s×2, 1m×2, 2m×2, 5m×2, 10m]（约 30 分钟）；
//      耗尽不标记失败，移交巡检（cron 30min）
//   2. 巡检：拉 polling/submitted 单查上游；超 24h 卡死 → manual + 告警事件
//   3. 回调：上游主动通知（P2-03 自家协议回调）；三通道汇聚同一
//      confirmResult 入口，状态机 + dedupe 幂等（并发到达同一终态只生效一次）
//
// PollTaskType 任务类型（critical 队列；worker mux 注册）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	fulfillmentport "github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
)

// PollTaskType 轮询任务类型（asynq mux 精确匹配）。
const PollTaskType = "procurement.poll"

// pollIntervals 指数退避间隔表（秒）：30s×2, 1m×2, 2m×2, 5m×2, 10m×1。
var pollIntervals = []int{30, 30, 60, 60, 120, 120, 300, 300, 600}

// PollOne 轮询单张采购单（退避表按 retry_count 取间隔；耗尽移交巡检不标记失败）。
func (s *ProcureService) PollOne(ctx context.Context, poID uint64) error {
	po, err := s.repo.Get(ctx, poID)
	if err != nil {
		return err
	}
	switch string(po.Status) {
	case "fulfilled", "rejected", "refunding", "refunded", "manual":
		return nil // 终态
	case "pending":
		// 提交尚未受理（上游排队中）——按退避继续
	}

	// 三通道汇聚：查询上游 → 结果确认
	info, err := s.gw.Query(ctx, po.ConnectionID, po.UpstreamOrderID)
	if err != nil {
		return s.handlePollError(ctx, po.ID, err)
	}
	return s.confirmResult(ctx, po.ID, info.Status, info.Cards, info.Amount)
}

// confirmResult 三通道统一结果入口（轮询/巡检/回调汇聚）：
// 并发到达同一终态只生效一次（状态机 CAS）。
func (s *ProcureService) confirmResult(ctx context.Context, poID uint64, status string, cards []string, amount int64) error {
	switch status {
	case "delivered":
		po, err := s.repo.Get(ctx, poID)
		if err != nil {
			return err
		}
		// 拿商品/订单信息用于加密与交付（采购项快照足够，交付需要 order_item → order）
		item, err := s.repo.ItemByProcurement(ctx, poID)
		if err != nil {
			return err
		}
		orderItemID := po.OrderItemID
		// 交付出口需要 orderID/productID：order_item → order_id + product_id
		oi, err := s.repo.OrderItemInfo(ctx, orderItemID)
		if err != nil {
			return err
		}
		sealed := make([][]byte, 0, len(cards))
		delivery := make([]fulfillmentport.UpstreamDeliveryItem, 0, len(cards))
		for _, plain := range cards {
			ct, err := s.cipher.Seal(plain, oi.ProductID, oi.SubsiteID)
			if err != nil {
				return fmt.Errorf("procurement: 卡密加密失败: %w", err)
			}
			sealed = append(sealed, ct)
			delivery = append(delivery, fulfillmentport.UpstreamDeliveryItem{SealedContent: ct, ContentHash: s.cipher.ContentHash(plain)})
		}
		if err := s.repo.AttachReceivedContent(ctx, poID, sealed); err != nil {
			return err
		}
		if err := s.repo.MarkFulfilled(ctx, poID); err != nil {
			return err
		}
		_ = item // 采购项快照（sku/成本）留档
		if err := s.attach.AttachUpstreamDelivery(ctx, oi.OrderID, orderItemID, oi.ProductID, delivery); err != nil {
			s.log.Error("procurement.attach_delivery_failed", "po_id", poID, "err", err)
		}
		s.publish(ctx, events.ProcurementFulfilled, poID, map[string]any{
			"procurement_id": poID, "order_item_id": orderItemID, "cards": len(cards), "amount": amount,
		})
		return nil
	case "rejected", "failed":
		return s.handleSubmitError(ctx, poID, errors.New("上游返回终态失败"))
	default:
		// pending：推进轮询档位
		po, err := s.repo.Get(ctx, poID)
		if err != nil {
			return err
		}
		idx := int(po.RetryCount)
		if idx >= len(pollIntervals) {
			// 间隔表耗尽：不标记失败，移交巡检（30min cron 兜底）；
			// 已是 polling 则不再迁移（幂等）
			if string(po.Status) != "polling" {
				if err := s.repo.MarkPolling(ctx, poID); err != nil {
					return err
				}
			}
			s.log.Info("procurement.poll_exhausted_patrol", "po_id", poID)
			return nil
		}
		delay := time.Duration(pollIntervals[idx]) * time.Second
		if err := s.repo.BumpRetry(ctx, poID, time.Now().UTC().Add(delay), "pending"); err != nil {
			return err
		}
		return s.schedulePoll(ctx, poID, delay)
	}
}

// handlePollError 轮询查询失败：可重试退避；永久错误 → rejected 分流。
func (s *ProcureService) handlePollError(ctx context.Context, poID uint64, err error) error {
	if errors.Is(err, supplyport.ErrUpstreamDeleted) || errors.Is(err, supplyport.ErrUpstreamUnavailable) {
		reason := err.Error()
		if merr := s.repo.MarkRejected(ctx, poID, reason); merr != nil {
			return merr
		}
		return s.applyFailStrategy(ctx, poID, reason)
	}
	po, gerr := s.repo.Get(ctx, poID)
	if gerr != nil {
		return gerr
	}
	idx := int(po.RetryCount)
	if idx >= len(pollIntervals) {
		if err := s.repo.MarkPolling(ctx, poID); err != nil {
			return err
		}
		return nil // 移交巡检
	}
	delay := time.Duration(pollIntervals[idx]) * time.Second
	if err := s.repo.BumpRetry(ctx, poID, time.Now().UTC().Add(delay), err.Error()); err != nil {
		return err
	}
	return s.schedulePoll(ctx, poID, delay)
}

// Patrol 巡检：拉 polling/submitted 单逐个查上游；超 24h 卡死 → manual + 事件。
// cron 每 30 分钟注册（bootstrap.NewCron）。
func (s *ProcureService) Patrol(ctx context.Context) {
	rows, err := s.repo.ListPollable(ctx, 100)
	if err != nil {
		s.log.Warn("procurement.patrol_list_failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, po := range rows {
		// 24h 卡死检测（last_poll_at 距今 > 24h）
		if !po.LastPollAt.IsZero() && now.Sub(po.LastPollAt) > 24*time.Hour {
			reason := fmt.Sprintf("采购单 %d 超过 24h 未推进，转人工处理", po.ID)
			if err := s.repo.MarkManual(ctx, po.ID, reason); err != nil {
				s.log.Warn("procurement.patrol_manual_failed", "po_id", po.ID, "err", err)
				continue
			}
			s.publish(ctx, events.ProcurementFailed, po.ID, map[string]any{
				"procurement_id": po.ID, "reason": reason, "strategy": "manual_24h_stale",
			})
			continue
		}
		ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.PollOne(ctx2, po.ID)
		cancel()
		if err != nil {
			s.log.Warn("procurement.patrol_poll_failed", "po_id", po.ID, "err", err)
		}
	}
}

// RunPollTask 队列任务入口（payload {"procurement_id": N}）。
func (s *ProcureService) RunPollTask(ctx context.Context, payload []byte) error {
	var req struct {
		ProcurementID uint64 `json:"procurement_id"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("procurement.poll: 解析任务载荷失败: %w", err)
	}
	if req.ProcurementID == 0 {
		return nil
	}
	return s.PollOne(ctx, req.ProcurementID)
}

