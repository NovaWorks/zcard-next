package wallet

// wire providers。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"github.com/google/wire"
)

// ProviderSet wallet providers。
var ProviderSet = wire.NewSet(
	NewWalletRepoImpl,
	NewStoreWalletService,
	NewAdminWalletService,
	// P3-05：钱包端口适配器（Entry 字段口径转换：port.Entry → 本地 Entry）
	ProvidePortWallet,
)

// portWalletAdapter port.Wallet → WalletRepoImpl（Entry 转换）。
type portWalletAdapter struct{ repo *WalletRepoImpl }

func (a portWalletAdapter) CreditInTx(ctx context.Context, e port.Entry) error {
	return a.repo.CreditInTx(ctx, toLocalEntry(e))
}

func (a portWalletAdapter) DebitInTx(ctx context.Context, e port.Entry) error {
	return a.repo.DebitInTx(ctx, toLocalEntry(e))
}

func (a portWalletAdapter) Lock(ctx context.Context, userID uint64, amount money.Cents, availableAt int64) error {
	return a.repo.Lock(ctx, userID, int64(amount), availableAt)
}

func (a portWalletAdapter) Unlock(ctx context.Context, userID uint64, amount money.Cents) error {
	return a.repo.Unlock(ctx, userID, int64(amount))
}

func toLocalEntry(e port.Entry) Entry {
	return Entry{
		UserID: e.UserID, Direction: e.Direction, Type: e.Type,
		Amount: int64(e.Amount), Reference: e.Reference,
		OrderID: e.OrderID, OperatorID: e.Operator, Remark: e.Remark,
	}
}

// ProvidePortWallet 端口适配 provider。
func ProvidePortWallet(repo *WalletRepoImpl) port.Wallet {
	return portWalletAdapter{repo: repo}
}
