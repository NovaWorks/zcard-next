// Package port 为 settings 模块对外契约（零依赖包）。
package port

import (
	"context"
	"encoding/json"
)

// Item 设置项。
type Item struct {
	Group string
	Key   string
	Value json.RawMessage
}

// Provider 设置读取窄接口（各模块启动/运行时读取业务开关的唯一入口，
// 铁律 7：运行时业务开关在 settings 表，env/config 只作首次部署兜底）。
type Provider interface {
	Get(ctx context.Context, group, key string) (json.RawMessage, error)
	// GetDefault 读取并绑定到默认值结构体（不存在时返回 def 原值）。
	GetDefault(ctx context.Context, group, key string, def json.RawMessage) (json.RawMessage, error)
}

// CurrencyReader 货币读取（展示换算取数端， exchange 消费；rate 为 decimal 字符串）。
type CurrencyReader interface {
	CurrencyByCode(ctx context.Context, code string) (rate string, precision int32, err error)
}
