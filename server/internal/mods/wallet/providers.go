package wallet

// wire providers。

import "github.com/google/wire"

// ProviderSet wallet providers。
var ProviderSet = wire.NewSet(NewWalletUsecase, NewWalletRepoImpl)
