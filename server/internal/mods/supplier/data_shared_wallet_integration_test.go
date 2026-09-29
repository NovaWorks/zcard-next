//go:build integration

package supplier

import (
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
	"testing"
)

func TestSharedWalletMySQL(t *testing.T)    { testSharedWalletDB(t, testint.MySQL(t)) }
func TestSharedWalletPostgres(t *testing.T) { testSharedWalletDB(t, testint.PG(t)) }
func testSharedWalletDB(t *testing.T, h *testint.Harness) {
	box, _ := crypto.NewBox(make([]byte, 32))
	r := NewSupplierRepoImpl(h.Data, box, wallet.ProvidePortWallet(wallet.NewWalletRepoImpl(h.Data)))
	exerciseSharedWallet(t, r, h.Data)
}
