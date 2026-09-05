package memberlevel

// 积分产生：订阅 order.paid → 按用户当前等级 points_rule（消费 X 分产 Y 分）
// 入积分账本（wallet.Points 端口，通道 A）；幂等键 points:<orderID>（重复投递直接 ACK）。
// 口径：积分兑换单 total=0 不产生；取消/未支付订单不触发（事件只在 paid 后发布）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
)

// orderPaidPointsPayload order.paid 载荷（与 order 模块发布结构对齐——只取所需字段）。
type orderPaidPointsPayload struct {
	OrderID    uint64 `json:"order_id"`
	UserID     uint64 `json:"user_id"`
	TotalCents int64  `json:"total_cents"`
}

// PointsService 积分产生服务（Dispatcher 注册）。
type PointsService struct {
	repo   *MemberLevelRepoImpl
	points walletport.Points
	log    *slog.Logger
}

// NewPointsService 构造。
func NewPointsService(repo *MemberLevelRepoImpl, points walletport.Points, log *slog.Logger) *PointsService {
	return &PointsService{repo: repo, points: points, log: log}
}

// OnOrderPaid 订阅 order.paid：等级 points_rule → 积分入账（幂等）。
func (s *PointsService) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var payload orderPaidPointsPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		// 载荷损坏不重投（人工排查）；口径与 procurement 一致
		s.log.Error("memberlevel.points_payload_invalid", "err", err)
		return nil
	}
	if payload.UserID == 0 || payload.TotalCents <= 0 {
		return nil // 游客/积分兑换单（total=0）不产生积分
	}
	lv, err := s.repo.EffectiveLevelOf(ctx, payload.UserID)
	if err != nil || lv == nil {
		return err
	}
	spend, per := PointsRuleOf(lv)
	if spend <= 0 || per <= 0 {
		return nil // 当前等级未配置积分规则
	}
	earned := payload.TotalCents / spend * per
	if earned <= 0 {
		return nil
	}
	err = s.points.PointCreditInTx(ctx, walletport.PointEntry{
		UserID:    payload.UserID,
		Direction: "in",
		Type:      "earn_consume",
		Amount:    earned,
		Reference: fmt.Sprintf("points:%d", payload.OrderID),
		OrderID:   payload.OrderID,
		Remark:    "消费产生积分",
	})
	if err != nil {
		s.log.Error("memberlevel.points_credit_failed", "order_id", payload.OrderID, "err", err)
	}
	return nil // 失败不重投整批（幂等键可人工补发；告警走日志）
}
