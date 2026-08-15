package wallet

// 账户/流水仓储骨架（M1 交付：InTx 行锁 + 乐观锁 + 流水幂等重入 + 并发充值/消费测试）。

import "github.com/NovaWorks/zcard-next/server/internal/data"

// WalletRepoImpl 钱包仓储实现（M1 交付方法体）。
type WalletRepoImpl struct {
	data *data.Data
}

// NewWalletRepoImpl 构造。
func NewWalletRepoImpl(d *data.Data) *WalletRepoImpl { return &WalletRepoImpl{data: d} }
