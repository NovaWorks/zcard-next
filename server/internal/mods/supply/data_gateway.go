package supply

// 采购网关实现（P2-02 消费方端口）：连接凭据解密 → 适配器装配 → 提交/查询/退款。
// fail-open 语义（T4）：CheckStock 查询失败返回 -1（放行），由 procurement 决定处理。

import (
	"context"
	"encoding/json"
	"errors"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// Gateway supply 采购网关（实现 supplyport.UpstreamGateway）。
type Gateway struct {
	repo *SupplyRepoImpl
}

// NewGateway 构造。
func NewGateway(repo *SupplyRepoImpl) *Gateway { return &Gateway{repo: repo} }

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

// Submit 提交采购。永久错误归一化（哨兵 → procurement 状态机判 rejected）。
func (g *Gateway) Submit(ctx context.Context, req supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
	a, err := g.adapterFor(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	res, err := a.CreateOrder(ctx, adapter.CreateOrderReq{
		ProductCode:       req.ProductCode,
		Quantity:          req.Quantity,
		DownstreamOrderNo: req.DownstreamOrderNo,
		TraceID:           req.TraceID,
		CallbackURL:       req.CallbackURL,
	})
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
	a, err := g.adapterFor(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	detail, err := a.GetOrder(ctx, upstreamOrderID)
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
