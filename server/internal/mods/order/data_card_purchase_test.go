package order

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

type cardOrderGateway struct {
	amount, cost int64
	revision     string
	request      supplyport.CardPurchaseRequest
	calls        int
}

func (g *cardOrderGateway) CheckItems(context.Context, uint64, []orderport.UpstreamStockItem) error {
	return nil
}
func (g *cardOrderGateway) QuoteCard(_ context.Context, r supplyport.CardPurchaseRequest) (*supplyport.CardPurchaseQuote, error) {
	g.request = r
	g.calls++
	return &supplyport.CardPurchaseQuote{AmountCents: g.amount, CostCents: g.cost, ProviderSelection: r.Selection, ProviderQuoteID: "provider-quote-private", ConfigurationHash: g.revision, ExpiresAt: time.Now().Add(time.Minute).Unix()}, nil
}
func (g *cardOrderGateway) CardConfiguration(context.Context, supplyport.CardPurchaseRequest) (string, error) {
	return g.revision, nil
}
func (g *cardOrderGateway) ListCardPicks(context.Context, supplyport.CardPurchaseRequest, int, int) (*supplyport.CardPickList, error) {
	return nil, nil
}

func cardOrderFixture(t *testing.T) (*data.Data, *OrderUsecase, *cardOrderGateway, CreateOrderInput) {
	t.Helper()
	d, uc, _ := newIdemEnv(t)
	ctx := context.Background()
	conn := d.Client.SupplyConnection.Create().SetName("cards").SetDriver("acg_faka").SetBaseURL("https://cards.test").SetCredentials([]byte("sealed")).SaveX(ctx)
	d.Client.Product.UpdateOneID(1).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("cards").SetControlConfig(map[string]any{cardpurchase.ConfigKey: cardpurchase.Rules{Minimum: 3, Maximum: 200, DynamicQuote: true, Preselection: true}}).ExecX(ctx)
	cipher, e := inventory.NewCardCipher(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	uc.AccessCipher = cipher
	g := &cardOrderGateway{amount: 1001, cost: 701, revision: "configuration-1"}
	uc.StockGate = g
	return d, uc, g, CreateOrderInput{UserID: 3, QueryPassword: "test1234", Items: []OrderItemInput{{ProductID: 1, Quantity: 3}}, IdempotencyKey: "card-quantity-order"}
}

func TestCardOrderQuantityQuoteAndFrozenTotal(t *testing.T) {
	d, uc, g, in := cardOrderFixture(t)
	ctx := context.Background()
	if _, e := uc.CreateOrder(ctx, in); e == nil || !strings.Contains(e.Error(), "QUOTE_CHANGED") {
		t.Fatalf("missing quote accepted: %v", e)
	}
	if d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("unconfirmed order persisted")
	}
	q := in
	q.QuoteOnly = true
	quote, e := uc.CreateOrder(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if !quote.ExpiresAt.After(time.Now()) || quote.ExpiresAt.After(time.Now().Add(65*time.Second)) {
		t.Fatal("quote did not expose provider expiry", quote.ExpiresAt)
	}
	if quote.TotalCents != 1001 || g.request.Quantity != 3 || d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatalf("quantity quote multiplied or persisted: %+v", quote)
	}
	in.QuoteKey = quote.QuoteKey
	created, e := uc.CreateOrder(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if created.TotalCents != 1001 {
		t.Fatalf("total=%d", created.TotalCents)
	}
	it := d.Client.OrderItem.Query().OnlyX(ctx)
	if it.Amount != 1001 || len(it.UpstreamCardCipher) == 0 {
		t.Fatal("exact total or encrypted purchase snapshot lost")
	}
	plain, e := uc.AccessCipher.Open(it.UpstreamCardCipher, it.ID, it.SubsiteID)
	if e != nil {
		t.Fatal(e)
	}
	var f frozenCardPurchase
	if json.Unmarshal([]byte(plain), &f) != nil || f.Quote.CostCents != 701 || f.Quote.ProviderQuoteID != "provider-quote-private" {
		t.Fatal("frozen obligation lost")
	}
	var sum int64
	for _, l := range d.Client.OrderAmountLine.Query().AllX(ctx) {
		sum += l.Amount
	}
	if sum != 1001 {
		t.Fatalf("ledger total=%d", sum)
	}
	if created.ExpiresAt.After(time.Now().Add(65 * time.Second)) {
		t.Fatal("order outlives provider quote")
	}
	calls := g.calls
	again, e := uc.CreateOrder(ctx, in)
	if e != nil || again.OrderNo != created.OrderNo || g.calls != calls {
		t.Fatalf("idempotent replay requoted: %v", e)
	}
	in.Items[0].CardSelection = "different-card"
	if _, e = uc.CreateOrder(ctx, in); e == nil || !strings.Contains(e.Error(), "同一请求标识不能用于不同订单内容") {
		t.Fatal("selection not bound to request identity", e)
	}
}

func TestCardOrderLimitsAndChangedQuote(t *testing.T) {
	d, uc, g, in := cardOrderFixture(t)
	ctx := context.Background()
	for _, n := range []int32{2, 201} {
		in.Items[0].Quantity = n
		if _, e := uc.CreateOrder(ctx, in); e == nil {
			t.Fatalf("invalid quantity %d accepted", n)
		}
	}
	in.Items[0].Quantity = 3
	q := in
	q.QuoteOnly = true
	quote, e := uc.CreateOrder(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	in.QuoteKey = quote.QuoteKey
	g.amount++
	if _, e = uc.CreateOrder(ctx, in); e == nil || !strings.Contains(e.Error(), "QUOTE_CHANGED") {
		t.Fatal("price change accepted", e)
	}
	if d.Client.Order.Query().CountX(ctx) != 0 {
		t.Fatal("changed quote created order")
	}
	d.Client.Product.UpdateOneID(1).SetControlConfig(map[string]any{cardpurchase.ConfigKey: cardpurchase.Rules{Minimum: 1, Maximum: 0, DynamicQuote: true}}).ExecX(ctx)
	in.Items[0].Quantity = 100001
	q = in
	q.QuoteOnly = true
	if _, e = uc.CreateOrder(ctx, q); e != nil || g.request.Quantity != 100001 {
		t.Fatal("invented maximum on unlimited provider", e)
	}
}

func TestCardTotalPriceLinesPreserveFractionalUnitAndDiscounts(t *testing.T) {
	r := cardPriceCalculator(PriceInput{Quantity: 3, MemberRate: 9000, SubsiteMarkup: 2}, money.Cents(1001))
	if r.Total != 907 {
		t.Fatalf("total=%d", r.Total)
	} // 1001 - floor(1001*10%) + 2*3
	var sum int64
	for _, l := range r.Lines {
		sum += lineAmount(l, 3)
	}
	if sum != 907 {
		t.Fatalf("amount lines=%d", sum)
	}
}

// Existing pending orders persisted this exact item representation in their
// idempotency fingerprint before optional preselection was introduced.
func TestCardSelectionPreservesLegacyRequestFingerprint(t *testing.T) {
	item := OrderItemInput{ProductID: 1, SkuID: 2, Quantity: 3, ControlAnswers: map[string]string{"7": "account"}}
	legacy := `[{"ControlAnswers":{"7":"account"},"ProductID":1,"SkuID":2,"Quantity":3}]`
	raw, err := json.Marshal([]OrderItemInput{item})
	if err != nil || string(raw) != legacy {
		t.Fatalf("empty selection changed legacy identity: %s %v", raw, err)
	}
	input := CreateOrderInput{Items: []OrderItemInput{item}}
	before := orderRequestHash(input)
	input.Items[0].CardSelection = "opaque-card"
	if orderRequestHash(input) == before {
		t.Fatal("selection not bound to request identity")
	}
}

func TestCardPointsQuoteShowsExactPointsAndRejectsChangedPrice(t *testing.T) {
	d, uc, w, p, _ := newPointsEnv(t)
	ctx := context.Background()
	conn := d.Client.SupplyConnection.Create().SetName("points-cards").SetDriver("acg_faka").SetBaseURL("https://cards.test").SetCredentials([]byte("sealed")).SaveX(ctx)
	d.Client.Product.UpdateOneID(p.ID).SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("points-cards").SetControlConfig(map[string]any{cardpurchase.ConfigKey: cardpurchase.Rules{Minimum: 3, Maximum: 3, DynamicQuote: true}}).ExecX(ctx)
	cipher, e := inventory.NewCardCipher(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	uc.AccessCipher = cipher
	uc.StockGate = &cardOrderGateway{amount: 1001, cost: 701, revision: "points-configuration"}
	in := CreateOrderInput{UserID: 7, QueryPassword: "test1234", UsePoints: true, Items: []OrderItemInput{{ProductID: p.ID, Quantity: 3}}}
	q := in
	q.QuoteOnly = true
	quote, e := uc.CreateOrder(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if quote.TotalCents != 0 || quote.PointsTotal != 300 {
		t.Fatalf("incorrect points quote: %+v", quote)
	}
	d.Client.Product.UpdateOneID(p.ID).SetPointsRequired(101).ExecX(ctx)
	in.QuoteKey = quote.QuoteKey
	if _, e = uc.CreateOrder(ctx, in); e == nil || !strings.Contains(e.Error(), "QUOTE_CHANGED") {
		t.Fatal("accepted changed points price", e)
	}
	quote, e = uc.CreateOrder(ctx, q)
	if e != nil || quote.PointsTotal != 303 {
		t.Fatal("new points not returned", e)
	}
	in.QuoteKey = quote.QuoteKey
	created, e := uc.CreateOrder(ctx, in)
	if e != nil || created.PointsTotal != 303 {
		t.Fatal("points purchase failed", e)
	}
	balance, e := w.GetPoints(ctx, 7)
	if e != nil || balance != 197 {
		t.Fatalf("points balance=%d %v", balance, e)
	}
}
