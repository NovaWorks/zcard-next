// Package port 为 affiliate 模块对外契约（零依赖包）。
package port

import "context"

// CommissionStats 佣金统计（dashboard/前台团队页消费）。
type CommissionStats struct {
	PendingCents   int64 `json:"pending_cents"`   // 冻结中（pending_confirm）
	AvailableCents int64 `json:"available_cents"` // 可提（已入 wallet 前）
	WithdrawnCents int64 `json:"withdrawn_cents"` // 已提现
	TotalCents     int64 `json:"total_cents"`     // 累计（正佣金合计）
	DebtCents      int64 `json:"debt_cents"`      // 负债（逆向未扣回）
}

// CommissionRow 佣金行（dashboard 列表消费；零依赖 DTO）。
type CommissionRow struct {
	ID          uint64
	OrderID     uint64
	BuyerID     uint64
	ReferrerID  uint64
	Tier        int32
	Rate        float64
	BaseAmount  int64
	Amount      int64
	Status      string
	AvailableAt int64 // unix；0 未设
	CreatedAt   int64
}

// CommissionReader 佣金读取（dashboard 统计/列表，通道 A）。
type CommissionReader interface {
	StatsByUser(ctx context.Context, userID uint64) (*CommissionStats, error)
	// ListCommissions 佣金列表（status 空=全部）。
	ListCommissions(ctx context.Context, status string, page, size int) (rows []CommissionRow, total int64, err error)
}
