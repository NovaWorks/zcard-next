//go:build integration

package supplier

import (
	"context"
	"net/url"
	"testing"

	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
)

func TestMCYSupplierProtocolMySQL(t *testing.T) {
	h := testint.MySQL(t) // Runs the real versioned migration chain in an isolated database.
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSupplierRepoImpl(h.Data, box)
	ctx := context.Background()
	for _, protocol := range []string{"zcard", "dujiao_next", "acg_faka", "mcy_shop"} {
		seedCompatAccount(t, repo, protocol, "mysql-"+protocol, "secret", 10000)
	}
	h.Data.Client.Product.Create().SetID(1).SetName("MCY MySQL").SetSlug("mcy-mysql").SetPrice(1234).SetStatus(1).SaveX(ctx)
	cat := &fakeCatalog{prods: []catalogport.SupplierProduct{{ID: 1, Name: "MCY MySQL", Price: 1234, Status: 1}}}
	svc := NewSupplyAPIService(repo, cat, &fakeInv{}, &fakeCards{}, nil, nil, nil)
	form := url.Values{"sku_id": {"1"}, "quantity": {"1"}, "trade_no": {"mysql-original"}}
	for i := 0; i < 2; i++ {
		reply := mcyCall(t, mcyMux(svc), "/plugin/open-api/trade", "mysql-mcy_shop", "secret", form, true)
		if reply["code"] != float64(200) || reply["data"].(map[string]any)["contents"] != "CARD-100" {
			t.Fatal("MCY purchase/replay failed against migrated MySQL", reply)
		}
	}
	account, _, err := repo.AccountByKey(ctx, "mysql-mcy_shop")
	if err != nil || account.BalanceCache != 8766 {
		t.Fatal("MCY migration/ledger mismatch", account, err)
	}
}
