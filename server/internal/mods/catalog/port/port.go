// Package port 为 catalog 模块对外契约（零依赖包）。
package port

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// Product 商品 DTO（跨模块快照：order 价格管线消费；管理字段不下发）。
type Product struct {
	ID           uint64
	SubsiteID    uint64
	Name         string
	Slug         string
	Price        money.Cents // 售价（分）
	FactoryPrice money.Cents // 成本价（分）
	StockType    string      // card / url / code
	DeliveryMode string      // status / delete
	Status       int8        // 1=上架 0=下架 2=隐藏
	StockVisible bool
	// 积分兑换价（0=常规商品；>0=积分商城商品——order 兑换分支判定，P3-01）
	PointsRequired int64
	// 货源信息（P2-02 procurement 消费：判定上游项与提交采购）
	UpstreamSourceID    uint64 // 0 = 自营
	UpstreamProductCode string
}

// Control 自定义控件 DTO（下单表单渲染）。
type Control struct {
	ID       uint64
	Name     string
	Type     string // text | password | select | number | checkbox | radio
	Required bool
	Options  []string
	Sort     int32
}

// ReviewItem 前台评价条目（真实 approved + 虚拟合并；Nickname 真实评价为空）。
type ReviewItem struct {
	ID        uint64
	Nickname  string
	Content   string
	Rating    int32
	IsVirtual bool
	Sort      int32
	CreatedAt time.Time
}

// Sku 前台多规格 DTO（只下发 id/名称/价格）。
type Sku struct {
	ID        uint64
	Name      string
	Price     money.Cents // 独立售价（分；0=继承商品价）
	ProductID uint64
}

// VisibleFilter 可见商品过滤（storefront 列表 / order 下单校验共用）。
type VisibleFilter struct {
	SubsiteID  uint64
	CategoryID uint64
	Keyword    string
	Page       int32
	PageSize   int32
	// PointsOnly 积分商城视图（true=仅 points_required>0 商品；P3-01）
	PointsOnly bool
}

// ProductReader 商品读取窄接口（storefront service 与 order 模块消费，通道 A）。
type ProductReader interface {
	// ListVisible 上架商品分页（隐藏商品对游客 404 由调用方按 Status 判定）。
	ListVisible(ctx context.Context, f VisibleFilter) (items []Product, total int64, err error)
	// Get 取单个商品（含下架/隐藏，调用方决定可见性语义）。
	Get(ctx context.Context, subsiteID, id uint64) (*Product, error)
}

// PricingResolver 商品定价解析（order 价格管线消费，通道 A）：
// SKU 价 > 商品价；会员商品组折扣万分比。
type PricingResolver interface {
	// ResolvePrice 解析商品/SKU 售价（分）。skuID=0 或 SKU 价为空时回落到商品价。
	ResolvePrice(ctx context.Context, productID, skuID uint64) (price money.Cents, err error)
	// ResolveGroupRate 解析命中的会员商品组折扣（万分比；0=不命中）。多组命中取最高折扣。
	ResolveGroupRate(ctx context.Context, productID uint64) (rate int32, err error)
}

// AdminFilter 管理面商品过滤（含下架/隐藏；成本价下发）。
type AdminFilter struct {
	SubsiteID  uint64
	CategoryID uint64
	Keyword    string
	Status     int8 // -1=全部
	Page       int32
	PageSize   int32
}

// ProductInput 商品创建/更新输入（description 已 sanitize）。
type ProductInput struct {
	Name         string
	CategoryID   uint64
	Description  string
	Cover        string
	Images       []string
	Price        int64 // 分
	FactoryPrice int64
	StockType    string
	DeliveryMode string
	StockVisible bool
	Dedup        bool
	Sort         int32
	Status       int8
	// 积分兑换价（分单位积分；0=不参与积分商城——PUT 全量语义，P3-01）
	PointsRequired    int64
	PointsRequiredSet bool // true = 写入该值（含 0=移出积分商城）
}

// ProductAdminRepo 管理面仓储端口（service 层消费）。
type ProductAdminRepo interface {
	ListAdmin(ctx context.Context, f AdminFilter) ([]any, int64, error) // any=*ent.Product，避免 port 依赖 ent
	GetAdmin(ctx context.Context, subsiteID, id uint64) (any, error)
	CreateProduct(ctx context.Context, in ProductInput) (any, error)
	UpdateProduct(ctx context.Context, id uint64, in ProductInput) (any, error)
	DeleteProduct(ctx context.Context, id uint64) error
}

// UpstreamProductInput 货源同步 upsert 输入（P2-01 T3）。
// Price=-1 表示「不更新价格」（价格保护：auto_sync_price=false 或运营已改价）。
type UpstreamProductInput struct {
	ConnectionID        uint64 // products.upstream_source_id
	UpstreamProductCode string // products.upstream_product_code（UNIQUE 判据）
	UpstreamSyncedAt    time.Time
	Name                string
	Description         string
	Cover               string
	CategoryID          uint64 // 0 = 不设置分类
	Price               int64  // 分；-1 = 保持现有价
	FactoryPrice        int64  // 分（上游成本快照）
	Status              int8   // 1=上架 2=隐藏 0=下架
	AutoOnshelf         bool   // 新建商品时是否上架（settings.auto_onshelf）
}

// UpstreamProductWriter 货源同步商品 upsert 端口（supply 模块消费，通道 A）。
// 语义：按 upstream_source_id + upstream_product_code 幂等 upsert；
// 返回 (productID, created, error)。
type UpstreamProductWriter interface {
	UpsertUpstreamProduct(ctx context.Context, in UpstreamProductInput) (productID uint64, created bool, err error)
}

// SupplierProduct 供货目录商品（P2-03 supplier 消费，通道 A）：
// 管理面语义（含下架/隐藏），仅下发可公开字段。
type SupplierProduct struct {
	ID           uint64
	Name         string
	Price        int64 // 分（基础价；覆盖价由 supplier 定价表决定）
	FactoryPrice int64 // 分（成本快照）
	CategoryID   uint64
	Description  string
	Cover        string
	Status       int8 // 1=上架 0=下架 2=隐藏
}

// SupplierCatalog 供货目录端口（对外供货 API 消费）。
type SupplierCatalog interface {
	// ListForSupply 目录分页（Status=-1 全含）。
	ListForSupply(ctx context.Context, f AdminFilter) ([]SupplierProduct, int64, error)
	// GetForSupply 单品（含下架）。
	GetForSupply(ctx context.Context, productID uint64) (*SupplierProduct, error)
}
