// Package inventory 卡密库存模块（M1）：卡密/链接/兑换码、导入导出、锁定预留、加密、预售/靓号。
//
// 铁律 11：卡密内容强制 AES-256-GCM（无关闭开关），keyed hash 去重，永不落明文；
// 导入预览确认与批量分片、CsvSafe 防 Excel 注入均 M1 交付。
package inventory

import (
	"context"
	"errors"

	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// 卡密状态机（§5.4）：available → reserved(订单锁定/TTL 自动释放) → used(出售)；
// 旁路 disabled（禁用开关）。
const (
	StatusAvailable = "available"
	StatusReserved  = "reserved"
	StatusUsed      = "used"
	StatusDisabled  = "disabled"
)

// ErrInsufficientStock 库存不足（事务内回滚，不发事件，§5.3）。
var ErrInsufficientStock = errors.New("inventory.INSUFFICIENT_STOCK")

// CardRepo 卡密仓储（模块内端口，实现于 data.go，M1 随下单链路交付）。
type CardRepo interface {
	port.Inventory
}

// CardCipher 卡密加密器：加密入库 + keyed hash 去重（铁律 11 的模块内落点）。
type CardCipher struct {
	box *crypto.Box
	key []byte
}

// NewCardCipher 构造（key = ZCARD_CARD_KEY 原始 32 字节）。
func NewCardCipher(key []byte) (*CardCipher, error) {
	box, err := crypto.NewBox(key)
	if err != nil {
		return nil, err
	}
	return &CardCipher{box: box, key: key}, nil
}

// Seal 加密卡密：AAD 绑定 product/subsite（防密文跨商品挪用）。
func (c *CardCipher) Seal(plain string, productID, subsiteID uint64) ([]byte, error) {
	return c.box.Seal([]byte(plain), aad(productID, subsiteID))
}

// Open 解密（交付时现场调用；失败降级为空并提示重配——绝不 500）。
func (c *CardCipher) Open(ciphertext []byte, productID, subsiteID uint64) (string, error) {
	plain, err := c.box.Open(ciphertext, aad(productID, subsiteID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// ContentHash keyed hash 去重（HMAC-SHA256(cardKey, plain)，防低熵卡彩虹表）。
func (c *CardCipher) ContentHash(plain string) string {
	return crypto.KeyedHash(c.key, []byte(plain))
}

func aad(productID, subsiteID uint64) []byte {
	return []byte("product:" + u64(productID) + ":subsite:" + u64(subsiteID))
}

func u64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// InventoryUsecase 库存用例骨架（M1 交付 Reserve/Release/MarkUsed 编排）。
type InventoryUsecase struct {
	repo CardRepo
}

// NewInventoryUsecase 构造。
func NewInventoryUsecase(repo CardRepo) *InventoryUsecase { return &InventoryUsecase{repo: repo} }

// Reserve 事务内锁卡（M1：并发防超卖 100/10 双数据库路径集成测试）。
func (uc *InventoryUsecase) Reserve(ctx context.Context, subsiteID uint64, items []port.ReserveItem) (*port.Reservation, error) {
	return uc.repo.Reserve(ctx, subsiteID, items)
}

// Release 释放预留。
func (uc *InventoryUsecase) Release(ctx context.Context, reservationID string) error {
	return uc.repo.Release(ctx, reservationID)
}
