package bootstrap

// 雪花 ID 生成器（订单号/工单号/退款单号）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"

	"github.com/google/wire"
)

var idProviderSet = wire.NewSet(NewIDGenerator)

// NewIDGenerator 构造（workerID 暂用 0——单实例部署；多实例由实例 ID 派生）。
func NewIDGenerator() (*id.Generator, error) {
	return id.NewGenerator(0)
}
