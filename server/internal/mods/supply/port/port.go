// Package port 为 supply 模块对外契约（零依赖包）。
package port

import "context"

// PurchaseRequest 采购提交请求（P2-02 procurement 消费；DownstreamOrderNo 幂等键）。
type PurchaseRequest struct {
	ConnectionID      uint64
	ProductCode       string // 上游商品标识（映射后的上游键）
	Quantity          int
	DownstreamOrderNo string // 幂等键（防重复下单）
	TraceID           string
	CallbackURL       string // 空 = 轮询/巡检兜底
}

// PurchaseResult 采购提交结果。
type PurchaseResult struct {
	UpstreamOrderID string // 查单依据
	Status          string // delivered | pending
	Amount          int64  // 分
	Cards           []string // delivered 时上游卡密（内存明文，调用方必须立即加密）
}

// PurchaseOrderInfo 上游订单查询结果。
type PurchaseOrderInfo struct {
	Status string
	Amount int64
	Cards  []string // delivered 时卡密（内存明文）
}

// UpstreamGateway 上游采购网关（procurement 模块消费，通道 A）。
// 实现位于 supply 模块（连接凭据解密 + 适配器装配 + fail-open 库存兜底）。
type UpstreamGateway interface {
	// Submit 提交采购（幂等键随请求；永久错误归一化为哨兵错误语义，见实现）。
	Submit(ctx context.Context, req PurchaseRequest) (*PurchaseResult, error)
	// Query 查询上游订单（三通道结果汇聚共用）。
	Query(ctx context.Context, connectionID uint64, upstreamOrderID string) (*PurchaseOrderInfo, error)
	// CheckStock 实时库存校验（T4 fail-open：缓存不足时调用；查询失败放行语义由调用方决定）。
	CheckStock(ctx context.Context, connectionID uint64, productCode string) (int32, error)
	// Refund 向上游传导退款（可选能力；不支持返回 ErrRefundNotSupported）。
	Refund(ctx context.Context, connectionID uint64, upstreamOrderID string) error
}

// 哨兵错误（适配器层归一化，procurement 状态机判据）。
var (
	// ErrUpstreamDeleted 上游商品已删除（永久错误 → rejected）。
	ErrUpstreamDeleted = errorsNew("supply: upstream product deleted")
	// ErrUpstreamUnavailable 上游商品下架（永久错误 → rejected）。
	ErrUpstreamUnavailable = errorsNew("supply: upstream product unavailable")
	// ErrUpstreamBalance 上游余额不足（永久错误 → rejected）。
	ErrUpstreamBalance = errorsNew("supply: upstream balance insufficient")
	// ErrUpstreamNoStock 上游无库存（快速拒绝）。
	ErrUpstreamNoStock = errorsNew("supply: upstream no stock")
	// ErrRefundNotSupported 上游不支持退款。
	ErrRefundNotSupported = errorsNew("supply: refund not supported by upstream")
)

func errorsNew(s string) error { return &sentinelError{s} }

type sentinelError struct{ s string }

func (e *sentinelError) Error() string { return e.s }
