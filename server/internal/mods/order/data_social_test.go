package order

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"strings"
	"testing"
	"time"
)

func socialOrderFixture(t *testing.T) (*data.Data, *OrderUsecase, CreateOrderInput) {
	t.Helper()
	d, uc, pid := newGateEnv(t)
	ctx := context.Background()
	cipher, e := inventory.NewCardCipher(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	uc.AccessCipher = cipher
	p := d.Client.Product.UpdateOneID(pid).SetDeliveryKind("social_service").SetProductKind("social_service").SetPrice(0).SaveX(ctx)
	conn := d.Client.SupplyConnection.Create().SetID(9).SetName("A").SetDriver("zcard").SetStatus("active").SetBaseURL("https://example.test").SetCredentials([]byte("ciphertext")).SetExchangeRate(1).SaveX(ctx)
	m := d.Client.SupplyMapping.Create().SetConnectionID(9).SetUpstreamProduct(p.UpstreamProductCode).SetUpstreamSku("").SetLocalProductID(pid).SetPricingOverride(map[string]any{"rule": map[string]any{"mode": "equal"}}).SaveX(ctx)
	schema := supplyport.SocialSchema{Version: 1, Revision: "schema", Profile: "package", Purchasable: true, Fields: []supplyport.SocialField{{Key: "link", Type: "url", Required: true}}, Quantity: supplyport.SocialQuantity{Mode: "absent"}}
	params := supplyport.SocialParameters{"link": "https://example.test/private-target"}
	raw, _ := json.Marshal(params)
	sealed, e := cipher.Seal(string(raw), pid, 0)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(schema)
	d.Client.SocialQuote.Create().SetID("quote-B").SetRole("retail").SetUserID(1).SetProductID(pid).SetConnectionID(9).SetConnectionIdentity(data.SocialConnectionIdentity(conn)).SetProductRevision(p.LockVersion).SetSchemaRevision("schema").SetPricingRevision(data.SocialRetailPricingRevision(conn, m)).SetUpstreamProductID(p.UpstreamProductCode).SetParamsCipher(sealed).SetSchemaJSON(string(b)).SetSupplyQuoteID("quote-A").SetCurrency("CNY").SetAmountCents(999).SetCostCents(123).SetExpiresAt(time.Now().Add(time.Minute).Unix()).SaveX(ctx)
	return d, uc, CreateOrderInput{SocialQuoteID: "quote-B", UserID: 1, IdempotencyKey: "stable-browser-request", Items: []OrderItemInput{{ProductID: pid, Quantity: 1}}}
}
func TestSocialNativeOrderFrozenPackageAndPaidTaskAtomic(t *testing.T) {
	d, uc, in := socialOrderFixture(t)
	ctx := context.Background()
	result, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if result.TotalCents != 999 || d.Client.SocialTask.Query().CountX(ctx) != 0 {
		t.Fatal("unexpected amount or purchase before payment")
	}
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	if strings.Contains(it.SocialPurchaseSnapshot, "private-target") {
		t.Fatal("plaintext service parameters persisted")
	}
	e = data.Tx(ctx, d, func(tx context.Context) error {
		if e := uc.MarkPaid(tx, result.OrderNo); e != nil {
			return e
		}
		return fmt.Errorf("crash")
	})
	if e == nil || d.Client.SocialTask.Query().CountX(ctx) != 0 || d.Client.Order.Query().OnlyX(ctx).Status != "pending_payment" {
		t.Fatal("paid task escaped rollback")
	}
	if e = uc.MarkPaid(ctx, result.OrderNo); e != nil {
		t.Fatal(e)
	}
	if e = uc.MarkPaid(ctx, result.OrderNo); e != nil {
		t.Fatal(e)
	}
	task := d.Client.SocialTask.Query().OnlyX(ctx)
	raw, e := uc.AccessCipher.Open(task.RequestCipher, it.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	var req supplyport.SocialPurchase
	if json.Unmarshal([]byte(raw), &req) != nil || req.QuoteID != "quote-A" || req.Parameters["link"] != "https://example.test/private-target" {
		t.Fatal("request not frozen")
	}
	if _, exists := req.Parameters["quantity"]; exists {
		t.Fatal("package fabricated a supplier quantity")
	}
	d.Client.SocialQuote.UpdateOneID(in.SocialQuoteID).SetExpiresAt(0).ExecX(ctx)
	d.Client.Product.UpdateOneID(in.Items[0].ProductID).SetStatus(0).ExecX(ctx)
	replay, e := uc.CreateOrder(ctx, in)
	if e != nil || replay.OrderNo != result.OrderNo || d.Client.SocialTask.Query().CountX(ctx) != 1 {
		t.Fatal("original purchase was not replayed", e)
	}
}
func TestSocialQuoteOwnerAndChangedRequestRejected(t *testing.T) {
	d, uc, in := socialOrderFixture(t)
	ctx := context.Background()
	foreign := in
	foreign.UserID = 2
	if _, e := uc.CreateOrder(ctx, foreign); e == nil {
		t.Fatal("foreign quote accepted")
	}
	if d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("invalid quote created order")
	}
	_, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	in.SocialQuoteID = "another-quote"
	if _, e = uc.CreateOrder(ctx, in); e == nil {
		t.Fatal("same request changed quote")
	}
}

func TestSocialQuoteExpiryBeforeCreateAndBeforePay(t *testing.T) {
	for _, stage := range []string{"create", "pay"} {
		t.Run(stage, func(t *testing.T) {
			d, uc, in := socialOrderFixture(t)
			ctx := context.Background()
			var no string
			if stage == "pay" {
				created, e := uc.CreateOrder(ctx, in)
				if e != nil {
					t.Fatal(e)
				}
				no = created.OrderNo
			}
			d.Client.SocialQuote.UpdateOneID(in.SocialQuoteID).SetExpiresAt(time.Now().Add(-time.Second).Unix()).ExecX(ctx)
			if stage == "create" {
				if _, e := uc.CreateOrder(ctx, in); e == nil {
					t.Fatal("expired quote created order")
				}
			} else {
				if e := uc.MarkPaid(ctx, no); e == nil {
					t.Fatal("expired quote accepted payment")
				}
				if d.Client.Order.Query().OnlyX(ctx).Status != "pending_payment" {
					t.Fatal("expired payment advanced order")
				}
			}
			if d.Client.SocialTask.Query().CountX(ctx) != 0 {
				t.Fatal("expired quote purchased upstream")
			}
		})
	}
}
