package supply

// 采购网关实现（P2-02 消费方端口）：连接凭据解密 → 适配器装配 → 提交/查询/退款。
// fail-open 语义（T4）：CheckStock 查询失败返回 -1（放行），由 procurement 决定处理。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dashboardport "github.com/NovaWorks/zcard-next/server/internal/mods/dashboard/port"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

// ErrCooldownActive 渠道熔断冷却中（可重试口径：procurement 退避轮询自然吸收）。
var ErrCooldownActive = errors.New("supply: rate limit cooldown active")

// Gateway supply 采购网关（实现 supplyport.UpstreamGateway）。
// P2-10 S2：出站共享自适应节奏器——熔断冷却中直接拒绝（可重试口径），
// 限流信号反馈降速、成功反馈回升（采购请求量小，主要防持续撞墙）。
type Gateway struct {
	repo  *SupplyRepoImpl
	pacer *Pacer // nil = 不节流（测试）
}

// NewGateway 构造。
func NewGateway(repo *SupplyRepoImpl, pacer *Pacer) *Gateway { return &Gateway{repo: repo, pacer: pacer} }

// FailStrategyOf 渠道级失败策略（port 实现；读连接 settings.failure_action）。
func (g *Gateway) FailStrategyOf(ctx context.Context, connectionID uint64) string {
	conn, err := g.repo.GetConnection(ctx, connectionID)
	if err != nil {
		return "auto_refund"
	}
	if v, _ := conn.Settings["failure_action"].(string); v == "manual" {
		return "manual"
	}
	return "auto_refund"
}

// gate 出站前置：熔断冷却判定（冷却中返回可重试错误——procurement 侧退避轮询）。
func (g *Gateway) gate(ctx context.Context, connectionID uint64) (*ent.SupplyConnection, error) {
	conn, err := g.repo.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if g.pacer != nil && g.pacer.CooldownActive(conn) {
		return nil, fmt.Errorf("supply: 渠道熔断冷却中（上游限流），采购稍后自动重试: %w", ErrCooldownActive)
	}
	return conn, nil
}

// feedback 出站结果反馈节奏器（err 分类：限流降速 / 成功回升）。
func (g *Gateway) feedback(ctx context.Context, conn *ent.SupplyConnection, err error) {
	if g.pacer == nil {
		return
	}
	if err != nil && errors.Is(err, adapter.ErrRateLimited) {
		g.pacer.OnRateLimited(ctx, conn, err.Error())
		return
	}
	if err == nil {
		g.pacer.OnSuccess(ctx, conn)
	}
}

// adapterFor 按连接构造适配器（凭据解密失败 → 明确错误）。
func (g *Gateway) adapterFor(ctx context.Context, connectionID uint64) (adapter.Adapter, error) {
	conn, err := g.repo.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	credsJSON, err := g.repo.OpenCredentials(conn)
	if err != nil {
		return nil, err
	}
	var creds adapter.Credentials
	if err := json.Unmarshal([]byte(credsJSON), &creds); err != nil {
		return nil, errors.New("supply: 凭据结构不合法（请重新配置连接）")
	}
	return adapter.New(conn.Driver, conn.BaseURL, creds, parseRetryIntervals(conn.RetryIntervals))
}

// ListOrders 上游订单列表（P3-07 对账数据源；dashboard port 消费——类型转换收口在此）。
// 协议不支持 → ErrUpstreamListUnsupported（对账 job failed 可查）。
func (g *Gateway) ListOrders(ctx context.Context, connectionID uint64, start, end time.Time) ([]dashboardport.UpstreamOrder, error) {
	a, err := g.adapterFor(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	lister, ok := a.(adapter.OrderLister)
	if !ok {
		return nil, dashboardport.ErrUpstreamListUnsupported
	}
	rows, err := lister.ListOrders(ctx, start, end)
	if errors.Is(err, adapter.ErrNotSupported) {
		return nil, dashboardport.ErrUpstreamListUnsupported
	}
	if err != nil {
		return nil, err
	}
	out := make([]dashboardport.UpstreamOrder, 0, len(rows))
	for _, r := range rows {
		out = append(out, dashboardport.UpstreamOrder{
			UpstreamOrderID: r.UpstreamOrderID, Amount: r.Amount, Status: r.Status,
		})
	}
	return out, nil
}

// Submit 提交采购。永久错误归一化（哨兵 → procurement 状态机判 rejected）。
func (g *Gateway) Submit(ctx context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
	conn, err := g.gate(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	a, err := g.adapterFor(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	res, err := a.CreateOrder(ctx, adapter.CreateOrderReq{
		ProductCode:       req.ProductCode,
		Quantity:          req.Quantity,
		DownstreamOrderNo: req.DownstreamOrderNo,
		TraceID:           req.TraceID,
		CallbackURL:       g.submitCallbackURL(ctx, req.ConnectionID, req.CallbackURL),
	})
	g.feedback(ctx, conn, err)
	if err != nil {
		switch {
		case errors.Is(err, adapter.ErrProductDeleted):
			return nil, supplyport.ErrUpstreamDeleted
		case errors.Is(err, adapter.ErrProductUnavailable):
			return nil, supplyport.ErrUpstreamUnavailable
		case errors.Is(err, adapter.ErrInsufficientBalance):
			return nil, supplyport.ErrUpstreamBalance
		case errors.Is(err, adapter.ErrNoStock):
			return nil, supplyport.ErrUpstreamNoStock
		}
		return nil, err
	}
	return &supplyport.PurchaseResult{
		UpstreamOrderID: res.UpstreamOrderID,
		Status:          res.Status,
		Amount:          res.Amount,
		Cards:           res.Cards,
	}, nil
}

// Query 查询上游订单。
func (g *Gateway) Query(ctx context.Context, connectionID uint64, upstreamOrderID string) (*supplyport.PurchaseOrderInfo, error) {
	conn, err := g.gate(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	a, err := g.adapterFor(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	detail, err := a.GetOrder(ctx, upstreamOrderID)
	g.feedback(ctx, conn, err)
	if err != nil {
		return nil, err
	}
	return &supplyport.PurchaseOrderInfo{
		Status: detail.Status,
		Amount: detail.Amount,
		Cards:  detail.Cards,
	}, nil
}

// CheckStock 实时库存（fail-open：查询失败返回 -1 放行，日志留痕由调用方）。
func (g *Gateway) CheckStock(ctx context.Context, connectionID uint64, productCode string) (int32, error) {
	a, err := g.adapterFor(ctx, connectionID)
	if err != nil {
		return -1, nil // fail-open：凭据/连接问题不阻断下单（转采购环节处理）
	}
	stock, err := a.GetStock(ctx, productCode, "")
	if err != nil {
		return -1, nil // fail-open：上游抖动放行
	}
	return stock, nil
}

// Refund 向上游传导退款（acg_faka 等不支持 → ErrRefundNotSupported）。
func (g *Gateway) Refund(ctx context.Context, connectionID uint64, upstreamOrderID string) error {
	a, err := g.adapterFor(ctx, connectionID)
	if err != nil {
		return err
	}
	err = a.RefundOrder(ctx, upstreamOrderID)
	if errors.Is(err, adapter.ErrNotSupported) {
		return supplyport.ErrRefundNotSupported
	}
	return err
}

// submitCallbackURL 回调地址决策：请求显式指定 > 连接配置 callback_url（运营在
// 渠道管理里登记的本站公网回调地址；P2-10 E 提交时自动携带给上游）。
func (g *Gateway) submitCallbackURL(ctx context.Context, connectionID uint64, explicit string) string {
	if explicit != "" {
		return explicit
	}
	conn, err := g.repo.GetConnection(ctx, connectionID)
	if err != nil {
		return ""
	}
	return conn.CallbackURL
}
