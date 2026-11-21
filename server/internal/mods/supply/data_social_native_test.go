package supply

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/google/uuid"
)

type nativeSocialFixture struct {
	schema                supplyport.SocialSchema
	purchases, operations int
	purchaseErr           error
	state                 string
}

func (f *nativeSocialFixture) SocialServices(context.Context) ([]supplyport.SocialService, error) {
	return []supplyport.SocialService{{ID: "1", Name: "fixture service", Category: "fixture", IsActive: true, Schema: f.schema}}, nil
}
func (f *nativeSocialFixture) CreateProviderSocial(context.Context, supplyport.SocialProviderPurchase) (*supplyport.SocialProviderOrder, error) {
	f.purchases++
	if f.purchaseErr != nil {
		return nil, f.purchaseErr
	}
	return &supplyport.SocialProviderOrder{UpstreamOrderID: "23501"}, nil
}
func (f *nativeSocialFixture) QueryProviderSocial(context.Context, string) (*supplyport.SocialProviderOrder, error) {
	return &supplyport.SocialProviderOrder{UpstreamOrderID: "23501", Currency: "USD", Charge: "0.015", Snapshot: supplyport.SocialSnapshot{State: f.state, CanRefill: true, CanCancel: true}}, nil
}
func (f *nativeSocialFixture) ActProviderSocial(context.Context, string, string, string) (*supplyport.SocialOperation, error) {
	f.operations++
	return &supplyport.SocialOperation{OperationID: "77", Status: "pending"}, nil
}
func (f *nativeSocialFixture) QueryProviderSocialOperation(_ context.Context, id, action string) (*supplyport.SocialOperation, error) {
	if id != "77" || action != "refill" {
		return nil, errors.New("provider refill identity lost")
	}
	return &supplyport.SocialOperation{OperationID: id, Status: "succeeded"}, nil
}
func nativeSocialEnv(t *testing.T) (*nativeSocialClient, *data.Data, *nativeSocialFixture, context.Context, *ent.Product) {
	t.Helper()
	repo, d := newTestRepo(t)
	ctx := identity.WithClaims(context.Background(), &authn.Claims{Subject: 7, Realm: authn.RealmUser})
	conn := d.Client.SupplyConnection.Create().SetName("19csn fixture").SetDriver("csn19").SetBaseURL("https://19csn.com/api/v2").SetCredentials([]byte("encrypted-fixture")).SetExchangeRate(7.2).SetPriceMarkupAmount(2).SaveX(ctx)
	p := d.Client.Product.Create().SetName("service").SetSlug("csn19-service").SetProductKind("social_service").SetDeliveryKind("social_service").SetUpstreamSourceID(conn.ID).SetUpstreamProductCode("1").SetStatus(1).SaveX(ctx)
	d.Client.SupplyMapping.Create().SetConnectionID(conn.ID).SetUpstreamProduct("1").SetLocalProductID(p.ID).SetPricingOverride(map[string]any{"rule": map[string]any{"mode": PriceModeChannel}}).SaveX(ctx)
	lo, hi := int64(50), int64(10000)
	s := supplyport.SocialSchema{Version: 1, Profile: "Default", Purchasable: true, Cancel: true, Refill: true, Fields: []supplyport.SocialField{{Key: "link", Type: "url", Required: true}, {Key: "quantity", Type: "integer", Required: true, Min: &lo, Max: &hi}}, Quantity: supplyport.SocialQuantity{Mode: "explicit", Field: "quantity", Min: &lo, Max: &hi}, Billing: supplyport.SocialBilling{Mode: "quantity", Rate: "0.015", RateUnit: 1000, Currency: "USD", Verified: true}}
	s.Revision = supplyport.SocialSchemaRevision(s)
	provider := &nativeSocialFixture{schema: s, state: "completed"}
	return &nativeSocialClient{g: NewGateway(repo, nil, nil), conn: conn, provider: provider}, d, provider, ctx, p
}
func nativePurchaseFixture(t *testing.T, n *nativeSocialClient, d *data.Data, ctx context.Context, p *ent.Product) (supplyport.SocialPurchase, *ent.SocialTask) {
	t.Helper()
	schema, e := n.SocialSchema(ctx, "1")
	if e != nil {
		t.Fatal(e)
	}
	if schema.Billing.Rate != "" || schema.Billing.Currency != "CNY" || schema.Billing.Mode != "quoted_total" {
		t.Fatal("private provider cost exposed", schema)
	}
	params := supplyport.SocialParameters{"link": "https://example.com/private-target", "quantity": "1000"}
	quote, e := n.QuoteSocial(ctx, "1", params, schema.Revision)
	if e != nil || quote.AmountCents != 11 {
		t.Fatal("exact total/FX rounding", quote, e)
	}
	sale, _, e := n.g.PriceSocialRetail(ctx, n.conn.ID, p.ID, int64(quote.AmountCents))
	if e != nil || sale != 13 {
		t.Fatal("FX applied twice or lost markup", sale, e)
	}
	qid := uuid.NewString()
	raw, _ := json.Marshal(schema)
	d.Client.SocialQuote.Create().SetID(qid).SetRole("retail").SetUserID(7).SetProductID(p.ID).SetConnectionID(n.conn.ID).SetSchemaRevision(schema.Revision).SetSchemaJSON(string(raw)).SetUpstreamProductID("1").SetAmountCents(13).SetCostCents(11).SetSupplyQuoteID(quote.QuoteID).SetExpiresAt(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	task := d.Client.SocialTask.Create().SetID(uuid.NewString()).SetRole("retail").SetUserID(7).SetProductID(p.ID).SetConnectionID(n.conn.ID).SetConnectionIdentity(data.SocialConnectionIdentity(n.conn)).SetQuoteID(quote.QuoteID).SetSchemaRevision(schema.Revision).SetRequestNo(uuid.NewString()).SetRequestHash("fixture").SetUpstreamProductID("1").SetCostCents(11).SetRetailPaidAmount(13).SetLeaseToken("lease").SetLeaseUntil(time.Now().Add(time.Minute).Unix()).SaveX(ctx)
	private, e := d.Client.SocialQuote.Get(ctx, quote.QuoteID)
	if e != nil {
		t.Fatal(e)
	}
	if private.ParamsJSON != "" || len(private.ParamsCipher) == 0 || strings.Contains(string(private.ParamsCipher), "private-target") {
		t.Fatal("purchase parameters stored in clear")
	}
	return supplyport.SocialPurchase{QuoteID: quote.QuoteID, ProductID: "1", SchemaRevision: schema.Revision, Parameters: params, DownstreamOrderNo: task.RequestNo, RequiredCapability: supplyport.SocialCapability, MaxSupplyAmountCents: 11, Currency: "CNY"}, task
}
func TestNativeSocialQuotePurchaseQueryAndRefill(t *testing.T) {
	n, d, provider, ctx, p := nativeSocialEnv(t)
	req, task := nativePurchaseFixture(t, n, d, ctx, p)
	result, e := n.CreateSocial(ctx, req)
	if e != nil || !result.Charged || result.Amount != 11 || provider.purchases != 1 {
		t.Fatal(result, e, provider.purchases)
	}
	d.Client.SocialTask.UpdateOneID(task.ID).SetUpstreamOrderID(result.SupplyOrderID).SetChargedAmount(11).ExecX(ctx)
	if _, e = n.CreateSocial(ctx, req); !errors.Is(e, supplyport.ErrSocialSubmitUnknown) || provider.purchases != 1 {
		t.Fatal("submission replayed", e, provider.purchases)
	}
	for _, state := range []string{"completed", "partial", "canceled"} {
		provider.state = state
		orders, e := n.QuerySocial(ctx, []string{result.SupplyOrderID})
		if e != nil || len(orders) != 1 {
			t.Fatal(orders, e)
		}
		snap := orders[0].Snapshot
		if snap.PaidAmountCents != 11 || snap.RefundedAmountCents != 0 || snap.Currency != "CNY" || *snap.Quantity != 1000 {
			t.Fatal("invented refund or lost quote", snap)
		}
		if state == "completed" && snap.SettlementState != "paid" {
			t.Fatal(snap)
		}
		if state != "completed" && snap.SettlementState != "review" {
			t.Fatal("partial/cancel inferred a money receipt", snap)
		}
	}
	operationID := uuid.NewString()
	op := d.Client.SocialOperation.Create().SetID(uuid.NewString()).SetTaskID(task.ID).SetOperationID(operationID).SetAction("refill").SaveX(ctx)
	operation, e := n.ActSocial(ctx, "23501", "refill", operationID)
	if e != nil || operation.OperationID != operationID || operation.ProviderOperationID == nil || *operation.ProviderOperationID != "77" {
		t.Fatal(operation, e)
	}
	d.Client.SocialOperation.UpdateOneID(op.ID).SetUpstreamOperationID(*operation.ProviderOperationID).ExecX(ctx)
	status, e := n.QuerySocialOperation(ctx, "23501", operationID)
	if e != nil || status.Status != "succeeded" {
		t.Fatal(status, e)
	}
	if _, e = n.ActSocial(ctx, "23501", "refill", operationID); e != nil || provider.operations != 1 {
		t.Fatal("refill replayed", e, provider.operations)
	}
}
func TestNativeSocialUnknownAndChangedQuoteNeverResubmit(t *testing.T) {
	for _, mode := range []string{"timeout", "rejected", "rate_changed"} {
		t.Run(mode, func(t *testing.T) {
			n, d, provider, ctx, p := nativeSocialEnv(t)
			req, task := nativePurchaseFixture(t, n, d, ctx, p)
			switch mode {
			case "timeout":
				provider.purchaseErr = errors.New("connection lost")
			case "rejected":
				provider.purchaseErr = &supplyport.SocialRejected{Code: "provider_rejected"}
			case "rate_changed":
				provider.schema.Revision = "updated-rate"
			}
			result, e := n.CreateSocial(ctx, req)
			if mode == "timeout" {
				if !errors.Is(e, supplyport.ErrSocialSubmitUnknown) {
					t.Fatal(result, e)
				}
				if _, e = n.CreateSocial(ctx, req); !errors.Is(e, supplyport.ErrSocialSubmitUnknown) || provider.purchases != 1 {
					t.Fatal("uncertain purchase replayed", e, provider.purchases)
				}
				saved := d.Client.SocialTask.GetX(ctx, task.ID)
				if !saved.SubmitStarted {
					t.Fatal("missing durable submission marker")
				}
			} else {
				if e != nil || result.Status != "rejected" || result.Charged || result.Amount != 0 {
					t.Fatal("unsafe rejection", result, e)
				}
				if mode == "rate_changed" && provider.purchases != 0 {
					t.Fatal("changed price reached provider")
				}
			}
		})
	}
}
