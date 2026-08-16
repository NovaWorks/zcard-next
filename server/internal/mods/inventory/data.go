package inventory

// 卡密仓储（P1-02；锁卡/导入/导出实现见 data_lock.go / data_import.go）。

import (
	"github.com/NovaWorks/zcard-next/server/internal/data"
)

// CardRepoImpl 卡密仓储实现。
type CardRepoImpl struct {
	data   *data.Data
	Cipher *CardCipher // AES-GCM 加密 + keyed hash（bootstrap 注入）
}

// NewCardRepoImpl 构造。
func NewCardRepoImpl(d *data.Data) *CardRepoImpl { return &CardRepoImpl{data: d} }

// SetCipher 注入加密器（wire 装配期调用）。
func (r *CardRepoImpl) SetCipher(c *CardCipher) { r.Cipher = c }
