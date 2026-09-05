// Package port 为 inventory 模块对外契约（零依赖包）。
package port

import (
	"context"
	"time"
)

// ReserveItem 锁卡请求项。
type ReserveItem struct {
	ProductID uint64
	SkuID     uint64 // 0 = 商品级
	Quantity  int32
	// 靓号自选卡号归一化 hash（非靓号场景为空）
	NumberHash string
}

// ReservedCard 已锁定卡密（不携带明文/密文内容——内容只在交付时现场解密）。
type ReservedCard struct {
	CardID uint64
	Locked bool
}

// Reservation 锁定结果。
type Reservation struct {
	// ReservationID 预留批次标识（TTL 释放的扫描锚点）
	ReservationID string
	Cards         []ReservedCard
	ExpiresAt     time.Time
}

// Inventory 库存窄接口（order 下单事务内锁卡消费，通道 B：同事务工作单元）。
// 交付；实现约束：MySQL/PG 事务内 FOR UPDATE 锁可用行，SQLite 走
// BEGIN IMMEDIATE + UPDATE...WHERE status='available' 校验 affected rows（）。
type Inventory interface {
	Reserve(ctx context.Context, subsiteID uint64, items []ReserveItem) (*Reservation, error)
	// Release 释放预留（订单取消/超时，TTL 兜底由周期任务二次释放）。
	Release(ctx context.Context, orderID uint64) error
	// BindOrder 锁卡后绑定订单（Reserve 成功 → caller 回填 order_id）。
	BindOrder(ctx context.Context, subsiteID, productID, orderID uint64, quantity int32) error
	// MarkUsed 售出标记（校验 affected rows 防并发重发，友商纪律）。
	MarkUsed(ctx context.Context, cardIDs []uint64, orderID uint64) error
	// Stock 可用库存数（-1 = 无限，链接类商品）。
	Stock(ctx context.Context, productID, skuID uint64) (int64, error)
}

// CardContentReader 交付卡密读取（ 供货交付消费，通道 A）：
// 按卡 ID 批量读取密文并现场解密返回明文（明文仅内存态，铁律 11 出口约束）。
type CardContentReader interface {
	Contents(ctx context.Context, cardIDs []uint64, productID, subsiteID uint64) ([]string, error)
}

// CardReleaser 锁卡回滚（ 供货交付失败时释放；reserved → available）。
type CardReleaser interface {
	ReleaseCards(ctx context.Context, cardIDs []uint64) error
}

// StockBatcher 批量可用库存（管理列表展示消费，通道 A——单商品 N+1 防护）。
type StockBatcher interface {
	// StockBatch 各商品可用卡密数（一条 GROUP BY；链接/兑换码类不入卡池=无条目）。
	StockBatch(ctx context.Context, productIDs []uint64) (map[uint64]int64, error)
}
