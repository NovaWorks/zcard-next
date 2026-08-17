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
	NewGiftcardRepo,
	NewStoreWalletService,
	NewAdminWalletService,
	// P3-05：钱包端口适配器（Entry 字段口径转换：port.Entry → 本地 Entry）
	ProvidePortWallet,
	// P1-05 M1b：积分账本端口（payment 充值赠送消费）
	ProvidePortPoints,
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

// portPointsAdapter port.Points → WalletRepoImpl（积分入账）。
type portPointsAdapter struct{ repo *WalletRepoImpl }

func (a portPointsAdapter) PointCreditInTx(ctx context.Context, e port.PointEntry) error {
	return a.repo.PointCreditInTx(ctx, PointEntry{
		UserID: e.UserID, Direction: e.Direction, Type: e.Type,
		Amount: e.Amount, Reference: e.Reference,
		OrderID: e.OrderID, Remark: e.Remark,
	})
}

// ProvidePortPoints 积分端口适配 provider。
func ProvidePortPoints(repo *WalletRepoImpl) port.Points {
	return portPointsAdapter{repo: repo}
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
