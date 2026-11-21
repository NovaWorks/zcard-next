package supply

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/procurement"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/id"
	"github.com/google/uuid"
)

type nativeJourneyGateway struct {
	*Gateway
	native *nativeSocialClient
}

func (g *nativeJourneyGateway) OpenSocial(context.Context, uint64) (supplyport.SocialClient, error) {
	return g.native, nil
}

func TestCSN19RetailQuotePayAndWorkerJourney(t *testing.T) {
	for _, mode := range []string{"completed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			n, d, provider, ctx, p := nativeSocialEnv(t)
			writer := catalog.NewProductRepoImpl(d, nil)
			syncer := NewSyncService(n.g.repo, writer, writer, nil, nil, nil, slog.Default())
			raw, _ := json.Marshal(provider.schema)
			var schemaData map[string]any
			_ = json.Unmarshal(raw, &schemaData)
			if _, e := syncer.ImportOne(ctx, n.conn, &adapter.Product{ID: "1", Name: "Imported 19csn service", IsActive: true, ProductKind: "social_service", DeliveryKind: "social_service", SocialSchema: schemaData, Stock: -2}, nil, PriceModeChannel, 0, 0); e != nil {
				t.Fatal("import service", e)
			}
			public, e := catalog.NewStoreCatalogService(catalog.NewCatalogUsecase(writer), nil, nil).GetProduct(ctx, &storefrontv1.GetProductRequest{Id: p.ID})
			if e != nil || public.ProductKind != "social_service" || public.PriceCents != 0 {
				t.Fatal("public service catalog", public, e)
			}
			billing := public.SocialSchema.AsMap()["billing"].(map[string]any)
			if _, exists := billing["rate"]; exists || billing["currency"] != "CNY" {
				t.Fatal("provider cost leaked through catalog", billing)
			}
			gateway := &nativeJourneyGateway{Gateway: n.g, native: n}
			cipher, e := inventory.NewCardCipher(make([]byte, 32))
			if e != nil {
				t.Fatal(e)
			}
			gen, e := id.NewGenerator(1)
			if e != nil {
				t.Fatal(e)
			}
			uc := &order.OrderUsecase{Data: d, Gen: gen, AccessCipher: cipher, StockGate: gateway}
			store := order.NewStoreSocialProductService(uc)
			schema, e := n.SocialSchema(ctx, "1")
			if e != nil {
				t.Fatal(e)
			}
			quote, e := store.Quote(ctx, &storefrontv1.SocialRetailQuoteRequest{ProductId: p.ID, SchemaRevision: schema.Revision, Parameters: map[string]string{"link": "https://example.com/target", "quantity": "1000"}})
			if e != nil || quote.GetAmountCents() != 13 {
				t.Fatal("retail quote", quote, e)
			}
			req := &storefrontv1.SocialRetailBuyRequest{ProductId: p.ID, QuoteId: quote.QuoteId, RequestId: uuid.NewString()}
			purchase, e := store.Buy(ctx, req)
			if e != nil || purchase.GetTotalCents() != 13 {
				t.Fatal("native order", purchase, e)
			}
			if provider.purchases != 0 || d.Client.SocialTask.Query().CountX(ctx) != 0 {
				t.Fatal("provider purchased before payment")
			}
			if e = uc.MarkPaid(ctx, purchase.OrderNo); e != nil {
				t.Fatal(e)
			}
			if e = uc.MarkPaid(ctx, purchase.OrderNo); e != nil {
				t.Fatal(e)
			}
			if d.Client.SocialTask.Query().CountX(ctx) != 1 {
				t.Fatal("duplicate paid task")
			}
			if mode == "unknown" {
				provider.purchaseErr = errors.New("connection interrupted after accepting order")
			}
			worker := procurement.NewProcureService(procurement.NewProcureRepo(d), gateway, nil, cipher, nil, nil, nil, nil, nil)
			worker.RunSocial(ctx)
			task := d.Client.SocialTask.Query().OnlyX(ctx)
			if !task.SubmitStarted || provider.purchases != 1 {
				t.Fatal("missing submission fence", task, provider.purchases)
			}
			if mode == "unknown" {
				worker.RunSocial(ctx)
				saved := d.Client.SocialTask.GetX(ctx, task.ID)
				if saved.Phase != "review" || saved.State != "submit_unknown" || provider.purchases != 1 || saved.RetailRefundedAmount != 0 {
					t.Fatal("uncertain charge retried/refunded", saved, provider.purchases)
				}
				return
			}
			if task.UpstreamOrderID != "23501" || task.ChargedAmount != 11 {
				t.Fatal("receipt not saved", task)
			}
			d.Client.SocialTask.UpdateOneID(task.ID).SetNextRunAt(0).ExecX(ctx)
			worker.RunSocial(ctx)
			saved := d.Client.SocialTask.GetX(ctx, task.ID)
			sale := d.Client.Order.Query().OnlyX(ctx)
			if saved.State != "completed" || saved.ChargedAmount != 11 || saved.RetailPaidAmount != 13 || sale.Status != "delivered" || provider.purchases != 1 {
				t.Fatal("service not delivered", saved, sale.Status, provider.purchases)
			}
			operationID := uuid.NewString()
			op := d.Client.SocialOperation.Create().SetID(uuid.NewString()).SetTaskID(task.ID).SetOperationID(operationID).SetAction("refill").SaveX(ctx)
			for attempt := 0; attempt < 2; attempt++ {
				d.Client.SocialTask.UpdateOneID(task.ID).SetNextRunAt(0).ExecX(ctx)
				worker.RunSocial(ctx)
			}
			operation := d.Client.SocialOperation.GetX(ctx, op.ID)
			if operation.UpstreamOperationID != "77" || operation.Status != "succeeded" || provider.operations != 1 {
				t.Fatal("worker lost provider refill ID or repeated operation", operation, provider.operations)
			}
		})
	}
}
