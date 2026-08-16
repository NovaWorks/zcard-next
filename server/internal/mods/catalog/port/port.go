// Package port 为 catalog 模块对外契约（零依赖包）。
package port

import (
	"context"

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
}

// VisibleFilter 可见商品过滤（storefront 列表 / order 下单校验共用）。
type VisibleFilter struct {
	SubsiteID  uint64
	CategoryID uint64
	Keyword    string
	Page       int32
	PageSize   int32
}

// ProductReader 商品读取窄接口（storefront service 与 order 模块消费，通道 A）。
type ProductReader interface {
	// ListVisible 上架商品分页（隐藏商品对游客 404 由调用方按 Status 判定）。
	ListVisible(ctx context.Context, f VisibleFilter) (items []Product, total int64, err error)
	// Get 取单个商品（含下架/隐藏，调用方决定可见性语义）。
	Get(ctx context.Context, subsiteID, id uint64) (*Product, error)
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
}

// ProductAdminRepo 管理面仓储端口（service 层消费）。
type ProductAdminRepo interface {
	ListAdmin(ctx context.Context, f AdminFilter) ([]any, int64, error) // any=*ent.Product，避免 port 依赖 ent
	GetAdmin(ctx context.Context, subsiteID, id uint64) (any, error)
	CreateProduct(ctx context.Context, in ProductInput) (any, error)
	UpdateProduct(ctx context.Context, id uint64, in ProductInput) (any, error)
	DeleteProduct(ctx context.Context, id uint64) error
}
