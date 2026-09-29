package adapter

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"net/http/httptest"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	_ "modernc.org/sqlite"
)

type selfContractCatalog struct{}

func (selfContractCatalog) ListForSupply(context.Context, catalogport.AdminFilter) ([]catalogport.SupplierProduct, int64, error) {
	return []catalogport.SupplierProduct{{ID: 1, Name: "Diagnostic product", Price: 1234, FactoryPrice: 500, CategoryID: 7, Status: 1}}, 1, nil
}
func (selfContractCatalog) GetForSupply(context.Context, uint64) (*catalogport.SupplierProduct, error) {
	return nil, fmt.Errorf("unused")
}
func (selfContractCatalog) ListSupplyCategories(context.Context) ([]catalogport.SupplyCategory, error) {
	return []catalogport.SupplyCategory{{ID: 7, Name: "Diagnostic category"}}, nil
}

// Exercise the real HTTP routes, HMAC filter and JSON encoding with the client.
func TestZCardSelfContract(t *testing.T) {
	ctx := context.Background()
	handle, err := db.SQLite.Open("file:selfdiag?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	box, _ := crypto.NewBox(make([]byte, 32))
	repo := supplier.NewSupplierRepoImpl(d, box, wallet.ProvidePortWallet(wallet.NewWalletRepoImpl(d)))
	acc, err := repo.CreateAccount(ctx, "diagnosis", "diagnostic-key", "diagnostic-secret", "", "zcard", "diagnosis")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReviewAccount(ctx, acc.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if err = repo.Recharge(ctx, acc.ID, 12345, "diagnosis-recharge", ""); err != nil {
		t.Fatal(err)
	}
	svc := supplier.NewSupplyAPIService(repo, selfContractCatalog{}, nil, nil, nil, nil, nil)
	server := khttp.NewServer(khttp.Filter(supplier.SupplyAuthFilter(repo, 0)))
	supplyv1.RegisterSupplyServiceHTTPServer(server, svc)
	srv := httptest.NewServer(server)
	defer srv.Close()

	oldValidate := validateURL
	validateURL = func(string) error { return nil }
	t.Cleanup(func() { validateURL = oldValidate })
	for _, suffix := range []string{"", "/", "/api/supply", "/api/supply/"} {
		t.Run("base="+suffix, func(t *testing.T) {
			generic, err := newZCard(srv.URL+suffix, Credentials{APIKey: "diagnostic-key", APISecret: "diagnostic-secret"}, []int{0})
			if err != nil {
				t.Fatal(err)
			}
			a := generic.(*zCardAdapter)
			a.t.client = srv.Client()
			ping, err := a.Ping(ctx)
			if err != nil || ping.Balance != 12345 || ping.Currency != "CNY" {
				t.Fatalf("self ping: %+v err=%v", ping, err)
			}
			list, err := a.ListProducts(ctx, 1, 50, true)
			if err != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].Price != 1234 || list.HasMore {
				t.Fatalf("self catalog: %+v err=%v", list, err)
			}
			if _, err := a.ListCategories(ctx); err != nil {
				t.Fatal(err)
			}
			a.creds.APISecret = "wrong-secret"
			_, err = a.Ping(ctx)
			if err == nil || upstreamErrorCode(err) != "invalid_signature" || ClassifyImportError(err).Code != "AUTH_FAILED" {
				t.Fatalf("wrong credentials accepted or error discarded: %v", err)
			}
		})
	}
}
