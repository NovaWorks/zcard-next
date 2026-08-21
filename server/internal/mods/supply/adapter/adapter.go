// Package adapter 货源协议适配器（P2-01 T2）。
//
// 三协议：zcard（自家 Supply v2，4 头 HMAC）/ dujiao_next（3 头 HMAC）/
// acg_faka（body 内 MD5 签名）。协议知识迁移自 1.x app/Supply/Drivers/CLAUDE.md
// 与 dujiao-next internal/upstream（signer 签名串、IncludesInactive 回声字段）。
//
// 纪律：
//   - DTO 统一输出**分**（金额一律 int64，铁律 1）；
//   - 出站 100% 经 platform/httpx（SSRF 防护），架构测试断言本包不得直接
//     import net/http 构造客户端；
//   - 凭据与签名串永不进日志（httpx.RedactURL + 本包日志纪律）。
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// 哨兵错误：适配器层把上游协议差异归一化为统一语义（T3 同步/T4 库存判据）。
var (
	// ErrProductDeleted 上游商品已删除（不存在）。同步时本地应下架。
	ErrProductDeleted = errors.New("adapter: upstream product deleted")
	// ErrProductUnavailable 上游商品已下架（存在但不可购）。同步时本地应隐藏。
	ErrProductUnavailable = errors.New("adapter: upstream product unavailable")
	// ErrInsufficientBalance 上游余额不足（采购提交时判为永久错误 → rejected）。
	ErrInsufficientBalance = errors.New("adapter: upstream balance insufficient")
	// ErrNoStock 上游明确无库存（fail-open 语义下快速拒绝）。
	ErrNoStock = errors.New("adapter: upstream no stock")
	// ErrRateLimited 上游限流或疑似 WAF 拦截（429 / 200 但非 JSON）——
	// 自适应节奏器的降速信号（P2-10 S2；AIMD 判据）。
	ErrRateLimited = errors.New("adapter: upstream rate limited")
	// ErrNotSupported 上游协议不支持该能力（如 acg-faka 无退款）。
	ErrNotSupported = errors.New("adapter: capability not supported by upstream")
)

// Credentials 解密后的上游凭据（内存明文态；落库前必须经 crypto.Box.Seal）。
type Credentials struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	AppID     string `json:"app_id"`
	AppKey    string `json:"app_key"`
}

// PingResult 连通性探测结果。
type PingResult struct {
	SiteName        string // 上游站点名
	ProtocolVersion string // 上游协议版本
	Balance         int64  // 上游余额（分，-1=未知）
	Currency        string
}

// Category 上游分类。
type Category struct {
	ID       string
	Name     string
	ParentID string // 空 = 顶层
}

// SKU 上游 SKU。
type SKU struct {
	ID       string
	Code     string
	Price    int64 // 分
	Stock    int32 // -1 = 无限
	IsActive bool
}

// Product 上游商品（统一输出分）。
type Product struct {
	ID            string
	Name          string
	CategoryID    string
	Price         int64 // 分
	FactoryPrice  int64 // 分（上游成本快照）
	Description   string
	Cover         string
	IsActive      bool
	Stock         int32 // -1 = 无限（手动发货）
	SKUs          []SKU
	UpstreamExtra map[string]any // 协议私有字段（同步时原样入 settings/审计）
}

// ProductList 商品列表（分页）。
type ProductList struct {
	Total   int
	Items   []Product
	HasMore bool
	// IncludesInactive 上游是否真的在本次响应里包含了下架商品（dujiao 回声字段）。
	// 删除对账的权威性判据：仅当快照完整（全量 + 回声为 true）时，
	// 「上游未见」才可推断为已删除（误判会把下架商品当删除下架掉——语义虽同向
	// 但统计口径失真；旧版上游不识别 include_inactive 时必须禁用对账）。
	IncludesInactive bool
}

// CreateOrderReq 采购提交请求（DownstreamOrderNo 即幂等键，随请求发送）。
type CreateOrderReq struct {
	ProductCode       string // 上游商品标识（acg-faka: shared_code；dujiao: sku_id；zcard: product_id）
	Quantity          int
	DownstreamOrderNo string // 幂等键（防重复下单；acg-faka 作 request_no 防重）
	TraceID           string
	CallbackURL       string // 空 = 不用回调（轮询/巡检兜底）
}

// CreateOrderResult 采购提交结果。
type CreateOrderResult struct {
	UpstreamOrderID string   // 上游订单号（查单依据；acg-faka 必须存 tradeNo）
	Status          string   // delivered | pending | ...
	Amount          int64    // 分
	Cards           []string // delivered 时上游卡密（内存态，调用方必须立即加密）
}

// OrderDetail 上游订单详情。
type OrderDetail struct {
	UpstreamOrderID string
	Status          string   // delivered | pending | ...
	Amount          int64    // 分
	Cards           []string // delivered 时上游卡密（内存态）
}

// OrderLister 上游订单列表能力（P3-07 对账数据源；可选——协议不开放列表的
// 适配器返回 ErrNotSupported，对账任务置 failed「上游不支持列表对账」）。
type OrderLister interface {
	ListOrders(ctx context.Context, start, end time.Time) ([]OrderDetail, error)
}

// IncrementalLister 增量商品列表能力（可选——协议支持 updated_after 过滤的
// 适配器实现；不支持增量的驱动不实现本接口，同步引擎自动回落全量分页）。
// 注意：增量快照不具删除对账权威性（未见 ≠ 已删除），引擎仅在全量模式对账。
type IncrementalLister interface {
	ListProductsAfter(ctx context.Context, page, pageSize int, updatedAfter time.Time) (*ProductList, error)
}

// Adapter 货源适配器接口（port 契约，P2-01 T2 / P2-02 消费方）。
type Adapter interface {
	// Protocol 返回协议名（zcard | dujiao_next | acg_faka）。
	Protocol() string

	// Ping 连通性探测。
	Ping(ctx context.Context) (*PingResult, error)

	// ListCategories 拉取分类（不支持时返回空切片，不报错）。
	ListCategories(ctx context.Context) ([]Category, error)

	// ListProducts 分页拉取商品。includeInactive=true 时上游应返回下架商品
	// （dujiao 的 includes_inactive 回声字段由本包转写为 ProductList 的
	// 语义：上游不支持该参数时，missing=已删除 的推断必须禁用，见 sync.go）。
	ListProducts(ctx context.Context, page, pageSize int, includeInactive bool) (*ProductList, error)

	// GetStock 实时查库存（-1 = 无限；T4 fail-open 判据）。
	GetStock(ctx context.Context, productCode, skuCode string) (int32, error)

	// CreateOrder 提交采购（DownstreamOrderNo 幂等）。
	CreateOrder(ctx context.Context, req CreateOrderReq) (*CreateOrderResult, error)

	// GetOrder 查询上游订单（三通道结果获取共用）。
	GetOrder(ctx context.Context, upstreamOrderID string) (*OrderDetail, error)

	// RefundOrder 向上游传导退款（可选能力；acg-faka 返回 ErrNotSupported）。
	RefundOrder(ctx context.Context, upstreamOrderID string) error
}

// New 构造适配器。baseURL 经 httpx.ValidateURL 校验（SSRF），凭据为解密后的
// 明文 JSON（调用方从 supply_connections.credentials 解密后传入）。
func New(driver, baseURL string, creds Credentials, retryIntervals []int) (Adapter, error) {
	if baseURL == "" {
		return nil, errors.New("adapter: base_url 不能为空")
	}
	switch driver {
	case "zcard":
		return newZCard(baseURL, creds, retryIntervals)
	case "dujiao_next":
		return newDujiaoNext(baseURL, creds, retryIntervals)
	case "acg_faka":
		return newAcgFaka(baseURL, creds, retryIntervals)
	default:
		return nil, fmt.Errorf("adapter: 不支持的驱动 %q", driver)
	}
}
