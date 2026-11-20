package adapter

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	invport "github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supplier"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

type mcySelfCatalog struct{ selfContractCatalog }

func (mcySelfCatalog) GetForSupply(context.Context, uint64) (*catalogport.SupplierProduct, error) {
	return &catalogport.SupplierProduct{ID: 1, Name: "Diagnostic product", Price: 1234, CategoryID: 7, Status: 1}, nil
}

type mcySelfInventory struct{ invport.Inventory }

func (mcySelfInventory) Stock(context.Context, uint64, uint64) (int64, error) {
	return 9, nil
}

func (mcySelfInventory) Reserve(_ context.Context, _ uint64, items []invport.ReserveItem) (*invport.Reservation, error) {
	out := &invport.Reservation{}
	for i := int32(0); i < items[0].Quantity; i++ {
		out.Cards = append(out.Cards, invport.ReservedCard{CardID: uint64(100 + i)})
	}
	return out, nil
}

func (mcySelfInventory) MarkUsed(context.Context, []uint64, uint64) error { return nil }

type mcySelfCards struct{}

func (mcySelfCards) Contents(_ context.Context, ids []uint64, _, _ uint64) ([]string, error) {
	out := []string{}
	for _, id := range ids {
		out = append(out, fmt.Sprintf("CARD-%d", id))
	}
	return out, nil
}

// Run the actual adapter against the registered HTTP compatibility routes and
// real database ledger, covering the complete signed request/response contract.
func TestMCYSelfContract(t *testing.T) {
	ctx := context.Background()
	handle, err := db.SQLite.Open("file:mcyself?mode=memory&cache=shared&_pragma=foreign_keys(1)")
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
	repo := supplier.NewSupplierRepoImpl(d, box)
	account, err := repo.CreateAccount(ctx, "MCY", "mcy-self", "self-secret", "", "mcy_shop", "MCY contract")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReviewAccount(ctx, account.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.Recharge(ctx, account.ID, 10000, "self-recharge", ""); err != nil {
		t.Fatal(err)
	}
	client.Product.Create().SetID(1).SetName("Diagnostic product").SetSlug("mcy-self").SetPrice(1234).SetStatus(1).SaveX(ctx)
	svc := supplier.NewSupplyAPIService(repo, mcySelfCatalog{}, mcySelfInventory{}, mcySelfCards{}, nil, nil, nil)
	server := khttp.NewServer()
	supplier.RegisterMCYCompat(server, svc)
	srv := httptest.NewServer(server)
	defer srv.Close()
	a := &mcyAdapter{creds: Credentials{AppID: "mcy-self", AppKey: "self-secret"}, t: newTransportWithClient(srv.URL, []int{0}, nil, srv.Client())}
	ping, err := a.Ping(ctx)
	if err != nil || ping.Balance != 10000 {
		t.Fatalf("self ping: %+v %v", ping, err)
	}
	list, err := a.ListProducts(ctx, 1, 50, true)
	if err != nil || len(list.Items) != 1 || list.Items[0].SKUs[0].ID != "1" {
		t.Fatalf("self catalog: %+v %v", list, err)
	}
	quoted, err := a.QuoteProduct(ctx, &list.Items[0])
	if err != nil || quoted.FactoryPrice != 1234 {
		t.Fatalf("self quote: %+v %v", quoted, err)
	}
	for i := 0; i < 2; i++ {
		order, err := a.CreateOrder(ctx, CreateOrderReq{ProductCode: "1", UpstreamSKU: "1", Quantity: 2, DownstreamOrderNo: "MCY-self-order"})
		if err != nil || len(order.Cards) != 2 || order.Cards[1] != "CARD-101" || order.Amount != 2468 {
			t.Fatalf("self order: %+v %v", order, err)
		}
	}
	balance, err := repo.BalanceOf(ctx, account.ID)
	if err != nil || balance != 7532 || client.SupplyOrder.Query().CountX(ctx) != 1 {
		t.Fatal("replay modified original ledger/order", balance, err)
	}
}
