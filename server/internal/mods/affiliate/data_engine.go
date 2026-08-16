package affiliate

// 佣金引擎（P3-03 T2/T3/T4）：
//   订阅 order.paid → 归因快照判定 → 三级计算入账（pending_confirm + available_at）
//   cron 到期确认 → available + wallet 入账（幂等键 commission:<id>）
//   order.refunded → 逆向：pending 作废 / available 扣回 / 已提现不足 → 负债行
//
// 不发佣清单（1.x 继承）：自购（buyer∈三级链）、无归因（链空）、supply_orders
// （独立表非 orders——天然排除）、分站自购（profit_eligible M3 reseller 联动）。
// 冻结期在佣金表自身状态机，不占用 wallet.locked（口径 §4.6）。

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

// AffiliateConfig settings.affiliate 配置（运行时读取，默认值兜底）。
type AffiliateConfig struct {
	RateL1     int32  `json:"rate_l1"` // 万分比（默认 500 = 5%）
	RateL2     int32  `json:"rate_l2"` // 默认 200 = 2%
	RateL3     int32  `json:"rate_l3"` // 默认 100 = 1%
	BaseScope  string `json:"base_scope"` // amount | profit（默认 amount）
	FreezeDays int    `json:"freeze_days"` // 冻结天数（默认 7）
	SelfBuy    bool   `json:"self_buy"`   // 自购发佣开关（默认 false 不发）
	Enabled    bool   `json:"enabled"`
}

func defaultConfig() AffiliateConfig {
	return AffiliateConfig{
		RateL1: 500, RateL2: 200, RateL3: 100,
		BaseScope: "amount", FreezeDays: 7, SelfBuy: false, Enabled: true,
	}
}

// AffiliateService 佣金引擎。
type AffiliateService struct {
	repo     *CommissionRepo
	wallet   walletport.Wallet
	settings notifyport.SettingsReader
	outbox   events.Writer
	log      *slog.Logger
}



// NewAffiliateService 构造。
func NewAffiliateService(repo *CommissionRepo, wallet walletport.Wallet, settings notifyport.SettingsReader, outbox events.Writer, logger *slog.Logger) *AffiliateService {
	return &AffiliateService{repo: repo, wallet: wallet, settings: settings, outbox: outbox, log: logger}
}

// paidPayload order.paid 载荷（与 order 模块发布结构对齐 + 归因链）。
type paidPayload struct {
	OrderNo   string `json:"order_no"`
	OrderID   uint64 `json:"order_id"`
	SubsiteID uint64 `json:"subsite_id"`
	BuyerID   uint64 `json:"user_id"` // 事件载荷统一字段名
	InviteL1  uint64 `json:"invite_l1"`
	InviteL2  uint64 `json:"invite_l2"`
	InviteL3  uint64 `json:"invite_l3"`
	TotalCents int64 `json:"total_cents"`
	ProfitCents int64 `json:"profit_cents"` // 毛利口径（amount−cost；order 侧随事件附赠）
}

// OnOrderPaid 订阅 order.paid（幂等：UNIQUE(order_id,tier) 兜底 + processed_events）。
func (s *AffiliateService) OnOrderPaid(ctx context.Context, env events.Envelope) error {
	var p paidPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return nil // 载荷不合法：ACK 不重试（order 侧契约破坏属异常路径）
	}
	cfg := s.config(ctx)
	if !cfg.Enabled {
		return nil
	}
	// 归因链（快照已随事件携带；空链 = 无归因不发）
	chain := [3]uint64{p.InviteL1, p.InviteL2, p.InviteL3}
	if chain[0] == 0 {
		return nil
	}
	// 不发佣：自购（买家在三级链内且开关关闭）
	if !cfg.SelfBuy && p.BuyerID != 0 {
		for _, uid := range chain {
			if uid == p.BuyerID {
				return nil
			}
		}
	}
	// 基数口径
	base := p.TotalCents
	if cfg.BaseScope == "profit" {
		base = p.ProfitCents
		if base <= 0 {
			return nil // 无毛利不发佣
		}
	}
	rates := [3]int32{cfg.RateL1, cfg.RateL2, cfg.RateL3}
	availableAt := time.Now().UTC().AddDate(0, 0, cfg.FreezeDays)
	for tier := 0; tier < 3; tier++ {
		referrer := chain[tier]
		if referrer == 0 {
			continue
		}
		amount := base * int64(rates[tier]) / 10000
		if amount <= 0 {
			continue
		}
		if err := s.repo.Insert(ctx, CommissionRow{
			OrderID: p.OrderID, BuyerID: p.BuyerID, ReferrerID: referrer,
			Tier: int8(tier + 1), Rate: rates[tier],
			BaseAmount: base, Amount: amount, AvailableAt: availableAt,
		}); err != nil {
			if errors.Is(err, ErrDuplicate) {
				continue // 幂等 ACK
			}
			s.log.Warn("affiliate.insert_failed", "order_id", p.OrderID, "tier", tier+1, "err", err)
		}
	}
	return nil
}

// config 运行时读配置（settings.affiliate 组扁平键——与设置目录键名一致；
// 逐项读取，单项缺失/非法回退默认值）。
func (s *AffiliateService) config(ctx context.Context) AffiliateConfig {
	cfg := defaultConfig()
	if s.settings == nil {
		return cfg
	}
	get := func(key string, out any) bool {
		raw, err := s.settings.GetJSON(ctx, "affiliate", key)
		if err != nil || len(raw) == 0 {
			return false
		}
		return json.Unmarshal(raw, out) == nil
	}
	get("enabled", &cfg.Enabled)
	get("self_buy", &cfg.SelfBuy)
	// 费率（>0 才覆盖；0 视为缺省）
	var v int
	if get("rate_l1", &v) && v > 0 {
		cfg.RateL1 = int32(v)
	}
	if get("rate_l2", &v) && v > 0 {
		cfg.RateL2 = int32(v)
	}
	if get("rate_l3", &v) && v > 0 {
		cfg.RateL3 = int32(v)
	}
	// 基数口径（amount | profit；映射目录 base 键）
	var base string
	if get("base", &base) && (base == "amount" || base == "profit") {
		cfg.BaseScope = base
	}
	// 冻结天数（confirm_days；>=0 均合法，0 = 立即到期）
	var days int
	if get("confirm_days", &days) && days >= 0 {
		cfg.FreezeDays = days
	}
	return cfg
}

// ConfirmDue 到期确认（cron 每小时；负债行重试扣款）。
// 正佣金 → wallet 入账（幂等键 commission:<id>）；负负债行 → 尝试扣款（成功标记 available=已抵扣）。
func (s *AffiliateService) ConfirmDue(ctx context.Context) {
	rows, err := s.repo.ListDueConfirm(ctx, time.Now().UTC(), 200)
	if err != nil {
		s.log.Warn("affiliate.confirm_list_failed", "err", err)
		return
	}
	for _, c := range rows {
		if c.Amount >= 0 {
			// 正佣金：wallet available 入账
			if err := s.wallet.CreditInTx(ctx, walletport.Entry{
				UserID: c.ReferrerID, Direction: "in", Type: "commission",
				Amount: centsOf(c.Amount), Reference: refKey(c.ID),
				Remark: "佣金到期确认",
			}); err != nil {
				s.log.Warn("affiliate.credit_failed", "id", c.ID, "err", err)
				continue
			}
			_ = s.repo.MarkAvailable(ctx, c.ID)
		} else {
			// 负债行：尝试扣回（余额不足保持 pending——后续佣金入账后 cron 重试成功）
			if err := s.wallet.DebitInTx(ctx, walletport.Entry{
				UserID: c.ReferrerID, Direction: "out", Type: "commission_debt",
				Amount: centsOf(-c.Amount), Reference: debtRefKey(c.ID),
				Remark: "佣金负债抵扣",
			}); err != nil {
				continue // 余额不足：留待下轮（负债态抵扣后续佣金语义）
			}
			_ = s.repo.MarkAvailable(ctx, c.ID) // 负债已清
		}
	}
}

// OnOrderRefunded 订阅 order.refunded（逆向扣回）。
func (s *AffiliateService) OnOrderRefunded(ctx context.Context, env events.Envelope) error {
	var p struct {
		OrderID    uint64 `json:"order_id"`
		RefundRatio int64 `json:"refund_ratio"` // 万分比（部分退款；0 视为全额）
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return nil
	}
	ratio := p.RefundRatio
	if ratio <= 0 {
		ratio = 10000 // 全额
	}
	rows, err := s.repo.ListByOrder(ctx, p.OrderID)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if string(c.Status) == "reversed" {
			continue // 幂等
		}
		clawback := c.Amount * ratio / 10000
		if clawback <= 0 {
			continue
		}
		switch string(c.Status) {
		case "pending_confirm":
			// 未入账：作废（按比例作废整行——简化口径：任何比例退款全额作废未确认行）
			_ = s.repo.MarkReversed(ctx, c.ID)
		case "available", "withdrawn":
			// 已入 wallet：扣回；不足 → 负债行（后续佣金抵扣）
			if err := s.wallet.DebitInTx(ctx, walletport.Entry{
				UserID: c.ReferrerID, Direction: "out", Type: "commission_reversal",
				Amount: centsOf(clawback), Reference: refKey(c.ID) + ":reversal",
				Remark: "退款佣金扣回",
			}); err != nil {
				_ = s.repo.InsertDebt(ctx, p.OrderID, c.ReferrerID, c.Tier, clawback)
			}
			_ = s.repo.MarkReversed(ctx, c.ID)
		}
	}
	return nil
}

func centsOf(v int64) money.Cents { return money.Cents(v) }

// PublishConfirmed 到账通知事件（notify 消费；轻量直发）。
func (s *AffiliateService) PublishConfirmed(ctx context.Context, c *ent.AffiliateCommission) {
	if s.outbox == nil {
		return
	}
	raw, _ := json.Marshal(map[string]any{
		"user_id": c.ReferrerID, "amount": c.Amount, "commission_id": c.ID,
	})
	_ = s.outbox.Write(ctx, "affiliate", "affiliate.commission.confirmed",
		refKey(c.ID), refKey(c.ID)+":confirmed", raw)
}
