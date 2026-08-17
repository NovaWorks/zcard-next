package inventory

// 卡密仓储（P1-02；锁卡/导入/导出实现见 data_lock.go / data_import.go）。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
)

// CardRepoImpl 卡密仓储实现。
type CardRepoImpl struct {
	data   *data.Data
	Cipher *CardCipher // AES-GCM 加密 + keyed hash（bootstrap 注入）
}

// NewCardRepoImpl 构造（cipher 由 bootstrap 注入）。
func NewCardRepoImpl(d *data.Data, cipher *CardCipher) *CardRepoImpl {
	return &CardRepoImpl{data: d, Cipher: cipher}
}

// StockBatch 批量可用库存（cards available 计数 GROUP BY product_id）。
func (r *CardRepoImpl) StockBatch(ctx context.Context, productIDs []uint64) (map[uint64]int64, error) {
	out := make(map[uint64]int64, len(productIDs))
	if len(productIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ProductID uint64 `json:"product_id"`
		Count     int    `json:"count"`
	}
	err := data.Client(ctx, r.data).Card.Query().
		Where(card.ProductIDIn(productIDs...), card.StatusEQ(card.StatusAvailable)).
		GroupBy(card.FieldProductID).
		Aggregate(ent.As(ent.Count(), "count")).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ProductID] = int64(r.Count)
	}
	return out, nil
}
