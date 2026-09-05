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
	Cover        string
	Description  string      // 商品详情（sanitize 后富文本；storefront 详情页下发）
	Price        money.Cents // 售价（分）
	FactoryPrice money.Cents // 成本价（分）
	StockType    string      // card / url / code
	DeliveryMode string      // status / delete
	Status       int8        // 1=上架 0=下架 2=隐藏
	StockVisible bool
	// 积分兑换价（0=常规商品；>0=积分商城商品——order 兑换分支判定，）
	PointsRequired int64
	// 货源信息（ procurement 消费：判定上游项与提交采购）
	UpstreamSourceID    uint64 // 0 = 自营
	UpstreamProductCode string
	// 运营推荐（storefront 首页推荐位）
	IsRecommend bool
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

// Category 前台可见分类（导航/筛选用；ParentID 0=根，多级分类树形）。
type Category struct {
	ID       uint64
	Name     string
	Icon     string
	ParentID uint64
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
	// PointsOnly 积分商城视图（true=仅 points_required>0 商品；）
	PointsOnly bool
	// RecommendOnly 推荐位视图（true=仅 is_recommend 商品；首页推荐区块）
	RecommendOnly bool
	// Sort 排序：default（综合）| newest（最新上架）| price_asc | price_desc | sales（空=default）
	Sort string
}

// ProductReader 商品读取窄接口（storefront service 与 order 模块消费，通道 A）。
type ProductReader interface {
	// ListVisible 上架商品分页（隐藏商品对游客 404 由调用方按 Status 判定）。
	ListVisible(ctx context.Context, f VisibleFilter) (items []Product, total int64, err error)
	// Get 取单个商品（含下架/隐藏，调用方决定可见性语义）。
	Get(ctx context.Context, subsiteID, id uint64) (*Product, error)
	// SkuUpstreamCode 本地 SKU 的上游标识（product_skus.upstream_sku_id；
	// 采购提交时还原规格选择。不存在/未同步来源 → 空串）。
	SkuUpstreamCode(ctx context.Context, subsiteID, skuID uint64) string
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
	// LowStockThreshold 低库存过滤阈值（>0 时仅返回卡密类且可用库存 < 阈值的商品）
	LowStockThreshold int
	// 渠道筛选（商品列表按供货渠道/自营过滤）
	ConnectionID uint64 // products.upstream_source_id
	LocalOnly    bool   // 仅自营（上游渠道为空）
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
	// 积分兑换价（分单位积分；0=不参与积分商城——PUT 全量语义，）
	PointsRequired    int64
	PointsRequiredSet bool // true = 写入该值（含 0=移出积分商城）
	// 运营推荐（storefront 首页推荐位；PUT 全量语义，含 false=取消推荐）
	IsRecommend bool
	// 直发内容密文（url/code 商品；nil=不动。service 层已用 CardCipher 加密，
	// AAD=product_id+subsite_id——创建时商品尚无 ID，由 repo 建后回填）
	DirectContent []byte
}

// ProductAdminRepo 管理面仓储端口（service 层消费）。
type ProductAdminRepo interface {
	ListAdmin(ctx context.Context, f AdminFilter) ([]any, int64, error) // any=*ent.Product，避免 port 依赖 ent
	GetAdmin(ctx context.Context, subsiteID, id uint64) (any, error)
	CreateProduct(ctx context.Context, in ProductInput) (any, error)
	UpdateProduct(ctx context.Context, id uint64, in ProductInput) (any, error)
	SetDirectContent(ctx context.Context, id uint64, ciphered []byte) error
	DeleteProduct(ctx context.Context, id uint64) error
}

// UpstreamProductInput 货源同步 upsert 输入（）。
// Price=-1 表示「不更新价格」（价格保护：auto_sync_price=false 或运营已改价）。
type UpstreamProductInput struct {
	ConnectionID        uint64 // products.upstream_source_id
	UpstreamProductCode string // products.upstream_product_code（UNIQUE 判据）
	UpstreamSyncedAt    time.Time
	Name                string
	Description         string
	Cover               string
	CategoryID          uint64             // 0 = 不设置分类
	Price               int64              // 分；-1 = 保持现有价
	FactoryPrice        int64              // 分（上游成本快照）
	Status              int8               // 1=上架 2=隐藏 0=下架
	AutoOnshelf         bool               // 新建商品时是否上架（settings.auto_onshelf）
	SKUs                []UpstreamSKUInput // 上游规格组合（空=不动现有 SKU；显式空切片语义同空——上游无规格时不清理本地手建 SKU）
}

// UpstreamSKUInput 上游规格组合（acg 笛卡尔积 / dujiao SKU）。
type UpstreamSKUInput struct {
	Code       string            // 上游 SKU 标识（acg 规格选择编码；dujiao sku_id）→ product_skus.upstream_sku_id
	Name       string            // 展示名（缺省回退 Code）
	PriceCents int64             // 组合价（已过定价管线）
	SpecValues map[string]string // 结构化规格 {规格: 值}
}

// UpstreamProductWriter 货源同步商品 upsert 端口（supply 模块消费，通道 A）。
// 语义：按 upstream_source_id + upstream_product_code 幂等 upsert；
// 返回 (productID, created, error)。
type UpstreamProductWriter interface {
	UpsertUpstreamProduct(ctx context.Context, in UpstreamProductInput) (productID uint64, created bool, err error)
}

// UpstreamProductMaintainer 货源轻量维护端口（ S1：scope=price/status 同步
// 与删除对账消费，通道 A）。定位判据同上；found=false 表示商品未导入（调用方跳过）。
type UpstreamProductMaintainer interface {
	// UpdateUpstreamPrice 仅更新价格（price scope；不动名称/状态/库存）。
	UpdateUpstreamPrice(ctx context.Context, connectionID uint64, productCode string, priceCents int64) (found bool, err error)
	// UpdateUpstreamStatus 仅更新上下架状态（status scope；1=上架 2=隐藏 0=下架）。
	UpdateUpstreamStatus(ctx context.Context, connectionID uint64, productCode string, status int8) (found bool, err error)
	// ShelveOffMissing 删除对账：将连接下 upstream_product_code ∉ seen 的
	// 已导入商品批量下架(0)（上游已删除推断；返回下架数）。
	ShelveOffMissing(ctx context.Context, connectionID uint64, seen []string) (shelved int64, err error)
}

// SupplierProduct 供货目录商品（ supplier 消费，通道 A）：
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
	// ListSupplyCategories 分类列表（acg-faka 兼容层 items 两级树用； C）。
	ListSupplyCategories(ctx context.Context) ([]SupplyCategory, error)
}

// SupplyCategory 供货目录分类（兼容层树节点）。
type SupplyCategory struct {
	ID   uint64
	Name string
}

// SettingsReader 系统设置读取（通道 A：settings.RepoImpl 适配；低库存阈值等）。
type SettingsReader interface {
	// GetJSON 读取分组配置（读取失败返回 nil, nil，调用方走默认值）。
	GetJSON(ctx context.Context, group, key string) ([]byte, error)
}
